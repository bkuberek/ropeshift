package server_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// A question in Needs you names who asked it, and the lane when a lane did, so its card can open a conversation with
// that lane and the list can group it with the lane's other requests. A question that blocks its lane holds a running
// session, so it comes first.
func TestAQuestionInNeedsYouNamesTheLaneThatAskedIt(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-asking", "Add per-client rate limits")
	ask := func(id string, actor ledger.Actor) {
		t.Helper()
		if _, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
			ID: id, Text: "Which limit applies to " + id + "?", Categories: []string{questions.CategoryProduct},
			Blocking: true, Lane: lane.ID, Generation: 1,
		}, actor); err != nil {
			t.Fatal(err)
		}
	}
	ask("question-lane", ledger.Actor{Kind: ledger.ActorLane, ID: lane.ID})
	ask("question-member", ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	time.Sleep(5 * time.Millisecond)
	w.Deliver()
	found := map[string]api.QuestionItem{}
	for _, need := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if need.Kind != api.NeedQuestions {
			continue
		}
		if need.Tier != api.TierRunningSession {
			t.Errorf("a batch of blocking questions is in tier %d", need.Tier)
		}
		for _, item := range need.Batch.Items {
			found[item.ID] = item
		}
	}
	if got := found["question-lane"]; got.AskedBy != lane.ID || got.Lane != lane.ID {
		t.Errorf("the lane's question was asked by %q, lane %q", got.AskedBy, got.Lane)
	}
	if got := found["question-member"]; got.AskedBy != "member-1" || got.Lane != "" {
		t.Errorf("the member's question was asked by %q, lane %q", got.AskedBy, got.Lane)
	}
}
