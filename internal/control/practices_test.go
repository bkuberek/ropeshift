package control_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	tipOne = "1111111111111111111111111111111111111111"
	tipTwo = "2222222222222222222222222222222222222222"
)

// practicesRig is the plane rig with the use case that asks workers to run the practices check, over the project
// `pa`, whose workers keep the checkouts the test says.
type practicesRig struct {
	*planeRig
	practices *control.Practices
	store     ports.Store
	hosts     map[string]control.AssistantHost
	local     string
}

func newPracticesRig(t *testing.T, protocol int) *practicesRig {
	t.Helper()
	r := &practicesRig{planeRig: newPlaneRig(t, protocol), hosts: map[string]control.AssistantHost{}, local: "worker-1"}
	r.store = r.stores["pa"]
	r.practices = control.NewPractices(r.store).Placing(
		func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
			return r.hosts, nil
		},
		0,
		func(context.Context) (string, error) { return r.local, nil },
	)
	return r
}

// register records a repository in the project and says each worker keeps a checkout of it.
func (r *practicesRig) register(id, name string, workers ...string) ledger.Repository {
	r.t.Helper()
	record, err := control.NewRepositories(r.store).Detect(r.t.Context(), "pa", control.DetectedRepository{
		ID: id, Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: name,
	}, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, worker := range workers {
		r.keeps(worker, id)
	}
	return record
}

func (r *practicesRig) keeps(worker string, repositories ...string) {
	host := r.hosts[worker]
	if host.Checkouts == nil {
		host.Checkouts = map[string]bool{}
	}
	for _, repository := range repositories {
		host.Checkouts[repository] = true
	}
	r.hosts[worker] = host
}

func (r *practicesRig) request(cause ledger.PracticesCause, repositories ...string) []control.PracticesAsked {
	r.t.Helper()
	asked, err := r.practices.Request(r.t.Context(), "pa", repositories, cause, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	return asked
}

// requested is the one request a call made, which the test requires it to have made.
func (r *practicesRig) requested(asked []control.PracticesAsked) ledger.PracticesRequest {
	r.t.Helper()
	if len(asked) != 1 || asked[0].Request == nil || asked[0].Reason != "" {
		r.t.Fatalf("the call answered %+v, want one request", asked)
	}
	return *asked[0].Request
}

// inserted puts a request into the project as it would stand, whatever its times.
func (r *practicesRig) inserted(request ledger.PracticesRequest) {
	r.t.Helper()
	err := r.store.Write(r.t.Context(), "pa", "test.practices", func(tx ports.WriteTx) error {
		if err := tx.Practices().Insert(request); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.inserted", Actor: projectMember, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request.ID}})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *practicesRig) stored(id string) ledger.PracticesRequest {
	r.t.Helper()
	var request ledger.PracticesRequest
	err := r.store.Read(r.t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		request, err = tx.Practices().Request(id)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return request
}

func (r *practicesRig) record(repository string) (ledger.PracticesRecord, error) {
	var record ledger.PracticesRecord
	err := r.store.Read(r.t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		record, err = tx.Practices().Result(repository)
		return err
	})
	return record, err
}

func (r *practicesRig) scan(repository string) ledger.PracticesScan {
	r.t.Helper()
	var scan ledger.PracticesScan
	err := r.store.Read(r.t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		scan, err = tx.Practices().Scan(repository)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return scan
}

func (r *practicesRig) tip(repository string) string { return r.scan(repository).Tip }

func (r *practicesRig) events(kind string) []ledger.Event {
	r.t.Helper()
	var out []ledger.Event
	err := r.store.Read(r.t.Context(), "pa", func(tx ports.ReadTx) error {
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

func (r *practicesRig) offered(auth ledger.WorkerAuth) []ledger.DesiredPracticesCheck {
	r.t.Helper()
	state, err := r.plane.Desired(r.t.Context(), ledger.DesiredRequest{Auth: auth})
	if err != nil {
		r.t.Fatal(err)
	}
	return state.PracticesChecks
}

func (r *practicesRig) report(request, outcome string, result *practices.Result) error {
	return r.plane.ReportPracticesCheck(r.t.Context(), ledger.PracticesReport{
		Auth: r.auth, Project: "pa", Request: request, Outcome: outcome, Result: result, Detail: map[bool]string{true: "the checkout could not be read"}[outcome == ledger.PracticesOutcomeFailed],
	})
}

func (r *practicesRig) mustReport(request, outcome string, result *practices.Result) {
	r.t.Helper()
	if err := r.report(request, outcome, result); err != nil {
		r.t.Fatal(err)
	}
}

// aResult is what a check of a repository at a commit came to: an env finding, and the secret findings given.
func aResult(repository, commit, from string, leaks ...string) *practices.Result {
	result := practices.Result{
		Repository: repository, Commit: commit, BaseBranch: "main", Worker: "w", TableVersion: practices.TableVersion, RulesVersion: ledger.CredentialRulesVersion,
		ScannedFrom: from, CheckedAt: time.Now().UTC().Truncate(time.Millisecond),
		Practices: []practices.PracticeStatus{{ID: practices.EnvIgnored, Status: practices.StatusFinding}, {ID: practices.Secrets, Status: practices.StatusMet}},
		Findings: []practices.Finding{{
			ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored",
		}},
	}
	for _, digest := range leaks {
		result.Practices[1] = practices.PracticeStatus{ID: practices.Secrets, Status: practices.StatusFinding}
		result.Findings = append(result.Findings, practices.Finding{
			ID: "secrets:github_token:config/dev.yaml:" + digest, Practice: practices.Secrets, Severity: practices.High,
			Rule: "github_token", Path: "config/dev.yaml", Line: 3, Commit: "aaaaaaaaaaaa", Digest: digest + "0000000000",
		})
	}
	return &result
}

// failedScanResult is what a worker's run of the check reports when the secret scan of a checkout at a commit fails.
func failedScanResult(t *testing.T, repository, commit, scannedFrom string) *practices.Result {
	t.Helper()
	reads := &fake.Practices{}
	reads.SetSnapshot("/work/widgets", practices.Snapshot{Commit: commit, Paths: []string{"SECURITY.md"}})
	result, err := practicerun.Run(t.Context(), practicerun.Sources{
		Snapshots: reads, Secrets: failingSecretScan{}, Forge: reads, Clock: fake.NewClock(time.Now().UTC()),
	}, practicerun.Request{Repository: repository, Worker: "w", Dir: "/work/widgets", BaseBranch: "main", ScannedFrom: scannedFrom})
	if err != nil {
		t.Fatal(err)
	}
	return &result
}

// failingSecretScan is a secret scan that cannot run.
type failingSecretScan struct{}

func (failingSecretScan) Scan(context.Context, string, string, string) (ports.SecretScan, error) {
	return ports.SecretScan{}, errors.New("git log: exit status 128")
}

func secretsFindings(result practices.Result) []string {
	var ids []string
	for _, f := range result.Findings {
		if f.Practice == practices.Secrets {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

// A request is offered to the worker it names and to no other, with what the worker needs to find and check the
// repository, and the revision the worker reads moves, so its next heartbeat fetches it.
func TestAPracticesCheckIsOfferedToItsWorkerOnly(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	other := r.secondWorker(ledger.ProtocolVersion)
	repository := r.register("repo-a", "widgets", "worker-1", "worker-2")
	before := r.desired()
	asked := r.request(ledger.PracticesMember, "repo-a")
	request := r.requested(asked)
	if asked[0].Existing || asked[0].Repository.ID != repository.ID {
		t.Errorf("the call answered %+v", asked[0])
	}
	if request.State != ledger.PracticesOffered || request.Worker != "worker-1" || request.Repository != "repo-a" || request.Cause != ledger.PracticesMember ||
		request.RequestedBy != projectMember.ID || request.ExpiresAt-request.RequestedAt != 30*60*1000 {
		t.Fatalf("the request is %+v", request)
	}
	if after := r.desired(); after.Rev == before.Rev {
		t.Errorf("the desired revision stayed at %d, so the worker's next heartbeat would not fetch the request", after.Rev)
	}
	offered := r.offered(r.auth)
	want := ledger.DesiredPracticesCheck{
		ID: request.ID, Project: "pa", Repository: "repo-a", BaseBranch: "main", MergeAuthority: "person_merges", ExpiresAt: request.ExpiresAt,
	}
	if len(offered) != 1 || offered[0] != want {
		t.Errorf("worker-1 is offered %+v, want %+v", offered, want)
	}
	if got := r.offered(other); len(got) != 0 {
		t.Errorf("worker-2 is offered %+v", got)
	}
	events := r.events(ledger.EventPracticesRequested)
	if len(events) != 1 || events[0].Subject != (ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request.ID}) || events[0].Actor != projectMember {
		t.Fatalf("the log holds %+v", events)
	}
	var data map[string]string
	if err := json.Unmarshal(events[0].Data, &data); err != nil || data["repository"] != "repo-a" || data["worker"] != "worker-1" || data["cause"] != "member" || data["request"] != request.ID {
		t.Errorf("the event's data is %v (%v)", data, err)
	}
}

// The check is offered with the repository's own base branch, forge binding, and merge authority, and the commit the
// last completed secret scan read.
func TestAPracticesCheckIsOfferedWithTheRepositorysSettings(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	base, binding, authority := "trunk", "github.com/acme/widgets", ledger.ReviewApproves
	if _, err := control.NewRepositories(r.store).Change(t.Context(), "pa", "repo-a", control.RepositoryChange{Base: &base, ForgeBinding: &binding, MergeAuthority: &authority}, projectMember); err != nil {
		t.Fatal(err)
	}
	err := r.store.Write(t.Context(), "pa", "test.scan", func(tx ports.WriteTx) error {
		if err := tx.Practices().SaveScan("repo-a", ledger.PracticesScan{Tip: tipOne, RulesVersion: ledger.CredentialRulesVersion}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.scanned", Actor: projectMember, Subject: ledger.Subject{Kind: "repository", ID: "repo-a"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	offered := r.offered(r.auth)
	want := ledger.DesiredPracticesCheck{
		ID: request.ID, Project: "pa", Repository: "repo-a", BaseBranch: "trunk", Binding: "github.com/acme/widgets", MergeAuthority: "review_approves",
		ScannedFrom: tipOne, ExpiresAt: request.ExpiresAt,
	}
	if len(offered) != 1 || offered[0] != want {
		t.Errorf("worker-1 is offered %+v, want %+v", offered, want)
	}
}

// A request is offered only until it expires, and not for a repository a member removed.
func TestAPracticesCheckIsOfferedOnlyUntilItExpires(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	r.register("repo-b", "gadgets", "worker-1")
	now := time.Now().UnixMilli()
	r.inserted(ledger.PracticesRequest{ID: "req-live", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now - 1000, ExpiresAt: now + 600_000, State: ledger.PracticesOffered})
	r.inserted(ledger.PracticesRequest{ID: "req-late", Repository: "repo-b", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now - 5000, ExpiresAt: now - 1000, State: ledger.PracticesOffered})
	offered := r.offered(r.auth)
	if len(offered) != 1 || offered[0].ID != "req-live" {
		t.Fatalf("worker-1 is offered %+v, want only the request that has not expired", offered)
	}
	if _, err := control.NewRepositories(r.store).Remove(t.Context(), "pa", "repo-a", nil, projectMember); err != nil {
		t.Fatal(err)
	}
	if offered := r.offered(r.auth); len(offered) != 0 {
		t.Errorf("worker-1 is offered %+v for a repository the project removed", offered)
	}
}

// A worker on a protocol older than the one that reads practices checks is offered none, and a request for the
// repository it keeps is refused with the sentence that says to upgrade it.
func TestAnOlderWorkerIsOfferedNoPracticesCheckAndTheRequestIsRefused(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks-1)
	r.register("repo-a", "widgets", "worker-1")
	asked := r.request(ledger.PracticesMember, "repo-a")
	if len(asked) != 1 || asked[0].Request != nil || asked[0].Reason != "worker w speaks protocol 6; upgrade it to run the practices check" {
		t.Fatalf("the call answered %+v", asked)
	}
	// A request recorded while the worker was on a newer protocol is not offered to the older session either.
	now := time.Now().UnixMilli()
	r.inserted(ledger.PracticesRequest{ID: "req-1", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now, ExpiresAt: now + 600_000, State: ledger.PracticesOffered})
	if offered := r.offered(r.auth); len(offered) != 0 {
		t.Errorf("an older worker is offered %+v", offered)
	}
	if events := r.events(ledger.EventPracticesRequested); len(events) != 0 {
		t.Errorf("a refused request left %d events", len(events))
	}
}

// A repository no connected worker keeps a checkout of gets no request, and the reply says why.
func TestARepositoryNoWorkerKeepsGetsNoRequest(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets")
	asked := r.request(ledger.PracticesMember)
	if len(asked) != 1 || asked[0].Request != nil || asked[0].Reason != "no connected worker keeps a checkout of widgets" {
		t.Fatalf("the call answered %+v", asked)
	}
	r.keeps("worker-9", "repo-a")
	asked = r.request(ledger.PracticesMember)
	if len(asked) != 1 || asked[0].Reason != "no connected worker keeps a checkout of widgets" {
		t.Errorf("a worker the control plane does not know keeps the checkout, and the call answered %+v", asked)
	}
}

// The daemon's own worker is asked first, and another worker when the daemon's keeps no checkout.
func TestTheDaemonsOwnWorkerIsAskedFirst(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.secondWorker(ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1", "worker-2")
	r.register("repo-b", "gadgets", "worker-2")
	asked := r.request(ledger.PracticesMember)
	byName := map[string]string{}
	for _, one := range asked {
		if one.Request == nil {
			t.Fatalf("no request for %+v", one)
		}
		byName[one.Repository.Name] = one.Request.Worker
	}
	if byName["widgets"] != "worker-1" || byName["gadgets"] != "worker-2" {
		t.Errorf("the requests went to %v, want widgets to the daemon's worker-1 and gadgets to worker-2", byName)
	}
	r.local = "worker-2"
	r.register("repo-c", "gizmos", "worker-1", "worker-2")
	if got := r.requested(r.request(ledger.PracticesMember, "repo-c")); got.Worker != "worker-2" {
		t.Errorf("with worker-2 as the daemon's own, the request went to %s", got.Worker)
	}
}

// A worker on an older protocol is passed over for one that reads the request.
func TestAWorkerThatSpeaksTheProtocolIsAskedInsteadOfOneThatDoesNot(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks-1)
	r.secondWorker(ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1", "worker-2")
	if got := r.requested(r.request(ledger.PracticesMember, "repo-a")); got.Worker != "worker-2" {
		t.Errorf("the request went to %s, want the worker that speaks protocol %d", got.Worker, ledger.ProtocolPracticesChecks)
	}
}

// One request per repository is open at a time: a new request for a repository that has one answers the open one, and
// records nothing, until the open one expires.
func TestARepositoryHasOneOpenRequest(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesRegistration, "repo-a"))
	asked := r.request(ledger.PracticesMember, "repo-a")
	if len(asked) != 1 || !asked[0].Existing || asked[0].Request == nil || asked[0].Request.ID != first.ID || asked[0].Request.Cause != ledger.PracticesRegistration {
		t.Fatalf("the second request answered %+v, want the open one", asked)
	}
	if events := r.events(ledger.EventPracticesRequested); len(events) != 1 {
		t.Errorf("the log holds %d requests", len(events))
	}
	// Once the first expired, the next request is a new one.
	err := r.store.Write(t.Context(), "pa", "test.expire", func(tx ports.WriteTx) error {
		if _, err := tx.Practices().Expire(first.ExpiresAt + 1); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.expired", Actor: projectMember, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: first.ID}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if again := r.requested(r.request(ledger.PracticesMember, "repo-a")); again.ID == first.ID {
		t.Error("a request after the first expired answered the expired one")
	}
}

// A repository the control plane stored as offered past its expiry is not open: the next request is a new one.
func TestARequestPastItsExpiryIsNotOpen(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	now := time.Now().UnixMilli()
	r.inserted(ledger.PracticesRequest{ID: "req-old", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now - 5000, ExpiresAt: now - 1000, State: ledger.PracticesOffered})
	if got := r.requested(r.request(ledger.PracticesMember, "repo-a")); got.ID == "req-old" {
		t.Errorf("the request answered the one past its expiry")
	}
}

// A request names repositories by ID or by name, and a name nobody holds is not found.
func TestARequestNamesItsRepositories(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	r.register("repo-b", "gadgets", "worker-1")
	if asked := r.request(ledger.PracticesMember, "widgets", "repo-b", "widgets"); len(asked) != 2 || asked[0].Repository.ID != "repo-a" || asked[1].Repository.ID != "repo-b" {
		t.Errorf("the call answered %+v, want widgets and gadgets once each, in the order named", asked)
	}
	if _, err := r.practices.Request(t.Context(), "pa", []string{"nothing"}, ledger.PracticesMember, projectMember); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a repository nobody holds answered %v, want not found", err)
	}
	if _, err := r.practices.Request(t.Context(), "pa", nil, "weekly", projectMember); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("a cause that is none answered %v, want invalid", err)
	}
}

// The first report of a request is recorded and a later one changes nothing, however it differs.
func TestTheFirstPracticesReportIsRecordedOnce(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(request.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, ""))
	revision := r.desired().Rev
	r.mustReport(request.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipTwo, ""))
	r.mustReport(request.ID, ledger.PracticesOutcomeFailed, nil)
	stored := r.stored(request.ID)
	if stored.State != ledger.PracticesReported || stored.ReportedAt == 0 {
		t.Errorf("the request is %+v", stored)
	}
	record, err := r.record("repo-a")
	if err != nil || record.Commit != tipOne || record.Request != request.ID {
		t.Errorf("the result is %+v (%v), want the first report's", record, err)
	}
	if events := r.events(ledger.EventPracticesChecked); len(events) != 1 {
		t.Errorf("the log holds %d checked events, want one", len(events))
	}
	if events := r.events(ledger.EventPracticesFailed); len(events) != 0 {
		t.Errorf("a later report left %d failed events", len(events))
	}
	if got := r.desired().Rev; got != revision {
		t.Errorf("a later report moved the revision from %d to %d", revision, got)
	}
	if offered := r.offered(r.auth); len(offered) != 0 {
		t.Errorf("a reported request is still offered: %+v", offered)
	}
}

// A checked report stores the result, moves the scan tip to the commit it read, and appends the whole result.
func TestACheckedReportStoresTheResultAndAdvancesTheScanTip(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if tip := r.tip("repo-a"); tip != "" {
		t.Fatalf("a repository never checked has the tip %q", tip)
	}
	result := aResult("repo-a", tipOne, "", "1a2b3c4d5e6f")
	r.mustReport(request.ID, ledger.PracticesOutcomeChecked, result)
	record, err := r.record("repo-a")
	if err != nil || record.Commit != tipOne {
		t.Fatalf("the result is %+v (%v)", record, err)
	}
	got, _ := json.Marshal(record.Result)
	want, _ := json.Marshal(result)
	if string(got) != string(want) {
		t.Errorf("the stored result is %s, want %s", got, want)
	}
	if tip := r.tip("repo-a"); tip != tipOne {
		t.Errorf("the scan tip is %q, want %s", tip, tipOne)
	}
	events := r.events(ledger.EventPracticesChecked)
	if len(events) != 1 || events[0].Actor != (ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-1"}) ||
		events[0].Subject != (ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request.ID}) {
		t.Fatalf("the log holds %+v", events)
	}
	var logged practices.Result
	if err := json.Unmarshal(events[0].Data, &logged); err != nil || string(events[0].Data) != string(want) {
		t.Errorf("the event holds %s (%v), want the whole result", events[0].Data, err)
	}
	// The next request starts its scan where this one ended.
	next := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if offered := r.offered(r.auth); len(offered) != 1 || offered[0].ID != next.ID || offered[0].ScannedFrom != tipOne {
		t.Errorf("the next check is offered %+v, want a scan from %s", offered, tipOne)
	}
}

// A failed report records why, keeps the previous result and the scan tip, and settles the request.
func TestAFailedReportKeepsThePreviousResult(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(first.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, ""))
	second := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(second.ID, ledger.PracticesOutcomeFailed, nil)
	record, err := r.record("repo-a")
	if err != nil || record.Commit != tipOne || record.Request != first.ID {
		t.Errorf("the result is %+v (%v), want the first report's", record, err)
	}
	if tip := r.tip("repo-a"); tip != tipOne {
		t.Errorf("the scan tip is %q, want %s", tip, tipOne)
	}
	if stored := r.stored(second.ID); stored.State != ledger.PracticesReported {
		t.Errorf("the failed request is %s", stored.State)
	}
	events := r.events(ledger.EventPracticesFailed)
	if len(events) != 1 {
		t.Fatalf("the log holds %d failed events, want one", len(events))
	}
	var data map[string]string
	if err := json.Unmarshal(events[0].Data, &data); err != nil || data["detail"] != "the checkout could not be read" || data["repository"] != "repo-a" || data["request"] != second.ID {
		t.Errorf("the event's data is %v (%v)", data, err)
	}
}

// A report that comes after the request expired is still recorded.
func TestAReportAfterExpiryIsStillRecorded(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	now := time.Now().UnixMilli()
	r.inserted(ledger.PracticesRequest{ID: "req-late", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now - 5000, ExpiresAt: now - 1000, State: ledger.PracticesOffered})
	if _, err := r.practices.Expire(t.Context(), "pa", projectMember); err != nil {
		t.Fatal(err)
	}
	if stored := r.stored("req-late"); stored.State != ledger.PracticesExpired {
		t.Fatalf("the request is %s, want expired", stored.State)
	}
	r.mustReport("req-late", ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, ""))
	if stored := r.stored("req-late"); stored.State != ledger.PracticesReported {
		t.Errorf("the request is %s after its late report", stored.State)
	}
	if record, err := r.record("repo-a"); err != nil || record.Commit != tipOne {
		t.Errorf("the late result is %+v (%v)", record, err)
	}
}

// A late report of an earlier request never replaces the result of a request made after it.
func TestAnEarlierRequestsLateReportDoesNotReplaceANewerResult(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	now := time.Now().UnixMilli()
	r.inserted(ledger.PracticesRequest{ID: "req-old", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now - 5000, ExpiresAt: now - 1000, State: ledger.PracticesOffered})
	newer := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(newer.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipTwo, ""))
	r.mustReport("req-old", ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, ""))
	if stored := r.stored("req-old"); stored.State != ledger.PracticesReported {
		t.Errorf("the earlier request is %s, want its report recorded", stored.State)
	}
	if record, err := r.record("repo-a"); err != nil || record.Commit != tipTwo || record.Request != newer.ID {
		t.Errorf("the result is %+v (%v), want the newer request's", record, err)
	}
	if tip := r.tip("repo-a"); tip != tipTwo {
		t.Errorf("the scan tip is %q, want %s", tip, tipTwo)
	}
	if events := r.events(ledger.EventPracticesChecked); len(events) != 2 {
		t.Errorf("the log holds %d checked events, want both reports", len(events))
	}
}

// An incremental scan's result keeps the secrets of the result it continued from, and a scan that could not run
// clears the tip, so the next one reads the whole history again.
func TestAnIncrementalScanKeepsTheEarlierSecretsAndAFailedScanRestartsIt(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(first.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, "", "1a2b3c4d5e6f"))
	second := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(second.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipTwo, tipOne))
	record, err := r.record("repo-a")
	if err != nil || !slices.Equal(secretsFindings(record.Result), []string{"secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"}) {
		t.Fatalf("the result is %+v (%v), want the earlier secret kept", record, err)
	}
	if events := r.events(ledger.EventPracticesChecked); len(events) != 2 || len(secretsFindings(unmarshalResult(t, events[1].Data))) != 1 {
		t.Errorf("the second event does not hold the result as it was kept")
	}
	third := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	// The result a worker sends when the scan fails: the run's own, which leaves where the scan started empty.
	unknown := failedScanResult(t, "repo-a", "3333333333333333333333333333333333333333", tipTwo)
	if unknown.ScannedFrom != "" || secretsStatus(t, *unknown).Status != practices.StatusUnknown {
		t.Fatalf("the run of a failed scan gave %+v", unknown)
	}
	r.mustReport(third.ID, ledger.PracticesOutcomeChecked, unknown)
	record, err = r.record("repo-a")
	if err != nil || record.Commit != unknown.Commit || secretsStatus(t, record.Result).Status != practices.StatusFinding ||
		!slices.Equal(secretsFindings(record.Result), []string{"secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"}) {
		t.Errorf("the result is %+v (%v), want the earlier secret kept as a finding though the scan could not run", record, err)
	}
	if reason := secretsStatus(t, record.Result).Reason; !strings.HasPrefix(reason, "the secret scan failed") {
		t.Errorf("the finding's reason is %q, want it to say the scan failed", reason)
	}
	if tip := r.tip("repo-a"); tip != "" {
		t.Errorf("a scan that could not run left the tip %q, want it cleared", tip)
	}
	fourth := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if offered := r.offered(r.auth); len(offered) != 1 || offered[0].ID != fourth.ID || offered[0].ScannedFrom != "" {
		t.Errorf("the next check is offered %+v, want a scan of the whole history", offered)
	}
}

// A scan tip a scan with another credential rule set left is not handed to the next check, which reads the whole
// history again: commits it already read were never matched against the rules in force. The version of the rule set
// is kept with the tip, and a tip of this build's rule set starts the next scan where it ended.
func TestAScanTipOfAnotherRuleSetIsNotHandedToTheNextCheck(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	older := aResult("repo-a", tipOne, "")
	older.RulesVersion = ledger.CredentialRulesVersion + 1
	r.mustReport(first.ID, ledger.PracticesOutcomeChecked, older)
	if scan := r.scan("repo-a"); scan.Tip != tipOne || scan.RulesVersion != older.RulesVersion {
		t.Fatalf("the scan is %+v, want the tip %s with the version %d", scan, tipOne, older.RulesVersion)
	}
	second := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if offered := r.offered(r.auth); len(offered) != 1 || offered[0].ID != second.ID || offered[0].ScannedFrom != "" {
		t.Fatalf("the check is offered %+v, want a scan of the whole history", offered)
	}
	r.mustReport(second.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipTwo, ""))
	if scan := r.scan("repo-a"); scan.Tip != tipTwo || scan.RulesVersion != ledger.CredentialRulesVersion {
		t.Fatalf("the scan is %+v, want the tip %s with this build's version", scan, tipTwo)
	}
	third := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if offered := r.offered(r.auth); len(offered) != 1 || offered[0].ID != third.ID || offered[0].ScannedFrom != tipTwo {
		t.Errorf("the check is offered %+v, want a scan from %s", offered, tipTwo)
	}
}

// A request that fails leaves the registration it follows as it was, and says so on the registration path's log, once.
// A request that is refused with a reason, as every request is when no worker keeps the checkout, is no failure.
func TestAFailedPracticesRequestLeavesTheRegistrationAndIsLogged(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	hosts := func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return nil, errors.New("the workers' checkouts cannot be read")
	}
	var logged []string
	repositories := control.NewRepositories(r.store).
		Practicing(control.NewPractices(r.store).Placing(hosts, 0, nil)).
		Warning(func(line string) { logged = append(logged, line) })
	kept := fake.NewCheckouts()
	kept.Place(ports.Clone{Path: "/work/widgets", ID: "repo-widgets", Remote: "git@github.com:acme/widgets.git", DefaultBranch: "main", Directory: "widgets"})
	added, err := repositories.Add(t.Context(), "pa", control.RepositoryAdd{Paths: []string{"/work/widgets"}}, kept, projectMember)
	if err != nil || len(added) != 1 || added[0].Outcome != ledger.Registered {
		t.Fatalf("registering the clone read %+v (%v), want it registered", added, err)
	}
	var record ledger.Repository
	err = r.store.Read(t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		record, err = tx.Repositories().Repository("repo-widgets")
		return err
	})
	if err != nil || record.Removed() || record.Name != "widgets" {
		t.Errorf("the registered repository reads %+v (%v)", record, err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "repo-widgets") || !strings.Contains(logged[0], "the workers' checkouts cannot be read") {
		t.Errorf("the log holds %q, want one line naming the repository and what failed", logged)
	}
	if events := r.events(ledger.EventPracticesRequested); len(events) != 0 {
		t.Errorf("a failed request left %d requests", len(events))
	}

	// With no worker keeping the checkout the request is refused with a reason, which is no failure and logs nothing.
	logged = nil
	quiet := control.NewRepositories(r.store).
		Practicing(control.NewPractices(r.store).Placing(func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) { return nil, nil }, 0, nil)).
		Warning(func(line string) { logged = append(logged, line) })
	kept.Place(ports.Clone{Path: "/work/gadgets", ID: "repo-gadgets", Remote: "git@github.com:acme/gadgets.git", DefaultBranch: "main", Directory: "gadgets"})
	if added, err := quiet.Add(t.Context(), "pa", control.RepositoryAdd{Paths: []string{"/work/gadgets"}}, kept, projectMember); err != nil || len(added) != 1 || added[0].Outcome != ledger.Registered {
		t.Fatalf("registering the second clone read %+v (%v)", added, err)
	}
	if len(logged) != 0 {
		t.Errorf("a refusal with a reason logged %q", logged)
	}
}

