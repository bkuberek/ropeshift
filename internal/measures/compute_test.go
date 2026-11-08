package measures_test

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/measures"
)

const hour = int64(3600 * 1000)

func value(t *testing.T, r measures.Ratio) float64 {
	t.Helper()
	if r.Value == nil {
		t.Fatalf("ratio %+v has no value", r)
	}
	return *r.Value
}

// A history with a known answer for every measure. The window is hours 10 to 20.
func scripted() measures.History {
	return measures.History{
		Interruptions: []measures.Occasion{
			{At: 9 * hour, Kind: measures.InterruptionBatch, Subject: "b-0"}, // before the window
			{At: 11 * hour, Kind: measures.InterruptionBatch, Subject: "b-1"},
			{At: 11 * hour, Kind: measures.InterruptionBatch, Subject: "b-1"}, // the same batch, recorded twice
			{At: 12 * hour, Kind: measures.InterruptionApproval, Subject: "a-1"},
			{At: 13 * hour, Kind: measures.InterruptionForced, Subject: "e-1"},
		},
		WorkOrders: []measures.WorkOrderFacts{
			{ID: "w1", Kind: "change", State: "done", CreatedAt: 1 * hour, ReadyAt: 2 * hour, DoneAt: 12 * hour},
			{ID: "w2", Kind: "change", State: "done", CreatedAt: 5 * hour, ReadyAt: 10 * hour, DoneAt: 14 * hour},
			{ID: "w3", Kind: "plan", State: "done", CreatedAt: 8 * hour, DoneAt: 18 * hour},
			{ID: "w4", Kind: "change", State: "done", CreatedAt: 1 * hour, ReadyAt: 2 * hour, DoneAt: 3 * hour}, // before
			{ID: "w5", Kind: "change", State: "running", CreatedAt: 11 * hour, ReadyAt: 12 * hour},
			{ID: "w6", Kind: "change", State: "parked", CreatedAt: 11 * hour},
			{ID: "w7", Kind: "rework", State: "contract_review", CreatedAt: 15 * hour, ReworkOf: "w1"},
		},
		Questions: []measures.QuestionFacts{
			{ID: "q1", AskedAt: 10 * hour, ClosedAt: 11 * hour, ClosedRung: 1},
			{ID: "q2", AskedAt: 10 * hour, ClosedAt: 11 * hour, ClosedRung: 4},
			{ID: "q3", AskedAt: 10 * hour, ReachedHumanAt: 11 * hour, ClosedAt: 12 * hour, ClosedRung: 5, ClosedByMember: true, Repeat: true},
			{ID: "q4", AskedAt: 10 * hour, ClosedAt: 12 * hour, ClosedRung: 2, ClosedByMember: true},
			{ID: "q5", AskedAt: 10 * hour, ReachedHumanAt: 13 * hour},
			{ID: "q6", AskedAt: 1 * hour, ClosedAt: 2 * hour, ClosedRung: 1}, // before
		},
		Decisions: []measures.DecisionFacts{
			{ID: "d1", RecordedAt: 11 * hour, Autonomous: true, Overturned: true},
			{ID: "d2", RecordedAt: 11 * hour, Autonomous: true},
			{ID: "d3", RecordedAt: 12 * hour, Autonomous: true},
			{ID: "d4", RecordedAt: 12 * hour, Overturned: true}, // a person's, which the rate does not hold
		},
		Parks: []measures.Park{
			{Trigger: "answer", ParkedAt: 10 * hour, WokenAt: 11 * hour},
			{Trigger: "answer", ParkedAt: 10 * hour, WokenAt: 13 * hour},
			{Trigger: "answer", ParkedAt: 10 * hour, WokenAt: 16 * hour},
			{Trigger: "at", ParkedAt: 12 * hour, WokenAt: 14 * hour},
			{Trigger: "at", ParkedAt: 1 * hour, WokenAt: 2 * hour}, // before
		},
		Lanes: []measures.LaneCost{
			{WorkOrderKind: "change", EndedAt: 11 * hour, CostUSD: 1.0},
			{WorkOrderKind: "change", EndedAt: 13 * hour, CostUSD: 2.0, LowerBound: true},
			{WorkOrderKind: "change", EndedAt: 13 * hour, CostUSD: 0.5},
			{WorkOrderKind: "plan", EndedAt: 17 * hour, CostUSD: 0.25},
			{WorkOrderKind: "change", EndedAt: 3 * hour, CostUSD: 100}, // before
		},
		Attachments: []int64{11 * hour, 12 * hour, 2 * hour},
	}
}

