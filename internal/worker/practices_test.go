package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	practicesTip    = "1111111111111111111111111111111111111111"
	practicesLater  = "2222222222222222222222222222222222222222"
	checkoutWidgets = "/work/checkouts/widgets"
	checkoutGadgets = "/work/checkouts/gadgets"
)

// practicesRig is a worker that runs the practices check on checkouts it keeps, over fakes of the git and forge
// reads, with a control plane that asks it to.
type practicesRig struct {
	*rig
	state     string
	fakes     *fake.Practices
	practices *control.Practices
	// checkouts is where this worker keeps each repository, by ID.
	checkouts map[string]string
	// reported collects what the worker said it could not do.
	reported   *errorLog
	subject    *worker.Worker
	hosts      map[string]control.AssistantHost
	sources    practicerun.Sources
	configured func(*worker.Config)
}

// errorLog keeps the errors a worker reports, which a test reads once the worker is done.
type errorLog struct {
	mu   sync.Mutex
	list []error
}

func (l *errorLog) add(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, err)
}

func (l *errorLog) all() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.list)
}

func newPracticesRig(t *testing.T) *practicesRig {
	t.Helper()
	base := newRig(t)
	r := &practicesRig{
		rig: base, state: t.TempDir(), fakes: &fake.Practices{}, reported: &errorLog{},
		checkouts: map[string]string{"repo-widgets": checkoutWidgets, "repo-gadgets": checkoutGadgets},
		hosts:     map[string]control.AssistantHost{},
	}
	r.fakes.SetSnapshot(checkoutWidgets, practices.Snapshot{Commit: practicesTip, Paths: []string{"SECURITY.md"}})
	r.fakes.SetSnapshot(checkoutGadgets, practices.Snapshot{Commit: practicesLater, Paths: []string{"SECURITY.md"}})
	r.sources = practicerun.Sources{Snapshots: r.fakes, Secrets: r.fakes, Forge: r.fakes, Clock: fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC))}
	r.practices = control.NewPractices(base.store).Placing(
		func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
			return r.hosts, nil
		},
		0,
		func(context.Context) (string, error) { return r.subject.Auth().WorkerID, nil },
	)
	for id, name := range map[string]string{"repo-widgets": "widgets", "repo-gadgets": "gadgets"} {
		if _, err := control.NewRepositories(base.store).Detect(t.Context(), scope, control.DetectedRepository{
			ID: id, Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: name,
		}, member); err != nil {
			t.Fatal(err)
		}
	}
	r.subject = r.workerOn(base.link, r.sources)
	if err := r.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.hosts[r.subject.Auth().WorkerID] = control.AssistantHost{Checkouts: map[string]bool{"repo-widgets": true, "repo-gadgets": true}}
	return r
}

// workerOn is a worker on a link, running the checks through sources, with the state directory this rig keeps.
func (r *practicesRig) workerOn(link ports.WorkerLink, sources practicerun.Sources) *worker.Worker {
	config := worker.Config{
		Name: "test",
		JoinToken: func(ctx context.Context) (string, error) {
			return r.plane.CreateJoinToken(ctx, member)
		},
		Report: r.reported.add,
		Practices: &worker.PracticesRuntime{
			Sources: sources, StateDir: r.state,
			Repository: func(_ context.Context, _, id string) (string, error) {
				if dir, ok := r.checkouts[id]; ok {
					return dir, nil
				}
				return "", fmt.Errorf("repository %s: %w", id, ports.ErrNotFound)
			},
		},
	}
	if r.configured != nil {
		r.configured(&config)
	}
	return worker.New(link, r.journal, r.clock, config)
}

// ask makes a member's request for each repository and returns the requests.
func (r *practicesRig) ask(repositories ...string) []ledger.PracticesRequest {
	r.t.Helper()
	asked, err := r.practices.Request(r.t.Context(), scope, repositories, ledger.PracticesMember, member)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []ledger.PracticesRequest
	for _, one := range asked {
		if one.Request == nil {
			r.t.Fatalf("no request for %s: %s", one.Repository.Name, one.Reason)
		}
		out = append(out, *one.Request)
	}
	return out
}

