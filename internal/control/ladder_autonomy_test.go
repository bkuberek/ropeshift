package control_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
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

// TestAnUnattendedOrchestratorDecidesAOneWayChoiceThePolicyLeavesIt holds the ladder of a worker a member set to
// unattended: a one-way choice in a category the decision policy leaves open is the orchestrator's, so the gate refuses
// to take it to a member, and the orchestrator records it as decided, with its rationale, for a member to overturn.
func TestAnUnattendedOrchestratorDecidesAOneWayChoiceThePolicyLeavesIt(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
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

// TestAnUnattendedOrchestratorEscalatesWhatThePolicyKeeps holds the rule an unattended worker keeps: a one-way choice
// in a category the decision policy keeps for a member reaches a member, and the orchestrator cannot decide it.
func TestAnUnattendedOrchestratorEscalatesWhatThePolicyKeeps(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
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
