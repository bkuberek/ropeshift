package control_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ladderAtRungThree records a lane's question about wo-a in the given categories, has the orchestrator take it, and
// climbs rungs 1 and 2, so the question stands where an orchestrator decides or escalates it. It returns the call the
// orchestrator acts with and what question_next read.
func ladderAtRungThree(t *testing.T, r *planeRig, categories ...string) (ledger.OrchestratorCall, control.LadderRead) {
	t.Helper()
	ctx := t.Context()
	r.ready("pa", "wo-a")
	if _, err := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{}).RecordQuestion(ctx, "pa", questions.Question{
		ID: "q-a", Text: "How many repositories does the product have?", Categories: categories,
		SubjectKind: workorders.SubjectKind, SubjectID: "wo-a",
		Options: []string{"One per service", "One for everything"}, Recommendation: "One per service",
		RecommendationRationale: "The plan fixes one repository per service.",
	}, projectMember); err != nil {
		t.Fatal(err)
	}
	call := r.orchestrating()
	call.Project = "pa"
	reply, err := r.plane.Orchestrate(ctx, ledger.VerbNext, call)
	if err != nil {
		t.Fatal(err)
	}
	var read control.LadderRead
	if err := json.Unmarshal(reply.Result, &read); err != nil {
		t.Fatal(err)
	}
	if read.Question.ID != "q-a" {
		t.Fatalf("the next question %s", reply.Result)
	}
	call.Question = "q-a"
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		step, err := json.Marshal(questions.Step{Rung: rung, Outcome: "no_match"})
		if err != nil {
			t.Fatal(err)
		}
		climb := call
		climb.Body = step
		if _, err := r.plane.Orchestrate(ctx, ledger.VerbClimb, climb); err != nil {
			t.Fatal(err)
		}
	}
	return call, read
}

func escalateOneWay(r *planeRig, call ledger.OrchestratorCall) error {
	body, err := json.Marshal(questions.Escalation{Reason: questions.ReasonOneWay})
	if err != nil {
		return err
	}
	call.Body = body
	_, err = r.plane.Orchestrate(r.t.Context(), ledger.VerbEscalate, call)
	return err
}

func decideOneWay(r *planeRig, call ledger.OrchestratorCall) (questions.Decision, error) {
	body, err := json.Marshal(questions.Ruling{
		ID: "ruling-a", Status: questions.DecisionDecided, Statement: "One repository per service.",
		Rationale: "The plan fixes one repository per service, and the lane recommends it.", Citations: []string{"wo:wo-a"},
		Reversibility: questions.OneWay,
	})
	if err != nil {
		return questions.Decision{}, err
	}
	call.Body = body
	reply, err := r.plane.Orchestrate(r.t.Context(), ledger.VerbRule, call)
	if err != nil {
		return questions.Decision{}, err
	}
	var recorded control.RecordedDecision
	if err := json.Unmarshal(reply.Result, &recorded); err != nil {
		return questions.Decision{}, err
	}
	return recorded.Decision, nil
}

// acceptOneWay has a member accept worker-1's one-way acknowledgement, as Settings does.
func acceptOneWay(t *testing.T, r *planeRig) {
	t.Helper()
	if err := control.NewAutonomy(r.stores["pa"]).AcknowledgeOneWay(t.Context(), "pa", "worker-1", authority.OneWayDigest(), projectMember); err != nil {
		t.Fatal(err)
	}
}

// TestAnUnattendedOrchestratorDecidesAOneWayChoiceThePolicyLeavesIt holds the ladder of a worker a member set to
// unattended and whose one-way acknowledgement a member accepted: a one-way choice in a category the decision policy
// leaves open is the orchestrator's, so the gate refuses to take it to a member, and the orchestrator records it as
// decided, with its rationale, for a member to overturn.
func TestAnUnattendedOrchestratorDecidesAOneWayChoiceThePolicyLeavesIt(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	acceptOneWay(t, r)
	call, read := ladderAtRungThree(t, r, questions.CategoryArchitecture)
	if !strings.Contains(read.Yours, "unattended") {
		t.Errorf("question_next says %q of what the orchestrator decides, which does not say the worker is unattended", read.Yours)
	}
	var gate *questions.GateError
	if err := escalateOneWay(r, call); !errors.As(err, &gate) {
		t.Fatalf("escalating a one-way architecture choice from an unattended worker: %v, want the gate's refusal", err)
	}
	decision, err := decideOneWay(r, call)
	if err != nil {
		t.Fatalf("deciding the one-way choice: %v", err)
	}
	if decision.Reversibility != questions.OneWay || decision.DecidedBy != "worker-1" || decision.Rationale == "" {
		t.Errorf("the decision %+v, want a one-way decision of worker-1 with its rationale", decision)
	}
}