const tipThree = "3333333333333333333333333333333333333333"

func secretsStatus(t *testing.T, result practices.Result) practices.PracticeStatus {
	t.Helper()
	for _, status := range result.Practices {
		if status.ID == practices.Secrets {
			return status
		}
	}
	t.Fatal("the result has no status for secrets")
	return practices.PracticeStatus{}
}

// A result of a scan that continued from a commit other than the one the kept result read cannot say its history holds
// no secret, and must not read as met: the history between the two was read by no scan. A late report of an expired
// request that was kept in between is how it happens. The result keeps the secrets of the kept result, which read as a
// finding, the scan tip is cleared, and the next check reads the whole history.
func TestAnIncrementalScanThatCannotContinueFromTheKeptResultKeepsTheKeptSecrets(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(first.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, "", "1a2b3c4d5e6f"))
	// Request A was made after the first, offered a scan from tipOne, and expired with no report.
	r.inserted(ledger.PracticesRequest{
		ID: "req-a", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m",
		RequestedAt: first.RequestedAt + 1, ExpiresAt: first.RequestedAt + 2, State: ledger.PracticesOffered,
	})
	// B is offered a scan from tipOne too, while A's report is still to come.
	b := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if offered := r.offered(r.auth); len(offered) != 1 || offered[0].ID != b.ID || offered[0].ScannedFrom != tipOne {
		t.Fatalf("the check is offered %+v, want B, scanning from %s", offered, tipOne)
	}
	// A's late report, from tipOne to tipTwo, is kept, with the secret it continued from.
	r.mustReport("req-a", ledger.PracticesOutcomeChecked, aResult("repo-a", tipTwo, tipOne))
	if record, err := r.record("repo-a"); err != nil || record.Commit != tipTwo || len(secretsFindings(record.Result)) != 1 {
		t.Fatalf("the result is %+v (%v), want A's, with the secret kept", record, err)
	}
	// B's report, from tipOne to tipThree, continues from a commit the kept result did not read.
	r.mustReport(b.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipThree, tipOne))
	record, err := r.record("repo-a")
	if err != nil || record.Commit != tipThree || record.Request != b.ID {
		t.Fatalf("the result is %+v (%v), want B's", record, err)
	}
	if status := secretsStatus(t, record.Result); status.Status != practices.StatusFinding {
		t.Errorf("secrets are %+v, want a finding: the secret the kept result found stays found", status)
	}
	if got := secretsFindings(record.Result); !slices.Equal(got, []string{"secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"}) {
		t.Errorf("the secrets findings are %v, want the kept result's", got)
	}
	if tip := r.tip("repo-a"); tip != "" {
		t.Errorf("the scan tip is %q, want it cleared", tip)
	}
	next := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	if offered := r.offered(r.auth); len(offered) != 1 || offered[0].ID != next.ID || offered[0].ScannedFrom != "" {
		t.Errorf("the next check is offered %+v, want a scan of the whole history", offered)
	}
}

