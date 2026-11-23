package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// laneRig is one worker with a fake driver and one dispatched work order.
type laneRig struct {
	*rig
	subject *worker.Worker
	driver  *fake.AgentDriver
	config  *worker.LaneRuntime
	id      string
	grant   workorders.Grant
	dir     string
	// stopWorker ends the context the lanes run under.
	stopWorker func()
	// telemetry is what the worker emitted.
	telemetry *fake.Telemetry
	// problemLock guards problemList, which holds what the worker reported it could not do.
	problemLock sync.Mutex
	problemList []error
	// noticeList holds what the worker said that is not a failure, which problemLock guards too.
	noticeList []string
}

func (r *laneRig) noteNotice(notice string) {
	r.problemLock.Lock()
	defer r.problemLock.Unlock()
	r.noticeList = append(r.noticeList, notice)
}

// notices are what the worker said it did that is not a failure.
func (r *laneRig) notices() []string {
	r.problemLock.Lock()
	defer r.problemLock.Unlock()
	return slices.Clone(r.noticeList)
}

func (r *laneRig) noteProblem(err error) {
	r.problemLock.Lock()
	defer r.problemLock.Unlock()
	r.problemList = append(r.problemList, err)
}

// problems are the failures the worker reported and went on from.
func (r *laneRig) problems() []error {
	r.problemLock.Lock()
	defer r.problemLock.Unlock()
	return slices.Clone(r.problemList)
}

func newLaneRig(t *testing.T) *laneRig {
	t.Helper()
	return newLaneRigFor(t, newRig(t))
}

// newLaneRigOn sets a driver up before its worker asks the doctor what it can run.
func newLaneRigOn(t *testing.T, setup func(*fake.AgentDriver)) *laneRig {
	t.Helper()
	return newLaneRigWith(t, newRig(t), setup)
}

// newLaneRigFor builds on a rig a test has already set up.
func newLaneRigFor(t *testing.T, base *rig) *laneRig {
	t.Helper()
	return newLaneRigWith(t, base, func(*fake.AgentDriver) {})
}

func newLaneRigWith(t *testing.T, base *rig, setup func(*fake.AgentDriver)) *laneRig {
	t.Helper()
	driver := fake.NewAgentDriver()
	setup(driver)
	dir := t.TempDir()
	worktrees := base.worktrees
	if worktrees == nil {
		worktrees = fake.NewWorktrees()
	}
	config := &worker.LaneRuntime{
		Driver: driver,
		Provisioner: worker.Provisioner{
			Profiles: fake.NewProfileSource(), Worktrees: worktrees, Hooks: fake.NewHookRunner(),
			Lookup: func(string) (string, bool) { return "", false },
		},
		Secrets:    fake.SecretResolver{},
		Repository: func(context.Context, string, string) (string, error) { return "/repository", nil },
		Socket:     "/run/worker.sock", Spool: "/state/hooks.spool", Binary: "/state/bin/ropeshift",
		StateDir: dir, RunDir: dir, PaneInputs: &worker.PaneInputs{},
	}
	r := &laneRig{rig: base, driver: driver, config: config, dir: dir, telemetry: fake.NewTelemetry()}
	// It runs after the cleanups of the test, and before the one that closes the journal a step writes.
	t.Cleanup(func() { endSessions(t, driver) })
	var journal ports.Journal = base.journal
	if base.workerJournal != nil {
		journal = base.workerJournal
	}
	var link ports.WorkerLink = base.link
	if base.workerLink != nil {
		link = base.workerLink
	}
	r.subject = worker.New(link, journal, base.clock, worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
		Lanes:     config,
		Spool:     filepath.Join(dir, "spool"),
		Telemetry: r.telemetry,
		Report:    r.noteProblem,
		Notice:    r.noteNotice,
	})
	if err := r.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.id = "wo"
	r.grant = base.claimFor(r.id, r.subject.Auth().WorkerID)
	dispatcher := member
	if base.dispatcher != nil {
		dispatcher = *base.dispatcher
	}
	if _, err := control.NewLeases(base.store).DispatchWith(t.Context(), scope, r.id, "lane-1", base.overrides, dispatcher); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *laneRig) row() workorders.WorkOrder {
	r.t.Helper()
	var out workorders.WorkOrder
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.WorkOrders().Get(r.id)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *laneRig) outcome() workorders.Change {
	r.t.Helper()
	var last ledger.Event
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, e := range events {
			if e.Subject.ID == r.id {
				last = e
			}
		}
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	var change workorders.Change
	if err := json.Unmarshal(last.Data, &change); err != nil {
		r.t.Fatal(err)
	}
	return change
}