func (r *practicesRig) converge(subject *worker.Worker) {
	r.t.Helper()
	if err := subject.Converge(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *practicesRig) request(id string) ledger.PracticesRequest {
	r.t.Helper()
	var request ledger.PracticesRequest
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) (err error) {
		request, err = tx.Practices().Request(id)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return request
}

func (r *practicesRig) result(repository string) (ledger.PracticesRecord, error) {
	var record ledger.PracticesRecord
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) (err error) {
		record, err = tx.Practices().Result(repository)
		return err
	})
	return record, err
}

func (r *practicesRig) eventsOf(kind string) []ledger.Event {
	r.t.Helper()
	var out []ledger.Event
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if event.Type == kind {
				out = append(out, event)
			}
		}
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *practicesRig) journalOf(id string) string {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.state, "practices", id+".json"))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw)
}

// offer hands a worker checks in its desired state, as the control plane would, and lets it act on them.
func (r *practicesRig) offer(subject *worker.Worker, checks ...ledger.DesiredPracticesCheck) {
	r.t.Helper()
	if err := r.journal.SaveDesired(r.t.Context(), ledger.DesiredState{Rev: 1, PracticesChecks: checks}); err != nil {
		r.t.Fatal(err)
	}
	if _, err := subject.Reconcile(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	subject.Wait()
}

// capturedPractices stands between a worker and the control plane and keeps what the worker reports of a check.
type capturedPractices struct {
	ports.WorkerLink
	mu      sync.Mutex
	reports []ledger.PracticesReport
	// answer is what each report is answered with, and nil accepts it.
	answer error
	// forward sends each report the control plane too, when the answer accepts it.
	forward bool
}

func (c *capturedPractices) ReportPracticesCheck(_ context.Context, report ledger.PracticesReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, report)
	if c.answer == nil && c.forward {
		return c.WorkerLink.ReportPracticesCheck(context.Background(), report)
	}
	return c.answer
}

func (c *capturedPractices) all() []ledger.PracticesReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reports)
}

func (c *capturedPractices) only(t *testing.T) ledger.PracticesReport {
	t.Helper()
	reports := c.all()
	if len(reports) != 1 {
		t.Fatalf("%d reports: %+v", len(reports), reports)
	}
	return reports[0]
}

// A worker takes the check it is offered, runs it on the checkout it keeps with the settings the request carries, and
// reports the result, which the control plane records.
func TestAWorkerRunsAnOfferedPracticesCheckAndReportsIt(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	request := r.ask("repo-widgets")[0]
	r.converge(r.subject)
	if got := r.request(request.ID); got.State != ledger.PracticesReported {
		t.Fatalf("the request is %s", got.State)
	}
	record, err := r.result("repo-widgets")
	if err != nil || record.Commit != practicesTip || record.Request != request.ID {
		t.Fatalf("the result is %+v (%v)", record, err)
	}
	if record.Result.Worker != "test" || record.Result.Repository != "repo-widgets" || record.Result.BaseBranch != "main" {
		t.Errorf("the result names %q, %q, and %q", record.Result.Worker, record.Result.Repository, record.Result.BaseBranch)
	}
	if want := [][2]string{{checkoutWidgets, "main"}}; !slices.Equal(r.fakes.SnapshotCalls, want) {
		t.Errorf("the snapshots read %v, want %v", r.fakes.SnapshotCalls, want)
	}
	if want := [][3]string{{checkoutWidgets, practicesTip, ""}}; !slices.Equal(r.fakes.ScanCalls, want) {
		t.Errorf("the scans were %v, want %v", r.fakes.ScanCalls, want)
	}
	if len(r.fakes.SettingsCalls) != 0 {
		t.Errorf("a repository with no forge binding had its settings read: %v", r.fakes.SettingsCalls)
	}
	if journal := r.journalOf(request.ID); !strings.Contains(journal, `"reported"`) {
		t.Errorf("the journal says %q, want reported", journal)
	}
	if errs := r.reported.all(); len(errs) != 0 {
		t.Errorf("the worker reported %v", errs)
	}
	// Taken again, a request the control plane no longer offers is run no more.
	r.converge(r.subject)
	if len(r.fakes.SnapshotCalls) != 1 {
		t.Errorf("the check ran %d times", len(r.fakes.SnapshotCalls))
	}
}