// A scan that cannot continue from a kept result that holds no secret reads its secrets unknown, with the reason.
func TestAnIncrementalScanThatCannotContinueFromACleanKeptResultReadsSecretsUnknown(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(first.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipOne, ""))
	second := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	r.mustReport(second.ID, ledger.PracticesOutcomeChecked, aResult("repo-a", tipThree, tipTwo))
	record, err := r.record("repo-a")
	if err != nil || record.Commit != tipThree {
		t.Fatalf("the result is %+v (%v)", record, err)
	}
	if status := secretsStatus(t, record.Result); status.Status != practices.StatusUnknown || status.Reason == "" {
		t.Errorf("secrets are %+v, want unknown with a reason", status)
	}
	if got := secretsFindings(record.Result); len(got) != 0 {
		t.Errorf("the secrets findings are %v, want none", got)
	}
}

// The same holds when the scan the kept result ended with matched with another version of the credential rules than
// this build's: the kept secrets stay found, the next check reads the whole history, and a secret the new scan finds is
// found beside them.
func TestAnIncrementalScanOverAnotherRuleSetKeepsTheKeptSecrets(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	first := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	older := aResult("repo-a", tipOne, "", "1a2b3c4d5e6f")
	older.RulesVersion = ledger.CredentialRulesVersion + 1
	r.mustReport(first.ID, ledger.PracticesOutcomeChecked, older)
	second := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	// A worker whose desired state was read before the first report continues from tipOne all the same.
	continued := aResult("repo-a", tipTwo, tipOne)
	continued.RulesVersion = older.RulesVersion
	r.mustReport(second.ID, ledger.PracticesOutcomeChecked, continued)
	record, err := r.record("repo-a")
	if err != nil || record.Commit != tipTwo {
		t.Fatalf("the result is %+v (%v)", record, err)
	}
	if status := secretsStatus(t, record.Result); status.Status != practices.StatusFinding || len(secretsFindings(record.Result)) != 1 {
		t.Errorf("secrets are %+v with %v, want the kept secret as a finding", status, secretsFindings(record.Result))
	}
	if tip := r.tip("repo-a"); tip != "" {
		t.Errorf("the scan tip is %q, want it cleared", tip)
	}
	third := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	found := aResult("repo-a", tipThree, tipTwo, "ffeeddccbbaa")
	found.RulesVersion = older.RulesVersion
	r.mustReport(third.ID, ledger.PracticesOutcomeChecked, found)
	if record, err := r.record("repo-a"); err != nil || secretsStatus(t, record.Result).Status != practices.StatusFinding || len(secretsFindings(record.Result)) != 2 {
		t.Errorf("a secret the new scan found reads %+v (%v), want it found beside the kept one", record, err)
	}
}