func (r *laneRig) reports(outcome workorders.Outcome) fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		raw, err := json.Marshal(workorders.Report{Outcome: outcome})
		if err != nil {
			r.t.Error(err)
			return
		}
		if _, err := r.journal.SaveReport(context.Background(), spec.Lane, raw); err != nil {
			r.t.Error(err)
		}
	}}
}

func completed(total float64) fake.DriverStep {
	return fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: total}}
}

// noRestarts turns a crash into a lane's end.
const noRestarts = "[lifecycle]\nmax_restarts = 0\n"

func script(turns ...[]fake.DriverStep) fake.DriverScript {
	return fake.DriverScript{SessionID: "session-1", Model: "resolved-model", Turns: turns}
}

func TestReportedLaneStopsAtTheEndOfItsTurn(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Fatalf("row %+v", row)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	spec := specs[0]
	// A medium change runs as the implementation agent, whose class the built-in profile maps to sonnet.
	if spec.Model != "sonnet" || spec.Effort != "medium" || spec.PermissionMode != "auto" || spec.Binary != "/state/bin/ropeshift" {
		t.Errorf("spec %+v", spec)
	}
	if !slices.Equal(spec.Tools, lanes.Tools) {
		t.Errorf("tools %v", spec.Tools)
	}
	if !strings.Contains(spec.Brief, lanes.ToolLaneReport) || !strings.Contains(spec.Brief, r.id) {
		t.Errorf("brief %q", spec.Brief)
	}
	if !strings.Contains(spec.Brief, "- c1: It works") {
		t.Errorf("a lane cannot name a criterion its brief does not: %q", spec.Brief)
	}
	var token string
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			token = value
		}
	}
	if token == "" {
		t.Fatalf("no token in %v", spec.Env)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateEnded || record.TokenDigest != "" {
		t.Errorf("the token outlived its process: %+v", record)
	}
	if _, err := r.journal.LaneByToken(t.Context(), lanes.TokenDigest(token)); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an expired token still finds its lane: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "t", "lane-1")); !os.IsNotExist(err) {
		t.Errorf("the private directory remains: %v", err)
	}
	var result workorders.LaneResult
	if err := json.Unmarshal(r.outcome().Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.CostUSD != 0.25 {
		t.Errorf("result %+v", result)
	}
}

// A lane tells the ledger what its generation spent when it ends (ADR-0037): the cost its turns reported and how many
// turns reported one, so charged spend can fold the generation.
func TestALaneReportsWhatItsGenerationSpent(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.1), r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	var generations []ledger.Generation
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		generations, err = tx.Lanes().Generations("lane-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ledger.GenerationUsage{CostUSD: 0.25, KnownTurns: 2}
	if len(generations) != 1 || generations[0].Usage.KnownTurns != want.KnownTurns || math.Abs(generations[0].Usage.CostUSD-want.CostUSD) > 1e-9 {
		t.Errorf("the ledger holds %+v, want %+v", generations, want)
	}
}

// What a member typed into a lane's web pane says only that they were at that lane's pane, so the worker
// forgets it once the lane ends, and keeps no input for every lane it ever ran.
func TestALanesEndForgetsWhatWasTypedIntoItsPane(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	at := r.clock.Now()
	r.config.PaneInputs.Typed("lane-1", 1, at)
	if !r.config.PaneInputs.TypedSince("lane-1", 1, at) {
		t.Fatal("the input was not kept while the lane had not ended")
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if record, err := r.journal.Lane(t.Context(), "lane-1"); err != nil || record.State != lanes.StateEnded {
		t.Fatalf("the lane did not end: %+v, %v", record, err)
	}
	if r.config.PaneInputs.TypedSince("lane-1", 1, at) {
		t.Error("the worker still holds the input of a lane that ended")
	}
}

// TestALanesEndCarriesTheBranchItsWorkerRead keeps the final artifact out of a session's hands
// (D17): the worker reads the worktree, and a report that names a branch changes nothing.
func TestALanesEndCarriesTheBranchItsWorkerRead(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	claimed := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		raw := []byte(`{"outcome":"completed","branch":"lane/not-this-one","head":"a-session-wrote-this","import_merge":"a-session-wrote-this-too"}`)
		if _, err := r.journal.SaveReport(context.Background(), spec.Lane, raw); err != nil {
			r.t.Error(err)
		}
	}}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, claimed, completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	var result workorders.LaneResult
	if err := json.Unmarshal(r.outcome().Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Branch != "lane/lane-1" || result.Head != fake.LaneHead {
		t.Errorf("the lane's end names %s at %s", result.Branch, result.Head)
	}
	if result.Change != nil {
		t.Errorf("a lane with no base commit measured %+v", result.Change)
	}
	if result.ImportMerge != "" {
		t.Errorf("a lane that imported nothing reports the import %q", result.ImportMerge)
	}
}

