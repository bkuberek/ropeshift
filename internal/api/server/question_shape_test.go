package server_test

import (
	"maps"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// A lane's question keeps its shape from the ask to the member: the ledger stores its title, what each option trades
// off, and what the answer unblocks, and Needs you shows them on the question's card.
func TestALanesQuestionReachesNeedsYouInItsShape(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	tradeoffs := map[string]string{"Twice": "It may give up early.", "Until the deadline": "It holds a lane longer."}
	asked, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
		ID: "question-shape", Text: "How often does a call to the forge retry before it gives up?", Title: "Forge retries",
		Categories: []string{questions.CategoryProduct}, Blocking: true, Lane: "lane-asking", Generation: 1,
		Options: []string{"Twice", "Until the deadline"}, Recommendation: "Twice", RecommendationRationale: "It fits the deadline.",
		Tradeoffs: tradeoffs, Unblocks: "The forge client's retry policy.",
	}, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	w.Deliver()
	var item *api.QuestionItem
	for _, need := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if need.Kind != api.NeedQuestions {
			continue
		}
		for i := range need.Batch.Items {
			if need.Batch.Items[i].ID == asked.QuestionID {
				item = &need.Batch.Items[i]
			}
		}
	}
	if item == nil {
		t.Fatal("Needs you holds no item for the question")
	}
	if item.Title != "Forge retries" || !maps.Equal(item.Tradeoffs, tradeoffs) || item.Unblocks != "The forge client's retry policy." {
		t.Errorf("the item reads %+v", item.Item)
	}
}
