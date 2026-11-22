package server_test

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
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

// TestAMemberWithdrawsTheOneWayAcknowledgementOverTheAPI holds the withdrawal route: once a member withdraws the
// acknowledgement, the unattended worker's orchestrator records no one-way decision, and the worker reads as waiting for
// a member until a member accepts it again.
func TestAMemberWithdrawsTheOneWayAcknowledgementOverTheAPI(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	d, w, _ := aWorld(t)
	if err := d.Client.AcknowledgeRisk(ctx, apitest.WorldWorker, api.AcknowledgeRisk{Digest: authority.RiskDigest()}); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.SetAutonomy(ctx, apitest.WorldWorker, api.SetAutonomy{Level: authority.LevelUnattended}); err != nil {
		t.Fatal(err)
	}
	accept := func() {
		t.Helper()
		if err := d.Client.AcknowledgeOneWay(ctx, apitest.WorldWorker, api.AcknowledgeOneWay{Digest: authority.OneWayDigest()}); err != nil {
			t.Fatal(err)
		}
	}
	accepted := func() bool {
		t.Helper()
		view, err := d.Client.Autonomy(ctx, apitest.WorldWorker)
		if err != nil {
			t.Fatal(err)
		}
		return view.OneWay.Accepted != nil
	}
	accept()
	if err := d.Client.WithdrawOneWay(ctx, apitest.WorldWorker); err != nil {
		t.Fatal(err)
	}
	if accepted() {
		t.Error("after a withdrawal the worker reads as accepted")
	}
	w.Running("wo-withdrawn", "Bill by the seat.")
	decision := questions.Decision{
		ID: "after-withdrawal", Status: questions.DecisionDecided, Statement: "Bill by the seat.", Rationale: "The plan fixes it.", Citations: []string{"PAY-12"},
		Scope: "wo:wo-withdrawn", Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
	}
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	if _, err := w.Questions.RecordDecision(ctx, apitest.Scope, decision, orchestrator); !errors.Is(err, questions.ErrOneWay) {
		t.Errorf("an orchestrator's one-way decision after a withdrawal: %v, want %v", err, questions.ErrOneWay)
	}
	accept()
	if !accepted() {
		t.Error("a member's acceptance after a withdrawal does not stand")
	}
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

// eventReads counts the reads of events a read makes, by the way it makes them.
type eventReads struct {
	about  atomic.Int64
	ofType atomic.Int64
}

// countingReadTx is a read that counts how it reads events.
type countingReadTx struct {
	ports.ReadTx
	reads *eventReads
}

func (c countingReadTx) EventsAbout(subject ledger.Subject) ([]ledger.Event, error) {
	c.reads.about.Add(1)
	return c.ReadTx.EventsAbout(subject)
}

func (c countingReadTx) EventsOfTypeAbout(eventType, subjectKind string, ids []string) ([]ledger.Event, error) {
	c.reads.ofType.Add(1)
	return c.ReadTx.EventsOfTypeAbout(eventType, subjectKind, ids)
}

// anUnattendedOrchestrator sets the world's worker to unattended, has a member accept its one-way acknowledgement, and
// returns what records one-way decisions as its orchestrator, each binding a work order of its own so none overlaps
// another.
func anUnattendedOrchestrator(t *testing.T, d *apitest.Daemon, w *apitest.World) (orchestrator ledger.Actor, oneWay func(id, statement string) questions.Decision) {
	t.Helper()
	ctx := t.Context()
	if err := d.Client.AcknowledgeRisk(ctx, apitest.WorldWorker, api.AcknowledgeRisk{Digest: authority.RiskDigest()}); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.SetAutonomy(ctx, apitest.WorldWorker, api.SetAutonomy{Level: authority.LevelUnattended}); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.AcknowledgeOneWay(ctx, apitest.WorldWorker, api.AcknowledgeOneWay{Digest: authority.OneWayDigest()}); err != nil {
		t.Fatal(err)
	}
	return ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}, func(id, statement string) questions.Decision {
		w.Running("wo-"+id, statement)
		return questions.Decision{
			ID: id, Status: questions.DecisionDecided, Statement: statement, Rationale: "The plan fixes it.", Citations: []string{"PAY-12"},
			Scope: "wo:wo-" + id, Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
		}
	}
}

// TestTheReviewOfOneWayDecisionsReadsTheirRecordingEventsOnce holds the read of the decisions waiting for review to one
// read of the events that recorded them, however many wait, and to the same item for each as a read of each decision's
// events gives: who recorded it, which a worker the ledger no longer holds does not change, and when.
func TestTheReviewOfOneWayDecisionsReadsTheirRecordingEventsOnce(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	d, w, _ := aWorld(t)
	orchestrator, oneWay := anUnattendedOrchestrator(t, d, w)
	const waiting = 40
	for i := range waiting {
		if _, err := w.Questions.RecordDecision(ctx, apitest.Scope, oneWay(fmt.Sprintf("orchestrator-%d", i), fmt.Sprintf("Decision %d.", i)), orchestrator); err != nil {
			t.Fatalf("the orchestrator's one-way decision %d: %v", i, err)
		}
	}
	// A member's one-way decision waits for no review, and its event is not the orchestrator's.
	if _, err := d.Client.RecordDecision(ctx, api.RecordDecision{
		ID: "member-one-way", Status: questions.DecisionDecided, Statement: "Store money as cents.", Rationale: "Rounding is a bug.",
		Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
	}); err != nil {
		t.Fatal(err)
	}

	reads := &eventReads{}
	var items []api.NeedItem
	err := d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) (err error) {
		items, err = server.OneWayReviewNeeds(countingReadTx{ReadTx: tx, reads: reads})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != waiting {
		t.Fatalf("the review holds %d decisions, want the orchestrator's %d", len(items), waiting)
	}
	for _, item := range items {
		if item.OneWayReview == nil || item.Since == 0 || item.OneWayReview.DecidedByName != "solo" {
			t.Errorf("the review item %+v", item)
		}
	}
	if got := reads.about.Load(); got != 0 {
		t.Errorf("the review read the events of %d decisions one decision at a time, want none", got)
	}
	if got := reads.ofType.Load(); got != 1 {
		t.Errorf("the review read the recording events %d times, want once", got)
	}
}