// TestALanesEndCarriesWhatItsBranchChanged holds FR-OBS-13: with a lane's result, its worker records the
// files and lines its branch changed since the base its claim named.
func TestALanesEndCarriesWhatItsBranchChanged(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	worktrees := fake.NewWorktrees()
	tip := worktrees.Grow("/repository", "main")
	base.worktrees, base.repository = worktrees, "repo-1"
	base.starts = &control.Starts{Bases: worktrees, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/repository", nil }}
	r := newLaneRigFor(t, base)
	commits := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		worktrees.Touch("/repository", "main", "retry.go")
		worktrees.MoveHead(spec.Dir, worktrees.Touch("/repository", "main", "retry_test.go"))
	}}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, commits, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	var result workorders.LaneResult
	if err := json.Unmarshal(r.outcome().Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Head == tip || result.Change == nil || *result.Change != (ledger.ChangeSize{Files: 2, Added: 2}) {
		t.Errorf("a lane that committed two files from %s ended at %s with %+v", tip, result.Head, result.Change)
	}
}

// A worktree starts at the commit the claim named, which the control plane resolved from the base
// branch, and not at wherever the checkout is (FR-BASE-1).
func TestALanesWorktreeStartsAtTheBaseItsClaimNamed(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	worktrees := fake.NewWorktrees()
	tip := worktrees.Grow("/repository", "main")
	base.worktrees, base.repository = worktrees, "repo-1"
	base.starts = &control.Starts{Bases: worktrees, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/repository", nil }}
	r := newLaneRigFor(t, base)
	// The base moves while the lane is not yet running.
	worktrees.Grow("/repository", "main")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	created := worktrees.Created()
	if len(created) != 1 || created[0].Base != tip || created[0].Repository != "/repository" {
		t.Fatalf("the worker asked for %+v, want a worktree at %s", created, tip)
	}
	var result workorders.LaneResult
	if err := json.Unmarshal(r.outcome().Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Head != tip {
		t.Errorf("the lane ended at %s, want the base %s it began on", result.Head, tip)
	}
	if result.Change != nil {
		t.Errorf("a lane that made no commit measured %+v", result.Change)
	}
}

func TestAnIdleLaneIsNudgedOnce(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Errorf("a nudged lane that reports: %+v", row)
	}
}

func TestALaneThatNeverReportsIsBlocked(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
	))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	change := r.outcome()
	if r.row().State != workorders.Blocked || change.Reason != workorders.ReasonNoOutcome {
		t.Errorf("outcome %+v", change)
	}
}

func TestAFailedTurnIsAFailedLane(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: "error_during_execution"}}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	change := r.outcome()
	if change.Outcome != workorders.OutcomeFailed || change.Reason != "error_during_execution" {
		t.Errorf("outcome %+v", change)
	}
}

func TestACrashBeforeAReportIsAFailedLane(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, noRestarts)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Crash: true}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if change := r.outcome(); change.Reason != workorders.ReasonCrashed {
		t.Errorf("outcome %+v", change)
	}
}

func TestACrashAfterAReportKeepsTheReport(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), {Crash: true}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Errorf("row %+v", row)
	}
}

