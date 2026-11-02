package worker_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// No agent may ask for an agent. The first passphrase needs no session, so an orchestrator that
// could start without a member would be a process able to take the workspace.
func TestOnlyAMemberStartsAnOrchestrator(t *testing.T) {
	r := newLaneRig(t)
	worker := r.subject.Auth().WorkerID
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		_, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, ledger.Actor{Kind: kind, ID: "x"})
		if !errors.Is(err, workorders.ErrForbidden) {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if _, err := r.plane.Orchestrator(t.Context(), worker); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a refusal left a row: %v", err)
	}
}

func TestAnOrchestratorHasNoOtherState(t *testing.T) {
	r := newLaneRig(t)
	_, err := r.plane.SetOrchestrator(t.Context(), r.subject.Auth().WorkerID, "Running", member)
	if !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("err %v", err)
	}
}

func TestANameThatIsNoWorkerStartsNothing(t *testing.T) {
	r := newLaneRig(t)
	_, err := r.plane.SetOrchestrator(t.Context(), "nobody", ledger.OrchestratorRunning, member)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v", err)
	}
}

// One per worker: a second start names the same one, and writes nothing.
func TestOneOrchestratorPerWorker(t *testing.T) {
	r := newLaneRig(t)
	worker := r.subject.Auth().WorkerID
	first, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	before := r.orchestratorEvents(t)
	second, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || second.ID != ledger.OrchestratorOf(worker) || second.Run != 1 {
		t.Errorf("first %+v, second %+v", first, second)
	}
	if after := r.orchestratorEvents(t); len(after) != len(before) {
		t.Errorf("%d events for nothing new", len(after)-len(before))
	}
}

// A journal keeps a lane that ended, so a start after a stop has to be a lane of its own.
func TestAStartAfterAStopIsANewRun(t *testing.T) {
	r := newLaneRig(t)
	worker := r.subject.Auth().WorkerID
	first, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorStopped, member); err != nil {
		t.Fatal(err)
	}
	second, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	if first.Lane() == second.Lane() {
		t.Errorf("both starts are %s", first.Lane())
	}
}

func (r *laneRig) orchestratorUpload(t *testing.T, events ...ledger.UploadEvent) []ledger.UploadResult {
	t.Helper()
	for i := range events {
		events[i].Epoch, events[i].Seq = r.subject.Session().Epoch, int64(9000+i)
	}
	reply, err := r.plane.Upload(t.Context(), ledger.UploadBatch{Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	return reply.Rejected
}

func status(t *testing.T, state string) []byte {
	t.Helper()
	raw, err := json.Marshal(ledger.LaneStatus{ActualState: state})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// An orchestrator's name is reserved, or a member's lane could read as one, and a run no member
// asked for could speak for the worker that holds it.
func TestNoLaneTakesAnOrchestratorsName(t *testing.T) {
	r := newLaneRig(t)
	r.claimFor("wo-2", r.subject.Auth().WorkerID)
	_, err := control.NewLeases(r.store).Dispatch(t.Context(), scope, "wo-2", ledger.Orchestrator{Run: 1}.Lane(), member)
	if !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("err %v", err)
	}
}

// A socket's path has a limit, and a lane's name is part of it, so a name with a worker's ID in it
// passes a fake and stops a real session.
func TestAnOrchestratorsLaneNameStaysShort(t *testing.T) {
	row := ledger.Orchestrator{ID: ledger.OrchestratorOf("44f226018ebb89cc2fc7c5d1a70a82e3"), Run: 1_000_000}
	if got := row.Lane(); len(got) > 24 {
		t.Errorf("%q is %d bytes", got, len(got))
	}
	for _, lane := range []string{"orchestrator-0", "orchestrator-1000001", "orchestrator-x", "orchestrator-", "lane-1"} {
		if row.Belongs(lane) {
			t.Errorf("%q reads as a start of this orchestrator", lane)
		}
	}
	if !row.Belongs("orchestrator-1") || !row.Belongs(row.Lane()) {
		t.Error("a start this orchestrator made reads as another's")
	}
}

// A worker speaks only for its own orchestrator, and a word about a start this one replaced is
// kept and moves nothing.
func TestAWorkerSpeaksForItsOwnOrchestrator(t *testing.T) {
	r := newLaneRig(t)
	worker := r.subject.Auth().WorkerID
	if _, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member); err != nil {
		t.Fatal(err)
	}
	if _, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorStopped, member); err != nil {
		t.Fatal(err)
	}
	row, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	stale := row
	stale.Run--
	rejected := r.orchestratorUpload(t,
		ledger.UploadEvent{Type: ledger.UploadLaneStatus, Lane: ledger.Orchestrator{Run: row.Run + 1}.Lane(), Payload: status(t, "idle")},
		ledger.UploadEvent{Type: ledger.UploadLaneStatus, Lane: row.Lane(), Generation: 3, Payload: status(t, "working")},
		ledger.UploadEvent{Type: ledger.UploadLaneStatus, Lane: stale.Lane(), Generation: 9, Payload: status(t, "tripped")},
	)
	if len(rejected) != 1 || rejected[0].Status != ledger.UploadInvalid || rejected[0].Seq != 9000 {
		t.Fatalf("rejected %+v", rejected)
	}
	got, err := r.plane.Orchestrator(t.Context(), worker)
	if err != nil {
		t.Fatal(err)
	}
	if got.Actual != "working" || got.Generation != 3 {
		t.Errorf("row %+v", got)
	}
}

