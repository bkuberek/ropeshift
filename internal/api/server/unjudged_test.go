package server_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// A question no orchestrator took within its window reaches Needs you from the core, saying no orchestrator judged
// it and holding its ladder trace, and the tick that tells the channels has a notice for it (FR-QD-3).
func TestAQuestionNoOrchestratorTookReachesNeedsYou(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	asked, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
		ID: "question-1", Text: "Which ID scheme do the work orders take?", Categories: []string{questions.CategoryProduct},
		Blocking: true, Lane: "lane-asking", Generation: 1,
	}, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	w.Deliver()
	var item *api.QuestionItem
	var since int64
	for _, need := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if need.Kind != api.NeedQuestions {
			continue
		}
		for i := range need.Batch.Items {
			if need.Batch.Items[i].ID == asked.QuestionID {
				item, since = &need.Batch.Items[i], need.Since
			}
		}
	}
	if item == nil {
		t.Fatal("Needs you holds no item for the question no orchestrator took")
	}
	if !item.NoOrchestrator || len(item.LadderTrace) != 4 || item.EscalationReason != questions.ReasonScopeChange {
		t.Errorf("the item reads %+v", item)
	}
	notices := waitingNotices(t, d)
	if !slices.ContainsFunc(notices, func(n ledger.Notice) bool { return n.Kind == ledger.NoticeQuestions && n.Since == since }) {
		t.Errorf("no notice tells of the question: %+v", notices)
	}
}