func TestLaneFailuresBeforeASession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*laneRig)
		reason string
	}{
		{"no checkout of the repository", func(r *laneRig) {
			r.config.Repository = func(context.Context, string, string) (string, error) { return "", errors.New("no source") }
		}, workorders.ReasonNoRepository},
		{"a bad secret reference", func(r *laneRig) {
			r.config.Provisioner.Profiles = badProfile{}
		}, workorders.ReasonSpawnFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLaneRig(t)
			tt.change(r)
			if err := r.subject.Converge(t.Context()); err != nil {
				t.Fatal(err)
			}
			if change := r.outcome(); change.Outcome != workorders.OutcomeFailed || change.Reason != tt.reason {
				t.Errorf("outcome %+v", change)
			}
			if len(r.driver.Specs()) != 0 {
				t.Error("a session started")
			}
		})
	}
}

// A lane that fails before its session says why in its outcome, so the card of its failed work shows the
// cause and not only the reason. The cause is what an error says, which a repository or a terminal wrote, so
// it reaches the ledger as plain text on one line, and cut.
func TestAFailedStartCarriesItsCauseInItsOutcome(t *testing.T) {
	t.Parallel()
	const capped = 500
	cause := "lane/4: no such session: tmux send-keys: command too long\x1b[31m\nsecond line\x1b[0m " + strings.Repeat("x", 3000)
	r := newLaneRig(t)
	r.config.Repository = func(context.Context, string, string) (string, error) { return "", errors.New(cause) }
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	change := r.outcome()
	if change.Reason != workorders.ReasonNoRepository {
		t.Fatalf("outcome %+v", change)
	}
	if !strings.HasPrefix(change.Detail, "lane/4: no such session: tmux send-keys: command too long second line xxx") {
		t.Errorf("the outcome's detail is %q", change.Detail)
	}
	if strings.ContainsAny(change.Detail, "\x1b\n") {
		t.Errorf("the detail kept an escape or a line break: %q", change.Detail)
	}
	if len(change.Detail) > capped || len(change.Detail) < capped-4 {
		t.Errorf("the detail is %d bytes, want it cut at %d", len(change.Detail), capped)
	}
}

// What a failed start prints can carry a credential: a hook's output can hold an npm token, and a git error can
// echo a remote's URL with its password. The cause reaches every member who reads the ledger, so the worker
// redacts it before it cuts it, because a token cut in two no longer matches the shape of one.
func TestAFailedStartRedactsItsCauseBeforeItCutsIt(t *testing.T) {
	t.Parallel()
	const npmToken = "npm_0123456789abcdefghijklmnopqrstuvwxyz"
	tests := []struct {
		name   string
		cause  string
		change func(*laneRig, string)
		reason string
		secret string
		marker string
	}{
		{
			name:  "a hook that printed an npm token",
			cause: "npm error //registry.npmjs.org/:_authToken=" + npmToken + " was refused",
			change: func(r *laneRig, cause string) {
				r.config.Provisioner.Hooks.(*fake.HookRunner).Fail(errors.New(cause))
			},
			reason: workorders.ReasonHookFailed, secret: npmToken, marker: "[redacted: npm_token]",
		},
		{
			name:  "a git error that echoes a remote's password",
			cause: "fatal: unable to access 'https://oauth2:hunter2-secret@github.com/acme/app.git/': the requested URL returned error: 403",
			change: func(r *laneRig, cause string) {
				r.config.Repository = func(context.Context, string, string) (string, error) { return "", errors.New(cause) }
			},
			reason: workorders.ReasonNoRepository, secret: "hunter2-secret", marker: "[redacted: url_credentials]",
		},
		{
			name:  "a token that stands where the cut falls",
			cause: strings.Repeat("y", 480) + " " + npmToken + " tail",
			change: func(r *laneRig, cause string) {
				r.config.Repository = func(context.Context, string, string) (string, error) { return "", errors.New(cause) }
			},
			reason: workorders.ReasonNoRepository, secret: "npm_0123", marker: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var r *laneRig
			if tt.reason == workorders.ReasonHookFailed {
				r = laneRigWith(t, profileAt+"[hooks]\non_lane_created = [\"./scripts/setup.sh\"]\n")
			} else {
				r = newLaneRig(t)
			}
			tt.change(r, tt.cause)
			if err := r.subject.Converge(t.Context()); err != nil {
				t.Fatal(err)
			}
			change := r.outcome()
			if change.Reason != tt.reason {
				t.Fatalf("outcome %+v", change)
			}
			if strings.Contains(change.Detail, tt.secret) {
				t.Errorf("the detail kept the credential: %q", change.Detail)
			}
			if tt.marker != "" && !strings.Contains(change.Detail, tt.marker) {
				t.Errorf("the detail %q does not say a credential stood there", change.Detail)
			}
		})
	}
}