// The next check of a repository starts its secret scan where the last completed scan ended, and reads the forge when
// the repository has a binding.
func TestAWorkerStartsTheScanWhereTheLastOneEndedAndReadsTheForge(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	binding := "github.com/acme/widgets"
	if _, err := control.NewRepositories(r.store).Change(t.Context(), scope, "repo-widgets", control.RepositoryChange{ForgeBinding: &binding}, member); err != nil {
		t.Fatal(err)
	}
	r.ask("repo-widgets")
	r.converge(r.subject)
	r.ask("repo-widgets")
	r.converge(r.subject)
	want := [][3]string{{checkoutWidgets, practicesTip, ""}, {checkoutWidgets, practicesTip, practicesTip}}
	if !slices.Equal(r.fakes.ScanCalls, want) {
		t.Errorf("the scans were %v, want %v", r.fakes.ScanCalls, want)
	}
	if want := [][2]string{{binding, "main"}, {binding, "main"}}; !slices.Equal(r.fakes.SettingsCalls, want) {
		t.Errorf("the settings reads were %v, want %v", r.fakes.SettingsCalls, want)
	}
}

// concurrencyProbe counts how many snapshot reads run at once.
type concurrencyProbe struct {
	ports.PracticeSnapshots
	running, most atomic.Int64
}

