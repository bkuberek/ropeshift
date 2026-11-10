package worker_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// pinned reads the pack the rig's claim pinned, at its newest version.
func (r *laneRig) pinned(t *testing.T) contextpacks.Pack {
	t.Helper()
	var pack contextpacks.Pack
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		claim, err := tx.Claims().Find(r.grant.ClaimID)
		if err != nil {
			return err
		}
		pack, err = tx.Packs().Latest(claim.PackID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func (r *laneRig) generationsOf(t *testing.T) []ledger.Generation {
	t.Helper()
	var out []ledger.Generation
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Generations("lane-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestALaneStartsWithItsPackInTheSystemPrompt(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	pack := r.pinned(t)
	spec := specs[0]
	if spec.SystemPromptFile == "" {
		t.Fatal("the session has no pack in its system prompt")
	}
	if got := r.worktrees().Context(spec.Dir); got != pack.Text {
		t.Errorf("the worktree holds %q, and the ledger %q", got, pack.Text)
	}
	if !strings.HasSuffix(spec.SystemPromptFile, lanes.PackFile) {
		t.Errorf("the system prompt reads %s", spec.SystemPromptFile)
	}
	if !strings.Contains(spec.Brief, lanes.PackFile) {
		t.Errorf("the brief does not point at the pack: %q", spec.Brief)
	}
	if !strings.Contains(spec.Brief, contextpacks.ToolGet) {
		t.Errorf("the brief does not say how to read past the pack: %q", spec.Brief)
	}
}

func (r *laneRig) callContext(t *testing.T, request lanes.ContextRequest) lanes.Reply {
	t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(t), Context: &request})
	if err != nil {
		t.Fatal(err)
	}
	return r.subject.AnswerLane(t.Context(), line)
}

// inASession is a lane holding a session, with its token known to the worker.
func inASession(t *testing.T) *laneRig {
	t.Helper()
	r := laneRigWith(t, noRestarts)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	return r
}

func TestALaneReadsPastItsPack(t *testing.T) {
	t.Parallel()
	r := inASession(t)
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	decided, err := ops.RecordDecision(t.Context(), scope, questions.Decision{
		ID: "d-1", Status: questions.DecisionDecided, Statement: "Use two retries.", Scope: "pay",
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	reply := r.callContext(t, lanes.ContextRequest{Tool: lanes.ToolContextGet, Kind: string(contextpacks.KindDecision), Ref: decided.ID})
	if !reply.OK || len(reply.Context) != 1 || !strings.Contains(reply.Context[0].Text, "Use two retries.") {
		t.Fatalf("reply %+v", reply)
	}
	search := r.callContext(t, lanes.ContextRequest{Tool: lanes.ToolDecisionSearch, Query: "retries"})
	if !search.OK || len(search.Context) != 1 {
		t.Fatalf("reply %+v", search)
	}
	none := r.callContext(t, lanes.ContextRequest{Tool: lanes.ToolContextGet, Kind: string(contextpacks.KindDecision), Ref: "d-none"})
	if !none.OK || len(none.Context) != 0 {
		t.Errorf("a miss reads %+v", none)
	}
}

func TestARefusedReadSaysWhy(t *testing.T) {
	t.Parallel()
	r := inASession(t)
	if reply := r.callContext(t, lanes.ContextRequest{Tool: "lane_delete"}); reply.OK || reply.Code != lanes.CodeInvalid {
		t.Errorf("a tool nobody named reads %+v", reply)
	}
	r.link.FailLaneFacts.Store(true)
	reply := r.callContext(t, lanes.ContextRequest{Tool: lanes.ToolContextGet, Kind: string(contextpacks.KindDecision), Ref: "d-1"})
	if reply.OK || reply.Code != lanes.CodeUnavailable {
		t.Errorf("a control plane that is away reads %+v", reply)
	}
	r.link.FailLaneFacts.Store(false)
	r.eventually("the ledger read as impaired", func(view ledger.LaneView) bool { return view.LedgerReach == string(lanes.Impaired) })
}

// A read goes only as far as a lane's own lease, whatever line a process sends.
func TestAProcessWithNoTokenReadsNothing(t *testing.T) {
	t.Parallel()
	r := inASession(t)
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: "not-a-token", Context: &lanes.ContextRequest{Tool: lanes.ToolDecisionSearch}})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(t.Context(), line); reply.OK || reply.Code != lanes.CodeBadToken {
		t.Errorf("reply %+v", reply)
	}
}

func TestAMeasuredBaselineCanReadOversized(t *testing.T) {
	t.Parallel()
	const profile = "[context]\nmin_headroom_tokens = 50\n\n[context.roles.writer]\nhandoff_tokens = 100\n"
	tests := []struct {
		name  string
		first int64
		want  bool
	}{
		{"room to work", 40, false},
		{"no room to work", 60, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := laneRigWith(t, profile)
			g := newGate()
			r.finishLater(g)
			// A later call says nothing of a baseline, which is the first.
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Context: &lanes.Usage{InputTokens: tt.first}}, {Context: &lanes.Usage{InputTokens: 90}}, g.step()}))
			r.atGate(g)
			r.eventually("a status", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
			// The gate opens on the session's side, so a status can come before the worker read the call.
			r.eventually("the first call", func(ledger.LaneView) bool {
				record, err := r.journal.Lane(t.Context(), "lane-1")
				return err == nil && record.Progress.BaselineTokens == tt.first
			})
			if err := r.subject.Publish(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(r.read().Conditions, string(lanes.PackOversized)); got != tt.want {
				t.Errorf("oversized %v, want %v", got, tt.want)
			}
		})
	}
}