// A report's detail is the worker's word, but the ledger keeps it for every member to read, so the control plane
// redacts it too: a worker that did not, or an older one, leaves no credential in the event or its payload.
func TestALaneEndsWithADetailTheLedgerKeepsRedacted(t *testing.T) {
	t.Parallel()
	const npmToken = "npm_0123456789abcdefghijklmnopqrstuvwxyz"
	r := laneRigWith(t, profileAt)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.start()
	t.Cleanup(r.driver.StopEverySession)
	r.eventuallyUp()
	holder := workorders.Holder{Lease: r.grant.Lease, Lane: "lane-1", Generation: 1}
	raw := "npm error //registry.npmjs.org/:_authToken=" + npmToken + " at https://oauth2:hunter2-secret@github.com/acme/app.git\x1b[31m"
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonSpawnFailed, Detail: raw}}
	if err := control.NewLeases(r.store).EndLane(t.Context(), scope, holder, result, member); err != nil {
		t.Fatal(err)
	}
	change := r.outcome()
	for _, secret := range []string{npmToken, "hunter2-secret", "\x1b"} {
		if strings.Contains(change.Detail, secret) || strings.Contains(string(change.Payload), secret) {
			t.Errorf("the event keeps %q: detail %q payload %s", secret, change.Detail, change.Payload)
		}
	}
	if !strings.Contains(change.Detail, "[redacted: npm_token]") || !strings.Contains(change.Detail, "[redacted: url_credentials]") {
		t.Errorf("the detail %q does not say what it redacted", change.Detail)
	}
}

type badProfile struct{}

func (badProfile) Load(context.Context, string, string, string) (lanes.Profile, error) {
	return lanes.Profile{}, lanes.ErrBadProfile
}

func TestARestartAccountsForALaneAndDoesNotStartItAgain(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	claim, fence := r.grant.ClaimID, r.grant.Fence
	record := lanes.Record{ID: "lane-1", WorkOrder: r.id, ClaimID: claim, Fence: fence, Generation: 1, State: lanes.StateRunning}
	begun := ledger.UploadEvent{
		Epoch: r.subject.Session().Epoch, Type: ledger.UploadLaneStarted, WorkOrder: r.id, ClaimID: claim, Fence: fence,
		Lane: "lane-1", Generation: 1, OccurredAt: 1,
	}
	if _, err := r.journal.BeginLane(t.Context(), record, begun); err != nil {
		t.Fatal(err)
	}
	spawn := lanes.NewEffect(lanes.EffectSession, "lane-1", 1, "", "")
	if _, err := r.journal.BeginEffect(t.Context(), spawn); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.FinishEffect(t.Context(), spawn.Key, lanes.EffectCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 0 {
		t.Error("a restart spawned a lane it had already started")
	}
	if change := r.outcome(); change.Reason != workorders.ReasonWorkerRestarted {
		t.Errorf("outcome %+v", change)
	}
}

// TestALaneStartsWithNoForgeCredential is ADR-0020's guardrail on a lane the worker spawns: a forge token in
// the worker's own environment does not reach it, and gh in it reads an empty configuration directory of
// the lane's own, so the person's sign-in is not there to use.
func TestALaneStartsWithNoForgeCredential(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	parent := map[string]string{"PATH": "/usr/bin", "GH_TOKEN": "gh", "GITHUB_TOKEN": "g", "GH_CONFIG_DIR": "/home/someone/.config/gh"}
	r.config.Provisioner.Lookup = func(name string) (string, bool) {
		value, ok := parent[name]
		return value, ok
	}
	stale := filepath.Join(r.dir, "lanes", "lane-1", "gh")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "hosts.yml"), []byte("github.com:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	var configDir string
	for _, entry := range specs[0].Env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN":
			t.Errorf("%s reached the lane", name)
		case "GH_CONFIG_DIR":
			configDir = value
		}
	}
	if configDir != stale {
		t.Fatalf("GH_CONFIG_DIR is %q, want %q", configDir, stale)
	}
	if entries, err := os.ReadDir(configDir); err != nil || len(entries) != 0 {
		t.Errorf("the lane's gh configuration holds %v: %v", entries, err)
	}
}