// The control plane bounds what a worker reports and does not trust the worker to have: each reason and detail is one
// line of at most 2000 bytes on a rune boundary, every other text is cut there, and the secret findings are capped at
// 200 with the result saying it left some out. The result is kept and appended as bounded.
func TestAReportedResultIsBoundedBeforeItIsKept(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	long := strings.Repeat("\u20ac", 3000)
	result := aResult("repo-a", tipOne, "")
	result.Practices[0].Reason = "first line\nsecond line " + long
	result.Findings[0].Detail = long
	for i := range 250 {
		result.Findings = append(result.Findings, practices.Finding{
			ID: fmt.Sprintf("secrets:github_token:config/%04d.yaml:abcdefabcdef", i), Practice: practices.Secrets, Severity: practices.High,
			Rule: "github_token", Path: "config/" + fmt.Sprintf("%04d", i) + ".yaml", Line: 1, Commit: "aaaaaaaaaaaa", Digest: "abcdefabcdef0000000000",
		})
	}
	r.mustReport(request.ID, ledger.PracticesOutcomeChecked, result)
	record, err := r.record("repo-a")
	if err != nil {
		t.Fatal(err)
	}
	got := record.Result
	if reason := got.Practices[0].Reason; len(reason) > 2000 || len(reason) < 1990 || strings.ContainsAny(reason, "\r\n") || !strings.HasPrefix(reason, "first line second line ") || !utf8.ValidString(reason) {
		t.Errorf("the reason is %d bytes: %.40q", len(reason), reason)
	}
	if detail := got.Findings[0].Detail; len(detail) > 2000 || !utf8.ValidString(detail) {
		t.Errorf("the detail is %d bytes", len(detail))
	}
	if n := len(secretsFindings(got)); n != practices.MaxSecretFindings || !got.Truncated {
		t.Errorf("%d secret findings, truncated %v; want %d, true", n, got.Truncated, practices.MaxSecretFindings)
	}
	events := r.events(ledger.EventPracticesChecked)
	if len(events) != 1 || len(events[0].Data) > 1<<20 || !strings.Contains(string(events[0].Data), `"truncated":true`) {
		t.Errorf("the event holds %d bytes, want the bounded result", len(events[0].Data))
	}
}