// The first call of a generation is what it started with, and a later call is a conversation.
func TestAGenerationRecordsItsBaseline(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, noRestarts)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, {Context: &lanes.Usage{InputTokens: 700}}, {Context: &lanes.Usage{InputTokens: 900}}, r.reports(workorders.OutcomeCompleted), completed(0.25),
	}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if generations := r.generationsOf(t); len(generations) != 1 || generations[0].BaselineTokens != 700 {
		t.Errorf("generations %+v", generations)
	}
}

// oneWayOn publishes a lane policy that parks, and decides something nobody can take back under a
// lane's work.
func (r *laneRig) oneWayOn(t *testing.T) {
	t.Helper()
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	const body = "[lane_policy]\nversion = 2\non_contract_version = \"notify\"\non_one_way_decision = \"park\"\n"
	draft := authority.LanePolicyDraft{Body: body, Policy: authority.LanePolicy{Version: 2, OnContractVersion: authority.LaneNotify, OnOneWayDecision: authority.LanePark}}
	if _, err := ops.PublishLanePolicy(t.Context(), scope, draft, member); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.RecordDecision(t.Context(), scope, questions.Decision{
		ID: "d-one-way", Status: questions.DecisionDecided, Statement: "Keep audit data in region.", Scope: r.id, Reversibility: questions.OneWay,
	}, member); err != nil {
		t.Fatal(err)
	}
}

func TestAPolicyParksALaneForAPerson(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.letGoOnFailure(t)
	r.oneWayOn(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("the lane parking", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Parking) })
	close(r.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	wake := r.wakeOf(t, 1)
	if wake.Expr != lanes.HumanWake() {
		t.Errorf("a lane a policy stopped waits on %s", wake.Expr)
	}
}

// The default policy tells a lane and leaves it running.
func TestALaneNoPolicyAskedKeepsWorking(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.letGoOnFailure(t)
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	if _, err := ops.RecordDecision(t.Context(), scope, questions.Decision{
		ID: "d-one-way", Status: questions.DecisionDecided, Statement: "Keep audit data in region.", Scope: r.id, Reversibility: questions.OneWay,
	}, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("a lane nothing asked to stop reads %s", view.ActualState)
	}
}

// A generation reads the newest version when it starts, and the one before it keeps what it read.
func TestANextGenerationReadsTheNewestPack(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.letGoOnFailure(t)
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	first := r.pinned(t)
	if _, err := ops.RecordDecision(t.Context(), scope, questions.Decision{ID: "d-old", Status: questions.DecisionDecided, Statement: "Use two retries.", Scope: "elsewhere"}, member); err != nil {
		t.Fatal(err)
	}
	addition := contextpacks.Addition{WorkOrder: r.id, Kind: contextpacks.KindKnowledge, Ref: "wiki:1", Text: "A page."}
	if err := control.NewPacks(r.store).AddPackItem(t.Context(), scope, addition, member); err != nil {
		t.Fatal(err)
	}
	r.renewPack(t, first)
	r.handOff(t)
	r.awaitSecondGeneration(t)
	specs := r.driver.Specs()
	generations := r.generationsOf(t)
	writes := r.worktrees().ContextWrites(specs[1].Dir)
	if len(writes) != 2 || writes[0] != first.Text || writes[1] == first.Text {
		t.Fatalf("a worktree held %d versions", len(writes))
	}
	if generations[0].PackVersion != first.Version || generations[1].PackVersion != first.Version+1 {
		t.Errorf("generations %+v", generations)
	}
	_, doc := r.storedHandoff(t, 1)
	if doc.Deterministic.ContextPack == nil || doc.Deterministic.ContextPack.Version != first.Version {
		t.Errorf("a handoff states %+v, and its generation read version %d", doc.Deterministic.ContextPack, first.Version)
	}
}