func (r *laneRig) orchestratorEvents(t *testing.T) []ledger.Event {
	t.Helper()
	var out []ledger.Event
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if event.Subject.Kind == ledger.SubjectOrchestrator {
				out = append(out, event)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every change has an event, so the projection is what its events say.
func TestReplayHoldsAnOrchestrator(t *testing.T) {
	r := newLaneRig(t)
	worker := r.subject.Auth().WorkerID
	row, err := r.plane.SetOrchestrator(t.Context(), worker, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	r.orchestratorUpload(t,
		ledger.UploadEvent{Type: ledger.UploadLaneStatus, Lane: row.Lane(), Generation: 1, Payload: status(t, "working")},
		ledger.UploadEvent{Type: ledger.UploadGenerationStarted, Lane: row.Lane(), Generation: 2},
		ledger.UploadEvent{Type: ledger.UploadLaneEnded, Lane: row.Lane(), Generation: 2},
	)
	want, err := r.plane.Orchestrator(t.Context(), worker)
	if err != nil {
		t.Fatal(err)
	}
	if want.Actual != "ended" || want.Generation != 2 {
		t.Fatalf("row %+v", want)
	}
	replayed, err := ledger.ReplayOrchestrators(r.orchestratorEvents(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := replayed[want.ID]; got != want {
		t.Errorf("replay %+v, projection %+v", got, want)
	}
}

func (r *laneRig) digest(t *testing.T) []ledger.WakeItem {
	t.Helper()
	found, err := r.plane.WakeDigest(t.Context(), ledger.WakeRequest{Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch})
	if err != nil {
		t.Fatal(err)
	}
	return found.Items
}

func kindsOf(items []ledger.WakeItem) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Kind+":"+item.ID)
	}
	slices.Sort(out)
	return out
}

// A digest with nothing in it is what keeps an idle fleet from spending anything.
func TestAnIdleWorkspaceHasAnEmptyDigest(t *testing.T) {
	r := newLaneRig(t)
	if got := r.digest(t); len(got) != 0 {
		t.Errorf("digest %+v", got)
	}
}

// What a person must answer and what another holds are nothing to judge here.
func TestADigestHoldsOnlyWhatIsFree(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.asked(t, "free", "Which retry budget?")
	o.asked(t, "held", "Which log level?")
	ops := control.NewQuestions(o.store, questions.BatchPolicy{})
	if _, err := ops.Take(t.Context(), scope, "held", 60_000, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "other"}); err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(o.digest(t)); !slices.Equal(got, []string{"question:free"}) {
		t.Errorf("digest %v", got)
	}
}

// A request reaches an orchestrator only if a member delegated its class, and then with the digest a
// decision binds to, which is how it reads one. Its own go to a member.
func TestADigestHoldsOnlyWhatWasDelegated(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	policy := authority.DelegationPolicy{Version: 2, Classes: []authority.DelegationClass{{Tool: "Bash", CommandPrefix: "git tag"}}}
	body := "[delegation_policy]\nversion = 2\n\n[[delegation_policy.classes]]\ntool = \"Bash\"\ncommand_prefix = \"git tag\"\n"
	if _, err := control.NewApprovals(o.store).PublishDelegation(t.Context(), scope, authority.DelegationDraft{Body: body, Policy: policy}, member); err != nil {
		t.Fatal(err)
	}
	raise := func(id, command string, by ledger.Actor) string {
		input := `{"command":"` + command + `"}`
		digest := authority.LandingDigest("Bash", input, "")
		err := control.NewApprovals(o.store).Raise(t.Context(), scope, authority.Approval{
			ID: id, Lane: "lane-1", WorkOrder: "wo", Generation: 1, Kind: authority.KindToolCall, Subject: "Bash",
			SubjectDigest: digest, Input: input, Summary: command, Command: command, RequestedBy: authority.Principal(by),
		}, by)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}
	self := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: o.subject.Auth().WorkerID}
	covered := raise("covered", "git tag v1", lane)
	raise("beyond", "rm -rf build", lane)
	raise("own", "git tag v2", self)
	items := o.digest(t)
	if got := kindsOf(items); !slices.Equal(got, []string{"approval:covered"}) {
		t.Fatalf("digest %v", got)
	}
	if items[0].Digest != covered {
		t.Errorf("digest %q, want %q", items[0].Digest, covered)
	}
}
