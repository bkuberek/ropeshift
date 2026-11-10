package worker_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// aPlanningLaneAtRest is a planning lane held mid-turn, so a test can ask in its name.
func aPlanningLaneAtRest(t *testing.T) *laneRig {
	t.Helper()
	r := newPlanningRig(t, func(*fake.AgentDriver) {})
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	return r
}

// A planning lane's question is the person's: it stands in Needs you with no ladder trace, and the
// lane parks on it as any lane does.
func TestAPlanningLanesAskGoesStraightToThePerson(t *testing.T) {
	r := aPlanningLaneAtRest(t)
	reply := r.ask(t, lanes.AskRequest{Text: "Who signs in to the admin console?", Categories: []string{questions.CategoryProduct}, Blocking: true})
	if !reply.OK || reply.Ask == nil || reply.Ask.Status != questions.AskPending {
		t.Fatalf("reply %+v", reply)
	}
	rows := r.questionList(t)
	if len(rows) != 1 {
		t.Fatalf("questions %+v", rows)
	}
	row := rows[0]
	if row.Status != questions.StatusNeedsHuman || !row.Direct() || len(row.LadderTrace) != 0 || row.AskedBy != "lane-1" || !row.Blocking {
		t.Errorf("question %+v", row)
	}
	if repeat := r.ask(t, lanes.AskRequest{Text: "who signs in to the admin console", Categories: []string{questions.CategoryProduct}}); !repeat.OK ||
		repeat.Ask.Status != questions.AskAttached || repeat.Ask.QuestionID != row.ID {
		t.Errorf("a repeat: %+v", repeat)
	}
}

// With no ladder to name a category, the lane has to, and the refusal tells it so.
func TestAPlanningLanesAskNeedsACategory(t *testing.T) {
	r := aPlanningLaneAtRest(t)
	reply := r.ask(t, lanes.AskRequest{Text: "Who signs in to the admin console?"})
	if reply.OK || reply.Code != lanes.CodeInvalid || !strings.Contains(reply.Message, "category") {
		t.Errorf("reply %+v", reply)
	}
	if rows := r.questionList(t); len(rows) != 0 {
		t.Errorf("questions %+v", rows)
	}
}

// Any other lane's question climbs the ladder, and the control plane reads which kind of lane asked
// from the ledger's work order. A lane that asserts the other kind changes nothing.
func TestTheLedgerSaysWhoseQuestionGoesStraightToThePerson(t *testing.T) {
	tests := []struct {
		name   string
		kind   workorders.Kind
		assert bool
		direct bool
		status questions.Status
	}{
		{"a plan asking as itself", workorders.KindPlan, true, true, questions.StatusNeedsHuman},
		{"a plan that denies being one", workorders.KindPlan, false, true, questions.StatusNeedsHuman},
		{"a change that claims to be a plan", workorders.KindChange, true, false, questions.StatusOpen},
		{"a change asking as itself", workorders.KindChange, false, false, questions.StatusOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newRig(t)
			base.kind = tt.kind
			r := newLaneRigWith(t, base, func(*fake.AgentDriver) {})
			r.dispatched()
			g := newGate()
			r.finishLater(g)
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
			r.atGate(g)
			lane, err := r.journal.Lane(t.Context(), "lane-1")
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.plane.Ask(t.Context(), questions.LaneAsk{
				Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, WorkOrder: r.id, Fence: lane.Fence,
				Ask: questions.Ask{
					ID: "q-" + string(tt.kind), Text: "Who signs in to the admin console?", Categories: []string{questions.CategoryProduct},
					Lane: "lane-1", Generation: 1, Direct: tt.assert,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			rows := r.questionList(t)
			if len(rows) != 1 || rows[0].Direct() != tt.direct || rows[0].Status != tt.status {
				t.Errorf("questions %+v", rows)
			}
		})
	}
}

// No orchestrator works a planner's question: it is not next, and each rung a session can reach is
// refused with the reason, whichever tool reaches it.
func TestAPlannersQuestionIsNoOrchestratorsToWork(t *testing.T) {
	r := aToolRig(t)
	other := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "another-worker"}
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	if _, err := ops.Take(t.Context(), scope, "q1", 60_000, other); err != nil {
		t.Fatal(err)
	}
	_, err := ops.Ask(t.Context(), scope, questions.Ask{
		ID: "planner-q", Text: "Who signs in to the admin console?", Categories: []string{questions.CategoryProduct}, Direct: true, Lane: "plan-lane",
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.done(t, lanes.ToolQuestionNext, nil); string(got) != "null" {
		t.Errorf("next %s", got)
	}
	calls := map[string]struct {
		tool string
		args map[string]any
	}{
		"decide": {lanes.ToolDecisionRecord, map[string]any{
			"question": "planner-q", "status": "decided", "statement": "Staff.", "rationale": "Because.", "scope": "login", "reversibility": "reversible",
			"citations": []string{"PAY-12"},
		}},
		"assume": {lanes.ToolDecisionRecord, map[string]any{
			"question": "planner-q", "status": "assumed", "statement": "Staff.", "rationale": "Because.", "scope": "login", "reversibility": "reversible",
			"citations": []string{"PAY-12"},
		}},
		"resolve":  {lanes.ToolDecisionRecord, map[string]any{"question": "planner-q", "decision": "d0"}},
		"escalate": {lanes.ToolQuestionEscalate, map[string]any{"question": "planner-q", "reason": "scope_change", "options": []string{"a", "b"}, "recommendation": "a"}},
		"attach":   {lanes.ToolQuestionAttach, map[string]any{"question": "planner-q", "into": "q1"}},
	}
	for name, call := range calls {
		reply := r.tool(t, call.tool, call.args)
		if reply.OK || reply.Code != lanes.CodeForbidden || !strings.Contains(reply.Message, "planning lane") {
			t.Errorf("%s: %+v", name, reply)
		}
	}
	if got := r.question(t, "planner-q"); got.Status != questions.StatusNeedsHuman || got.ClaimedBy != "" || got.DecisionID != "" || len(got.LadderTrace) != 0 {
		t.Errorf("question %+v", got)
	}
}
