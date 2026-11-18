package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// A delegate that calls question.merge is refused as member-only, and both questions stay as they were.
func TestADelegatesMergeIsRefused(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	for _, id := range []string{"question-1", "question-2"} {
		ask := questions.Ask{ID: id, Text: "How many retries? " + id, Categories: []string{questions.CategoryProduct}, Lane: "lane-" + id, Generation: 1}
		if _, err := w.Questions.Ask(t.Context(), apitest.Scope, ask, ledger.Actor{Kind: ledger.ActorLane, ID: ask.Lane}); err != nil {
			t.Fatal(err)
		}
	}
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	err := delegate.MergeQuestion(t.Context(), "question-1", api.MergeQuestion{Into: "question-2"})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	row, err := d.Client.Question(t.Context(), "question-1")
	if err != nil || row.Status != questions.StatusOpen {
		t.Errorf("a refused merge left the question %+v: %v", row, err)
	}
}
