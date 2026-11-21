package server_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// reviews lists the one-way decisions Needs you shows for review, by decision.
func reviews(t *testing.T, d *apitest.Daemon) map[string]api.NeedItem {
	t.Helper()
	out := map[string]api.NeedItem{}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedOneWayReview {
			out[item.ID] = item
		}
	}
	return out
}

// TestNeedsYouShowsAnOrchestratorsOneWayDecisionForReview holds the review a one-way decision of an unattended
// orchestrator waits for (FR-QD-8): once a member set the worker to unattended and accepted its one-way acknowledgement,
// the orchestrator records a one-way decision, and Needs you shows it with Keep and Overturn, and neither a member's
// one-way decision nor an orchestrator's reversible one. Keeping it records the same decision as the member's, and
// overturning it reverses it, and either ends the review.
func TestNeedsYouShowsAnOrchestratorsOneWayDecisionForReview(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	d, w, _ := aWorld(t)
	if err := d.Client.AcknowledgeRisk(ctx, apitest.WorldWorker, api.AcknowledgeRisk{Digest: authority.RiskDigest()}); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.SetAutonomy(ctx, apitest.WorldWorker, api.SetAutonomy{Level: authority.LevelUnattended}); err != nil {
		t.Fatal(err)
	}
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	// Each decision binds a work order of its own, so none overlaps another.
	oneWay := func(id, statement string) questions.Decision {
		w.Running("wo-"+id, statement)
		return questions.Decision{
			ID: id, Status: questions.DecisionDecided, Statement: statement, Rationale: "The plan fixes it.", Citations: []string{"PAY-12"},
			Scope: "wo:wo-" + id, Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
		}
	}
	if _, err := w.Questions.RecordDecision(ctx, apitest.Scope, oneWay("orchestrator-early", "Bill by the seat."), orchestrator); !errors.Is(err, questions.ErrOneWay) {
		t.Fatalf("an orchestrator's one-way decision before the acknowledgement: %v, want %v", err, questions.ErrOneWay)
	}
	if err := d.Client.AcknowledgeOneWay(ctx, apitest.WorldWorker, api.AcknowledgeOneWay{Digest: authority.OneWayDigest()}); err != nil {
		t.Fatal(err)
	}
	for _, decision := range []questions.Decision{oneWay("orchestrator-kept", "One repository per service."), oneWay("orchestrator-overturned", "Number work orders with ULIDs.")} {
		if _, err := w.Questions.RecordDecision(ctx, apitest.Scope, decision, orchestrator); err != nil {
			t.Fatalf("the orchestrator's one-way decision %s: %v", decision.ID, err)
		}
	}
	reversible := oneWay("orchestrator-reversible", "Log retries at warn.")
	reversible.Reversibility = questions.Reversible
	if _, err := w.Questions.RecordDecision(ctx, apitest.Scope, reversible, orchestrator); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.RecordDecision(ctx, api.RecordDecision{
		ID: "member-one-way", Status: questions.DecisionDecided, Statement: "Store money as cents.", Rationale: "Rounding is a bug.",
		Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
	}); err != nil {
		t.Fatal(err)
	}

	waiting := reviews(t, d)
	if len(waiting) != 2 {
		t.Fatalf("Needs you shows %d one-way decisions for review, want the orchestrator's 2: %+v", len(waiting), waiting)
	}
	item := waiting["orchestrator-kept"]
	if item.OneWayReview == nil || item.OneWayReview.Decision.Statement != "One repository per service." || item.Since == 0 {
		t.Fatalf("the review item %+v", item)
	}
	if item.OneWayReview.DecidedByName != "solo" {
		t.Errorf("the review names the decider %q, want the worker's name", item.OneWayReview.DecidedByName)
	}
	if want := []string{api.ActDecisionKeep, api.ActDecisionOverturn}; len(item.OneWayReview.Actions) != 2 ||
		item.OneWayReview.Actions[0] != want[0] || item.OneWayReview.Actions[1] != want[1] {
		t.Errorf("the review offers %v, want %v", item.OneWayReview.Actions, want)
	}

	kept := item.OneWayReview.Decision
	if _, err := d.Client.RecordDecision(ctx, api.RecordDecision{
		ID: "kept-by-member", Status: kept.Status, Statement: kept.Statement, Rationale: kept.Rationale, Citations: kept.Citations,
		Scope: kept.Scope, Reversibility: kept.Reversibility, Categories: kept.Categories, Supersedes: kept.ID,
	}); err != nil {
		t.Fatalf("keeping the decision: %v", err)
	}
	if _, err := d.Client.OverturnDecision(ctx, "orchestrator-overturned", api.OverturnDecision{
		ID: "overturned-by-member", Statement: "Number work orders in sequence.", Rationale: "People read them aloud.",
	}); err != nil {
		t.Fatalf("overturning the decision: %v", err)
	}
	if left := reviews(t, d); len(left) != 0 {
		t.Errorf("after Keep and Overturn, Needs you still shows %+v", left)
	}
}