// A result no worker makes is refused as invalid, and leaves the request offered.
func TestAResultWithAPracticeNoTableHoldsIsRefused(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	result := aResult("repo-a", tipOne, "")
	result.Practices[0].ID = "tabs"
	if err := r.report(request.ID, ledger.PracticesOutcomeChecked, result); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("the report answered %v, want invalid", err)
	}
	if stored := r.stored(request.ID); stored.State != ledger.PracticesOffered {
		t.Errorf("a refused report settled the request: %s", stored.State)
	}
}

func unmarshalResult(t *testing.T, raw []byte) practices.Result {
	t.Helper()
	var result practices.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// Only the worker a request names may report it, and a report is refused when it does not say what it did.
func TestAPracticesReportIsRefusedFromAnotherWorkerOrWhenItIsMalformed(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	other := r.secondWorker(ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	send := func(auth ledger.WorkerAuth, report ledger.PracticesReport) error {
		report.Auth, report.Project = auth, "pa"
		return r.plane.ReportPracticesCheck(t.Context(), report)
	}
	good := aResult("repo-a", tipOne, "")
	if err := send(other, ledger.PracticesReport{Request: request.ID, Outcome: ledger.PracticesOutcomeChecked, Result: good}); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("another worker's report answered %v, want not found", err)
	}
	if err := send(ledger.WorkerAuth{WorkerID: "worker-1", Credential: "wrong"}, ledger.PracticesReport{Request: request.ID, Outcome: ledger.PracticesOutcomeChecked, Result: good}); !errors.Is(err, ledger.ErrBadCredential) {
		t.Errorf("a report with a wrong credential answered %v", err)
	}
	if err := send(r.auth, ledger.PracticesReport{Request: "missing", Outcome: ledger.PracticesOutcomeChecked, Result: good}); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a report of no request answered %v, want not found", err)
	}
	wrongRepository := aResult("repo-b", tipOne, "")
	for name, report := range map[string]ledger.PracticesReport{
		"no request":                     {Outcome: ledger.PracticesOutcomeChecked, Result: good},
		"an outcome that is neither":     {Request: request.ID, Outcome: "done", Result: good},
		"checked with no result":         {Request: request.ID, Outcome: ledger.PracticesOutcomeChecked},
		"checked with no commit":         {Request: request.ID, Outcome: ledger.PracticesOutcomeChecked, Result: &practices.Result{Repository: "repo-a"}},
		"a result of another repository": {Request: request.ID, Outcome: ledger.PracticesOutcomeChecked, Result: wrongRepository},
		"failed with no detail":          {Request: request.ID, Outcome: ledger.PracticesOutcomeFailed},
	} {
		if err := send(r.auth, report); !errors.Is(err, workorders.ErrInvalid) {
			t.Errorf("%s answered %v, want invalid", name, err)
		}
	}
	if stored := r.stored(request.ID); stored.State != ledger.PracticesOffered {
		t.Errorf("a refused report settled the request: %s", stored.State)
	}
	if err := send(r.auth, ledger.PracticesReport{Request: request.ID, Outcome: ledger.PracticesOutcomeChecked, Result: good}); err != nil {
		t.Errorf("the worker's own report answered %v", err)
	}
}