func TestEveryMeasureFromAScriptedHistory(t *testing.T) {
	r := measures.Compute(scripted(), measures.Window{Name: "test", From: 10 * hour, To: 20 * hour})
	// Three interruptions: one batch, however many questions it held, one approval, and one forced action.
	if r.Interruptions != 3 || r.InterruptionsByKind[measures.InterruptionBatch] != 1 || r.InterruptionsByKind[measures.InterruptionApproval] != 1 ||
		r.InterruptionsByKind[measures.InterruptionForced] != 1 {
		t.Errorf("interruptions %d %v", r.Interruptions, r.InterruptionsByKind)
	}
	if r.Completed != 3 || value(t, r.InterruptionsPerCompleted) != 1 {
		t.Errorf("completed %d, north star %+v", r.Completed, r.InterruptionsPerCompleted)
	}
	if got := value(t, r.AutonomousDecisionRate); r.AutonomousDecisionRate.Denominator != 4 || got != 0.5 {
		t.Errorf("autonomous decision rate %+v", r.AutonomousDecisionRate)
	}
	if got := value(t, r.OverturnRate); r.OverturnRate.Denominator != 3 || got != 1.0/3 {
		t.Errorf("overturn rate %+v", r.OverturnRate)
	}
	if got := value(t, r.RepeatedQuestionRate); r.RepeatedQuestionRate.Denominator != 2 || got != 0.5 {
		t.Errorf("repeated question rate %+v", r.RepeatedQuestionRate)
	}
	if got := value(t, r.Rework); r.Rework.Numerator != 1 || got != 1.0/3 {
		t.Errorf("rework %+v", r.Rework)
	}
	if r.PreventedRepeats != 2 {
		t.Errorf("prevented repeats %d", r.PreventedRepeats)
	}
	answer := r.ParkTime["answer"]
	if answer.Count != 3 || *answer.MedianMs != 3*hour || *answer.P95Ms != 6*hour {
		t.Errorf("park time %+v", answer)
	}
	if at := r.ParkTime["at"]; at.Count != 1 || *at.MedianMs != 2*hour {
		t.Errorf("park time by at %+v", at)
	}
	if r.ReadyToDone.Count != 2 || *r.ReadyToDone.MedianMs != 4*hour || *r.ReadyToDone.P95Ms != 10*hour {
		t.Errorf("ready to done %+v", r.ReadyToDone)
	}
	if r.IntakeToDone.Count != 3 || *r.IntakeToDone.MedianMs != 10*hour || *r.IntakeToDone.P95Ms != 11*hour {
		t.Errorf("intake to done %+v", r.IntakeToDone)
	}
	if r.WorkInProgress != 2 {
		t.Errorf("work in progress %d", r.WorkInProgress)
	}
	change := r.CostPerOutcome["change"]
	if change.TotalUSD != 3.5 || change.Done != 2 || *change.PerDone != 1.75 || !change.LowerBound {
		t.Errorf("change cost %+v", change)
	}
	if plan := r.CostPerOutcome["plan"]; plan.TotalUSD != 0.25 || plan.Done != 1 || *plan.PerDone != 0.25 || plan.LowerBound {
		t.Errorf("plan cost %+v", plan)
	}
}

// A rate of nothing is no rate, so an empty window says so rather than reading zero.
func TestAnEmptyWindowHasNoRates(t *testing.T) {
	r := measures.Compute(scripted(), measures.Window{Name: "empty", From: 100 * hour, To: 200 * hour})
	for name, ratio := range map[string]measures.Ratio{
		"north star": r.InterruptionsPerCompleted, "overturn": r.OverturnRate, "autonomous": r.AutonomousDecisionRate,
		"repeated": r.RepeatedQuestionRate, "rework": r.Rework,
	} {
		if ratio.Value != nil {
			t.Errorf("%s reads %v with nothing under it", name, *ratio.Value)
		}
	}
	if r.WorkInProgress != 2 || r.ReadyToDone.MedianMs != nil || len(r.ParkTime) != 0 {
		t.Errorf("report %+v", r)
	}
}

func TestPointsLeaveOutARateOfNothing(t *testing.T) {
	r := measures.Compute(scripted(), measures.Window{Name: "empty", From: 100 * hour, To: 200 * hour})
	for _, p := range measures.Points(r) {
		if p.Name == "interruptions_per_completed" || p.Name == "overturn_rate" {
			t.Errorf("point %+v", p)
		}
	}
	full := measures.Points(measures.Compute(scripted(), measures.Window{From: 10 * hour, To: 20 * hour}))
	found := map[string]bool{}
	for _, p := range full {
		found[p.Name] = true
	}
	for _, name := range []string{"interruptions_per_completed", "overturn_rate", "rework_rate", "autonomous_decision_rate",
		"repeated_question_rate", "park_time", "flow_time", "work_in_progress", "cost_per_outcome", "prevented_repeats"} {
		if !found[name] {
			t.Errorf("no point for %s", name)
		}
	}
}