func (p *concurrencyProbe) Snapshot(ctx context.Context, dir, commit string) (practices.Snapshot, error) {
	now := p.running.Add(1)
	defer p.running.Add(-1)
	for {
		seen := p.most.Load()
		if now <= seen || p.most.CompareAndSwap(seen, now) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	return p.PracticeSnapshots.Snapshot(ctx, dir, commit)
}

// A worker runs the checks it is offered one at a time, and runs each.
func TestAWorkerRunsPracticesChecksOneAtATime(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	probe := &concurrencyProbe{PracticeSnapshots: r.fakes}
	sources := r.sources
	sources.Snapshots = probe
	subject := r.workerOn(r.link, sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.hosts[subject.Auth().WorkerID] = control.AssistantHost{Checkouts: map[string]bool{"repo-widgets": true, "repo-gadgets": true}}
	asked := r.ask()
	if len(asked) != 2 {
		t.Fatalf("%d requests", len(asked))
	}
	r.converge(subject)
	for _, request := range asked {
		if got := r.request(request.ID); got.State != ledger.PracticesReported {
			t.Errorf("request %s is %s", request.ID, got.State)
		}
	}
	if probe.most.Load() != 1 {
		t.Errorf("%d checks ran at once, want one at a time", probe.most.Load())
	}
}

// A worker that crashes after the run and before the report is heard has run the check once, and the worker that
// starts after it, on the same state directory, sends what the first one decided and runs nothing again: the control
// plane records one report.
func TestACrashBetweenTheRunAndTheReportReportsOnceAndRunsNothingTwice(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	request := r.ask("repo-widgets")[0]
	// The first worker runs the check, and its report never arrives.
	r.link.FailPracticesReport.Store(true)
	r.converge(r.subject)
	if errs := r.reported.all(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "reporting it") {
		t.Fatalf("the first worker reported %v, want its lost report", errs)
	}
	if got := r.request(request.ID); got.State != ledger.PracticesOffered {
		t.Fatalf("the request is %s after a report that never arrived", got.State)
	}
	if journal := r.journalOf(request.ID); !strings.Contains(journal, `"ran"`) || !strings.Contains(journal, practicesTip) {
		t.Fatalf("the journal says %q, want the run's outcome kept", journal)
	}
	ran := len(r.fakes.SnapshotCalls)
	// The worker that starts after the crash takes the same request again, and nothing in its reads can run.
	r.link.FailPracticesReport.Store(false)
	broken := r.sources
	broken.Snapshots, broken.Secrets, broken.Forge = refusingReads{}, refusingReads{}, refusingReads{}
	r.reported = &errorLog{}
	restarted := r.workerOn(r.link, broken)
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.converge(restarted)
	if errs := r.reported.all(); len(errs) != 0 {
		t.Errorf("the restarted worker reported %v", errs)
	}
	if got := r.request(request.ID); got.State != ledger.PracticesReported {
		t.Fatalf("the request is %s after the restart", got.State)
	}
	if record, err := r.result("repo-widgets"); err != nil || record.Commit != practicesTip {
		t.Errorf("the result is %+v (%v)", record, err)
	}
	if len(r.fakes.SnapshotCalls) != ran {
		t.Errorf("the restarted worker ran the check again")
	}
	if journal := r.journalOf(request.ID); !strings.Contains(journal, `"reported"`) {
		t.Errorf("the journal says %q, want reported", journal)
	}
	r.converge(restarted)
	if reports := r.eventsOf(ledger.EventPracticesChecked); len(reports) != 1 {
		t.Errorf("the log holds %d reports, want one", len(reports))
	}
	if got := r.link.PracticesReports.Load(); got != 1 {
		t.Errorf("%d reports reached the control plane, want one", got)
	}
}

// refusingReads is the three reads of a worker whose machine cannot run a check.
type refusingReads struct{}

var errRefused = errors.New("the reads of this worker must not run")

func (refusingReads) Snapshot(context.Context, string, string) (practices.Snapshot, error) {
	return practices.Snapshot{}, errRefused
}

func (refusingReads) Scan(context.Context, string, string, string) (ports.SecretScan, error) {
	return ports.SecretScan{}, errRefused
}

func (refusingReads) Settings(context.Context, ports.ForgeRepo, string) (practices.ForgeFacts, error) {
	return practices.ForgeFacts{}, errRefused
}

// A report the control plane recorded and whose answer the worker lost is sent again, and the control plane, which
// keeps the first report, records one.
func TestALostAnswerToAReportIsSentAgainAndRecordedOnce(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	request := r.ask("repo-widgets")[0]
	lost := &lostAnswer{WorkerLink: r.link}
	subject := r.workerOn(lost, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.hosts[subject.Auth().WorkerID] = control.AssistantHost{Checkouts: map[string]bool{"repo-widgets": true}}
	r.converge(subject)
	if got := r.request(request.ID); got.State != ledger.PracticesReported {
		t.Fatalf("the request is %s", got.State)
	}
	if journal := r.journalOf(request.ID); !strings.Contains(journal, `"ran"`) {
		t.Fatalf("the journal says %q, want it to say the report was not confirmed", journal)
	}
	subject2 := r.workerOn(r.link, r.sources)
	if err := subject2.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.offer(subject2, ledger.DesiredPracticesCheck{ID: request.ID, Repository: "repo-widgets", BaseBranch: "main", MergeAuthority: "person_merges", ExpiresAt: 1 << 60})
	if reports := r.eventsOf(ledger.EventPracticesChecked); len(reports) != 1 {
		t.Errorf("the log holds %d reports, want one", len(reports))
	}
	if journal := r.journalOf(request.ID); !strings.Contains(journal, `"reported"`) {
		t.Errorf("the journal says %q, want reported", journal)
	}
	if len(r.fakes.SnapshotCalls) != 1 {
		t.Errorf("the check ran %d times, want once", len(r.fakes.SnapshotCalls))
	}
}

// lostAnswer delivers the first report and then fails it, as a connection that drops after the control plane
// recorded it would.
type lostAnswer struct {
	ports.WorkerLink
	done atomic.Bool
}

func (l *lostAnswer) ReportPracticesCheck(ctx context.Context, report ledger.PracticesReport) error {
	if err := l.WorkerLink.ReportPracticesCheck(ctx, report); err != nil {
		return err
	}
	if l.done.CompareAndSwap(false, true) {
		return errors.New("the connection dropped")
	}
	return nil
}

// A snapshot that cannot be read is a failed check, and its report says why without the path of the checkout, on one
// line and bounded; the control plane keeps the previous result.
func TestAFailedRunReportsADetailWithoutTheCheckoutPath(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	first := r.ask("repo-widgets")[0]
	r.converge(r.subject)
	second := r.ask("repo-widgets")[0]
	captured := &capturedPractices{WorkerLink: r.link, forward: true}
	failing := r.sources
	failing.Snapshots = failingSnapshots{err: fmt.Errorf("git -C %s ls-tree: exit status 128\nfatal: not a git repository: %s/.git %s", checkoutWidgets, checkoutWidgets, strings.Repeat("€", 3000))}
	subject := r.workerOn(captured, failing)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.hosts[subject.Auth().WorkerID] = control.AssistantHost{Checkouts: map[string]bool{"repo-widgets": true}}
	r.offer(subject, ledger.DesiredPracticesCheck{ID: second.ID, Repository: "repo-widgets", BaseBranch: "main", MergeAuthority: "person_merges", ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.PracticesOutcomeFailed || report.Result != nil || report.Request != second.ID {
		t.Fatalf("the report is %+v, want a failure of the second request", report)
	}
	if strings.Contains(report.Detail, checkoutWidgets) || strings.Contains(report.Detail, "/work/checkouts") {
		t.Errorf("the detail names the checkout: %.200s", report.Detail)
	}
	if !strings.Contains(report.Detail, "<checkout>") || !strings.Contains(report.Detail, "not a git repository") {
		t.Errorf("the detail is %.200s, want what failed with the checkout replaced", report.Detail)
	}
	if len(report.Detail) > practicerun.MaxReason || strings.ContainsAny(report.Detail, "\r\n") {
		t.Errorf("the detail is %d bytes on %d lines, want one bounded line", len(report.Detail), strings.Count(report.Detail, "\n")+1)
	}
	if journal := r.journalOf(second.ID); !strings.Contains(journal, `"reported"`) || strings.Contains(journal, checkoutWidgets) {
		t.Errorf("the journal says %.200s", journal)
	}
	if got := r.request(second.ID); got.State != ledger.PracticesReported {
		t.Errorf("the failed request is %s", got.State)
	}
	if record, err := r.result("repo-widgets"); err != nil || record.Request != first.ID {
		t.Errorf("the result is %+v (%v), want the first request's kept", record, err)
	}
	if events := r.eventsOf(ledger.EventPracticesFailed); len(events) != 1 || strings.Contains(string(events[0].Data), "/work/checkouts") {
		t.Errorf("the log holds %d failed events, or one names the checkout", len(events))
	}
}

type failingSnapshots struct{ err error }

func (f failingSnapshots) Snapshot(context.Context, string, string) (practices.Snapshot, error) {
	return practices.Snapshot{}, f.err
}

type failingScans struct{ err error }

func (f failingScans) Scan(context.Context, string, string, string) (ports.SecretScan, error) {
	return ports.SecretScan{}, f.err
}

// The reason a result gives for a practice it could not decide holds no path of the worker's machine either.
func TestAResultsReasonsNameNoCheckoutPath(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	captured := &capturedPractices{WorkerLink: r.link}
	broken := r.sources
	broken.Secrets = failingScans{err: fmt.Errorf("git -C %s log: exit status 128", checkoutWidgets)}
	subject := r.workerOn(captured, broken)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.offer(subject, ledger.DesiredPracticesCheck{ID: "req-1", Repository: "repo-widgets", BaseBranch: "main", MergeAuthority: "person_merges", ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.PracticesOutcomeChecked || report.Result == nil {
		t.Fatalf("the report is %+v", report)
	}
	for _, status := range report.Result.Practices {
		if strings.Contains(status.Reason, "/work/checkouts") {
			t.Errorf("%s says %q", status.ID, status.Reason)
		}
	}
	var secrets practices.PracticeStatus
	for _, status := range report.Result.Practices {
		if status.ID == practices.Secrets {
			secrets = status
		}
	}
	if secrets.Status != practices.StatusUnknown || !strings.Contains(secrets.Reason, "git -C <checkout> log") {
		t.Errorf("secrets are %+v, want unknown with the checkout replaced", secrets)
	}
}

// A request for a repository this worker keeps no checkout of is a failed check that says so.
func TestAWorkerThatKeepsNoCheckoutReportsAFailure(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	captured := &capturedPractices{WorkerLink: r.link}
	subject := r.workerOn(captured, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.offer(subject, ledger.DesiredPracticesCheck{ID: "req-1", Repository: "repo-elsewhere", BaseBranch: "main", MergeAuthority: "person_merges", ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.PracticesOutcomeFailed || !strings.Contains(report.Detail, "keeps no checkout of repository repo-elsewhere") {
		t.Errorf("the report is %+v", report)
	}
	if len(r.fakes.SnapshotCalls) != 0 {
		t.Errorf("a check ran with no checkout")
	}
}

// A check nobody started that is past its expiry is not run, a check whose run was journaled is reported however late,
// and one whose journal says reported is neither run nor reported again.
func TestAWorkerHonorsTheExpiryAndTheJournalOfAnOfferedCheck(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	if err := os.MkdirAll(filepath.Join(r.state, "practices"), 0o700); err != nil {
		t.Fatal(err)
	}
	for id, journal := range map[string]string{
		"req-ran":      `{"state":"ran","report":{"request":"req-ran","outcome":"failed","detail":"the run failed"}}`,
		"req-reported": `{"state":"reported","report":{"request":"req-reported","outcome":"failed","detail":"the run failed"}}`,
	} {
		if err := os.WriteFile(filepath.Join(r.state, "practices", id+".json"), []byte(journal), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	captured := &capturedPractices{WorkerLink: r.link}
	subject := r.workerOn(captured, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	past := r.clock.Now().UnixMilli() - 1
	r.offer(subject,
		ledger.DesiredPracticesCheck{ID: "req-late", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: past},
		ledger.DesiredPracticesCheck{ID: "req-ran", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: past},
		ledger.DesiredPracticesCheck{ID: "req-reported", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: 1 << 60},
	)
	reports := captured.all()
	if len(reports) != 1 || reports[0].Request != "req-ran" || reports[0].Detail != "the run failed" || reports[0].Outcome != ledger.PracticesOutcomeFailed {
		t.Errorf("the worker reported %+v, want only the journaled run of req-ran", reports)
	}
	if len(r.fakes.SnapshotCalls) != 0 {
		t.Errorf("a check ran: %v", r.fakes.SnapshotCalls)
	}
}

// A request whose ID is no file name is refused, and the worker writes no journal outside its own.
func TestAWorkerRefusesARequestIDThatIsNoFileName(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	subject := r.workerOn(r.link, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(r.state), "escaped")
	r.offer(subject, ledger.DesiredPracticesCheck{ID: "../../escaped", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: 1 << 60})
	if _, err := os.Stat(outside + ".json"); err == nil {
		t.Error("the worker wrote a journal outside its state directory")
	}
	if len(r.fakes.SnapshotCalls) != 0 {
		t.Error("the check ran")
	}
	if errs := r.reported.all(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "no file name") {
		t.Errorf("the worker reported %v, want a refusal", errs)
	}
}

// A report the control plane refuses for good is not sent again: the worker journals it as reported and says so once.
func TestAReportTheControlPlaneRefusesForGoodIsNotSentAgain(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	captured := &capturedPractices{WorkerLink: r.link, answer: fmt.Errorf("%w: the result is of another repository", workorders.ErrInvalid)}
	subject := r.workerOn(captured, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := ledger.DesiredPracticesCheck{ID: "req-1", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: 1 << 60}
	r.offer(subject, check)
	r.offer(subject, check)
	if got := len(captured.all()); got != 1 {
		t.Errorf("%d reports were sent, want one", got)
	}
	if errs := r.reported.all(); len(errs) != 1 || !errors.Is(errs[0], workorders.ErrInvalid) {
		t.Errorf("the worker reported %v, want the refusal once", errs)
	}
	if journal := r.journalOf("req-1"); !strings.Contains(journal, `"reported"`) {
		t.Errorf("the journal says %q, want reported", journal)
	}
}

// A report the control plane could not be reached for is sent again on the next pass.
func TestAReportThatFailsToArriveIsSentAgainOnTheNextPass(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	captured := &capturedPractices{WorkerLink: r.link, answer: errors.New("the control plane is away")}
	subject := r.workerOn(captured, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := ledger.DesiredPracticesCheck{ID: "req-1", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: 1 << 60}
	r.offer(subject, check)
	captured.mu.Lock()
	captured.answer = nil
	captured.mu.Unlock()
	r.offer(subject, check)
	if got := len(captured.all()); got != 2 {
		t.Errorf("%d reports were sent, want the lost one and its resend", got)
	}
	if got := len(r.fakes.SnapshotCalls); got != 1 {
		t.Errorf("the check ran %d times, want once", got)
	}
}

// A settings read the forge refused for a rate limit still reports the result its other facts give, and holds the
// repository's reads as the worker does for any other refusal.
func TestARateLimitedRunIsReportedAndHoldsTheRepositorysReads(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	binding := "github.com/acme/widgets"
	r.fakes.SetSettings(binding, practices.ForgeFacts{Visibility: practices.VisibilityPublic, Protected: true, ReviewCount: 1})
	r.fakes.SetSettingsError(binding, &ports.RateLimitedError{Detail: "secondary limit"})
	var notices []string
	r.configured = func(c *worker.Config) { c.Notice = func(text string) { notices = append(notices, text) } }
	captured := &capturedPractices{WorkerLink: r.link}
	subject := r.workerOn(captured, r.sources)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.offer(subject, ledger.DesiredPracticesCheck{ID: "req-1", Repository: "repo-widgets", BaseBranch: "main", Binding: binding, MergeAuthority: "person_merges", ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.PracticesOutcomeChecked || report.Result == nil || report.Result.Commit != practicesTip {
		t.Fatalf("the report is %+v, want the result the run gave", report)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "rate limiting") {
		t.Errorf("the worker noted %v", notices)
	}
	if errs := r.reported.all(); len(errs) != 0 {
		t.Errorf("the worker reported %v", errs)
	}
}

// A run that ends with the worker's context is no failed check: nothing is journaled and nothing is reported, so the
// next worker runs it.
func TestACheckThatEndsWithTheWorkersContextIsNeitherJournaledNorReported(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	captured := &capturedPractices{WorkerLink: r.link}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	canceling := r.sources
	canceling.Snapshots = cancelingSnapshots{cancel: cancel}
	subject := r.workerOn(captured, canceling)
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.SaveDesired(t.Context(), ledger.DesiredState{Rev: 1, PracticesChecks: []ledger.DesiredPracticesCheck{
		{ID: "req-1", Repository: "repo-widgets", BaseBranch: "main", ExpiresAt: 1 << 60},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := subject.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	subject.Wait()
	if len(captured.all()) != 0 {
		t.Errorf("a canceled run was reported: %+v", captured.all())
	}
	if _, err := os.Stat(filepath.Join(r.state, "practices", "req-1.json")); err == nil {
		raw, _ := os.ReadFile(filepath.Join(r.state, "practices", "req-1.json"))
		t.Errorf("a canceled run was journaled: %s", bytes.TrimSpace(raw))
	}
}

type cancelingSnapshots struct{ cancel context.CancelFunc }

func (c cancelingSnapshots) Snapshot(ctx context.Context, _, _ string) (practices.Snapshot, error) {
	c.cancel()
	<-ctx.Done()
	return practices.Snapshot{}, ctx.Err()
}

// The journal keeps the report as the control plane reads it, so a restart sends exactly what the first run decided.
func TestTheJournalKeepsTheReportItWillSend(t *testing.T) {
	t.Parallel()
	r := newPracticesRig(t)
	r.link.FailPracticesReport.Store(true)
	request := r.ask("repo-widgets")[0]
	r.converge(r.subject)
	var journal struct {
		State  string                  `json:"state"`
		Report *ledger.PracticesReport `json:"report"`
	}
	if err := json.Unmarshal([]byte(r.journalOf(request.ID)), &journal); err != nil {
		t.Fatal(err)
	}
	if journal.State != "ran" || journal.Report == nil || journal.Report.Outcome != ledger.PracticesOutcomeChecked ||
		journal.Report.Result == nil || journal.Report.Result.Commit != practicesTip || journal.Report.Request != request.ID {
		t.Errorf("the journal holds %+v", journal)
	}
}