// A failure's detail is bounded however long the worker sent it.
func TestAFailureDetailIsBounded(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	request := r.requested(r.request(ledger.PracticesMember, "repo-a"))
	err := r.plane.ReportPracticesCheck(t.Context(), ledger.PracticesReport{
		Auth: r.auth, Project: "pa", Request: request.ID, Outcome: ledger.PracticesOutcomeFailed, Detail: strings.Repeat("€", 3000),
	})
	if err != nil {
		t.Fatal(err)
	}
	events := r.events(ledger.EventPracticesFailed)
	var data map[string]string
	if len(events) != 1 || json.Unmarshal(events[0].Data, &data) != nil || len(data["detail"]) > 2000 || len(data["detail"]) < 1990 {
		t.Errorf("the event's detail is %d bytes, want it cut near 2000", len(data["detail"]))
	}
}

// A request nobody reported by its expiry is stored as expired on the next pass of the tick, once, and a request a
// worker reported or that has time left is left alone.
func TestARequestNobodyReportedExpires(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets", "worker-1")
	r.register("repo-b", "gadgets", "worker-1")
	now := time.Now().UnixMilli()
	r.inserted(ledger.PracticesRequest{ID: "req-old", Repository: "repo-a", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "m", RequestedAt: now - 5000, ExpiresAt: now - 1000, State: ledger.PracticesOffered})
	live := r.requested(r.request(ledger.PracticesMember, "repo-b"))
	ids, err := r.practices.Expire(t.Context(), "pa", projectMember)
	if err != nil || !slices.Equal(ids, []string{"req-old"}) {
		t.Fatalf("the tick expired %v (%v), want req-old", ids, err)
	}
	if again, err := r.practices.Expire(t.Context(), "pa", projectMember); err != nil || len(again) != 0 {
		t.Errorf("a second pass expired %v (%v)", again, err)
	}
	if stored := r.stored("req-old"); stored.State != ledger.PracticesExpired {
		t.Errorf("req-old is %s", stored.State)
	}
	if stored := r.stored(live.ID); stored.State != ledger.PracticesOffered {
		t.Errorf("the live request is %s", stored.State)
	}
	if events := r.events(ledger.EventPracticesExpired); len(events) != 1 || events[0].Subject.ID != "req-old" {
		t.Errorf("the log holds %+v, want one expiry", events)
	}
}