// TestAnUnattendedOrchestratorEscalatesAOneWayChoiceUntilAMemberAcceptsTheAcknowledgement holds the acknowledgement
// every worker needs, an API key's too: until a member accepts it, a one-way choice reaches a member at unattended as it
// does at attended, and question_next says why (FR-QD-8).
func TestAnUnattendedOrchestratorEscalatesAOneWayChoiceUntilAMemberAcceptsTheAcknowledgement(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	call, read := ladderAtRungThree(t, r, questions.CategoryArchitecture)
	if !strings.Contains(read.Yours, "one-way acknowledgement") {
		t.Errorf("question_next says %q, which does not say the one-way acknowledgement waits for a member", read.Yours)
	}
	if _, err := decideOneWay(r, call); !errors.Is(err, questions.ErrOneWay) {
		t.Errorf("deciding a one-way choice before the acknowledgement: %v, want %v", err, questions.ErrOneWay)
	}
	if err := escalateOneWay(r, call); err != nil {
		t.Errorf("escalating a one-way choice before the acknowledgement: %v", err)
	}
}

// TestAMemberAcceptsTheOneWayAcknowledgementOnce records the acceptance as one event of the installation's log, which
// names the statement it covers, and refuses anyone but a member and a statement other than the current one.
func TestAMemberAcceptsTheOneWayAcknowledgementOnce(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	autonomy := control.NewAutonomy(r.stores["pa"])
	if err := autonomy.AcknowledgeOneWay(ctx, "pa", "worker-1", "sha256:another", projectMember); !errors.Is(err, authority.ErrStaleStatement) {
		t.Errorf("accepting another statement: %v, want %v", err, authority.ErrStaleStatement)
	}
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "worker-1"}
	if err := autonomy.AcknowledgeOneWay(ctx, "pa", "worker-1", authority.OneWayDigest(), orchestrator); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("an orchestrator accepting its own acknowledgement: %v, want %v", err, workorders.ErrForbidden)
	}
	view, err := autonomy.View(ctx, "pa", "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.OneWay.Accepted != nil || view.OneWay.Statement != authority.OneWayStatement || view.OneWay.Digest != authority.OneWayDigest() {
		t.Errorf("before a member accepts it, the view reads %+v", view.OneWay)
	}
	acceptOneWay(t, r)
	acceptOneWay(t, r)
	if view, err = autonomy.View(ctx, "pa", "worker-1"); err != nil {
		t.Fatal(err)
	}
	if view.OneWay.Accepted == nil || view.OneWay.Accepted.By != projectMember.ID || view.OneWay.Accepted.Digest != authority.OneWayDigest() {
		t.Errorf("after a member accepts it, the view reads %+v", view.OneWay)
	}
	var accepted int
	err = r.state.Installation.Read(ctx, func(tx ports.SharedReadTx) error {
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			if event.Type == authority.EventOneWayAcknowledged && event.Subject.ID == "worker-1" {
				accepted++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted != 1 {
		t.Errorf("two acceptances recorded %d events, want 1", accepted)
	}
}

// publishParkOnOneWay publishes a lane policy that parks a lane on a one-way decision and tells it of any other.
func publishParkOnOneWay(t *testing.T, r *planeRig) {
	t.Helper()
	policy := authority.LanePolicy{Version: 2, OnContractVersion: authority.LaneNotify, OnBindingDecision: authority.LaneNotify, OnOneWayDecision: authority.LanePark}
	body := "[lane_policy]\nversion = 2\non_contract_version = \"notify\"\non_binding_decision = \"notify\"\non_one_way_decision = \"park\"\n"
	if _, err := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{}).PublishLanePolicy(t.Context(), "pa", authority.LanePolicyDraft{Body: body, Policy: policy}, projectMember); err != nil {
		t.Fatal(err)
	}
}

// parkWanted reads what a policy asked of a lane.
func parkWanted(t *testing.T, r *planeRig, lane string) string {
	t.Helper()
	var wanted string
	err := r.stores["pa"].Read(t.Context(), "pa", func(tx ports.ReadTx) error {
		row, err := tx.Lanes().Row(lane)
		wanted = row.ParkWanted
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return wanted
}

// TestAnOrchestratorsOneWayDecisionParksNoLane holds a decision an unattended orchestrator made to blocking no work: a
// lane policy that parks a lane on a one-way decision parks none for it, and a member who keeps it records the same
// decision, which binds nothing again. A member's own one-way decision still parks the lane (FR-QD-8).
func TestAnOrchestratorsOneWayDecisionParksNoLane(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	publishParkOnOneWay(t, r)
	acceptOneWay(t, r)
	call, _ := ladderAtRungThree(t, r, questions.CategoryArchitecture)
	if err := r.claim("pa", "wo-a"); err != nil {
		t.Fatal(err)
	}
	for _, lease := range r.desired().Leases {
		if lease.WorkOrder != "wo-a" {
			continue
		}
		running := workorders.Lease{WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence}
		if _, err := control.NewLeases(r.stores["pa"]).StartLane(ctx, "pa", running, "lane-wo-a", ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-1"}); err != nil {
			t.Fatal(err)
		}
	}
	decision, err := decideOneWay(r, call)
	if err != nil {
		t.Fatal(err)
	}
	if wanted := parkWanted(t, r, "lane-wo-a"); wanted != "" {
		t.Errorf("the orchestrator's one-way decision asks the lane to park: %q", wanted)
	}
	decisions := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{})
	kept := decision
	kept.ID, kept.Supersedes, kept.DecidedBy, kept.Overlaps = "kept-a", decision.ID, "", nil
	if _, err := decisions.RecordDecision(ctx, "pa", kept, projectMember); err != nil {
		t.Fatalf("keeping the decision: %v", err)
	}
	if wanted := parkWanted(t, r, "lane-wo-a"); wanted != "" {
		t.Errorf("keeping the orchestrator's decision asks the lane to park: %q", wanted)
	}
	own := questions.Decision{
		ID: "member-a", Status: questions.DecisionDecided, Statement: "Name the repositories after their services.", Rationale: "The plan names them so.",
		Scope: "wo:wo-a", Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
	}
	if _, err := decisions.RecordDecision(ctx, "pa", own, projectMember); err != nil {
		t.Fatal(err)
	}
	if wanted := parkWanted(t, r, "lane-wo-a"); wanted == "" {
		t.Error("a member's one-way decision asks the lane to park nothing, so this test cannot see a park")
	}
}

// TestAnUnattendedOrchestratorEscalatesWhatThePolicyKeeps holds the rule an unattended worker keeps: a one-way choice
// in a category the decision policy keeps for a member reaches a member, and the orchestrator cannot decide it.
func TestAnUnattendedOrchestratorEscalatesWhatThePolicyKeeps(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	acceptOneWay(t, r)
	call, read := ladderAtRungThree(t, r, questions.CategoryArchitecture, questions.CategorySecurity)
	if !strings.Contains(read.Yours, questions.CategorySecurity) {
		t.Errorf("question_next says %q of what the orchestrator decides, which does not name the category the policy keeps", read.Yours)
	}
	if _, err := decideOneWay(r, call); !errors.Is(err, questions.ErrOneWay) {
		t.Errorf("deciding a security choice: %v, want %v", err, questions.ErrOneWay)
	}
	if err := escalateOneWay(r, call); err != nil {
		t.Errorf("escalating a security choice: %v", err)
	}
}

// TestAnAttendedOrchestratorEscalatesAOneWayChoice holds the ladder of a worker a member attends: a one-way choice
// reaches a member whatever its category, and the orchestrator decides only what can be undone.
func TestAnAttendedOrchestratorEscalatesAOneWayChoice(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	if err := control.NewAutonomy(r.stores["pa"]).SetLevel(t.Context(), "pa", "worker-1", authority.LevelAttended, projectMember); err != nil {
		t.Fatal(err)
	}
	call, read := ladderAtRungThree(t, r, questions.CategoryArchitecture)
	if strings.Contains(read.Yours, "unattended") {
		t.Errorf("question_next says %q to an attended worker's orchestrator", read.Yours)
	}
	if _, err := decideOneWay(r, call); !errors.Is(err, questions.ErrOneWay) {
		t.Errorf("deciding a one-way choice on an attended worker: %v, want %v", err, questions.ErrOneWay)
	}
	if err := escalateOneWay(r, call); err != nil {
		t.Errorf("escalating a one-way choice on an attended worker: %v", err)
	}
}