// renewPack stands for what a decision does to a pack, because this rig's work cites none.
func (r *laneRig) renewPack(t *testing.T, previous contextpacks.Pack) {
	t.Helper()
	next := previous
	next.Version, next.Text = previous.Version+1, previous.Text+"\nA later version.\n"
	raw, err := next.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	next.Digest = contextpacks.DigestSHA256 + hex.EncodeToString(sum[:])
	payload, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	err = r.store.Write(t.Context(), scope, "note", func(tx ports.WriteTx) error {
		if err := tx.Packs().Insert(next); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: contextpacks.EventBuilt, Actor: member, Subject: ledger.Subject{Kind: contextpacks.SubjectPack, ID: next.ID}, Data: payload,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A handoff under way has its own way out, and parking waits for it to finish.
func TestAHandoffUnderWayOutranksAPolicy(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.letGoOnFailure(t)
	if err := r.subject.HandOff(t.Context(), "lane-1", lanes.HandoffContextThreshold); err != nil {
		t.Fatal(err)
	}
	r.oneWayOn(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if view := r.read(); view.ActualState != string(lanes.HandingOff) {
		t.Fatalf("a handoff under way moved to %s", view.ActualState)
	}
	close(r.release)
	r.awaitSecondGeneration(t)
}

// letGoOnFailure ends the first turn of a test that stopped before it would have.
func (r *handoffRig) letGoOnFailure(t *testing.T) {
	t.Cleanup(func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
	})
}

func TestAGenerationRecordsThePackItRead(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	pack := r.pinned(t)
	generations := r.generationsOf(t)
	if len(generations) != 1 || generations[0].PackID != pack.ID || generations[0].PackVersion != pack.Version {
		t.Errorf("generations %+v, pack %s@%d", generations, pack.ID, pack.Version)
	}
}

// A lane keeps its pack across a handoff, and the next generation's cache depends on the bytes
// staying the same.
func TestALaneKeepsItsPackAcrossAHandoff(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.handOff(t)
	r.awaitSecondGeneration(t)
	specs := r.driver.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d sessions", len(specs))
	}
	if specs[0].SystemPromptFile == "" || specs[1].SystemPromptFile != specs[0].SystemPromptFile {
		t.Errorf("the files %q and %q", specs[0].SystemPromptFile, specs[1].SystemPromptFile)
	}
	pack := r.pinned(t)
	if got := r.worktrees().Context(specs[1].Dir); got != pack.Text {
		t.Errorf("the second generation reads %q", got)
	}
	if writes := r.worktrees().ContextWrites(specs[1].Dir); len(writes) != 2 || writes[0] != writes[1] {
		t.Errorf("two generations wrote %d packs that differ", len(writes))
	}
	_, doc := r.storedHandoff(t, 1)
	if doc.Deterministic.ContextPack == nil || doc.Deterministic.ContextPack.ID != pack.ID || doc.Deterministic.ContextPack.Version != pack.Version {
		t.Errorf("the handoff names %+v, and the lane read %s@%d", doc.Deterministic.ContextPack, pack.ID, pack.Version)
	}
	generations := r.generationsOf(t)
	if len(generations) != 2 || generations[1].PackID != pack.ID || generations[1].PackVersion != pack.Version {
		t.Errorf("generations %+v", generations)
	}
}

func TestALaneThatCannotWriteItsPackDoesNotStart(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, noRestarts)
	r.worktrees().FailContext(errors.New("the disk is full"))
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(r.driver.Specs()); got != 0 {
		t.Errorf("%d sessions started with no pack", got)
	}
	if row := r.row(); row.State != workorders.Failed {
		t.Errorf("row %+v", row)
	}
}

// A claim from before packs has none, and that lane still runs.
func TestALaneWithNoPackStartsWithoutTheFlag(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	base.link.StripPacks.Store(true)
	r := newLaneRigFor(t, base)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 || specs[0].SystemPromptFile != "" {
		t.Fatalf("specs %+v", specs)
	}
	if strings.Contains(specs[0].Brief, lanes.PackFile) {
		t.Errorf("the brief points at a pack that is not there: %q", specs[0].Brief)
	}
	if generations := r.generationsOf(t); len(generations) != 1 || generations[0].PackID != "" {
		t.Errorf("generations %+v", generations)
	}
	if !slices.Contains(lanes.Tools, contextpacks.ToolGet) {
		t.Errorf("a lane has no tool to read what its pack names: %v", lanes.Tools)
	}
}
