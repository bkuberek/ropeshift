package server_test

import (
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// aDeliveredBatch escalates one question for each ID and delivers them as one batch, which it returns as a member
// reads it.
func aDeliveredBatch(t *testing.T, ids ...string) (*apitest.Daemon, api.QuestionBatch) {
	t.Helper()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	for _, id := range ids {
		escalateAbout(t, w, questions.Ask{ID: id, Text: "Is " + id + " settled?"})
	}
	clock.Advance(time.Minute)
	w.Deliver()
	batches, err := d.Client.Batches(t.Context(), "")
	if err != nil || len(batches) != 1 || len(batches[0].Items) != len(ids) {
		t.Fatalf("batches %+v: %v", batches, err)
	}
	return d, batches[0]
}

// Every read of a question states what an answer to it will be, by the rule a batch's item states it by: a category
// the decision policy keeps from an orchestrator makes the answer one-way, whatever the question's reason, so no
// surface chooses it for a member.
func TestEveryReadOfAQuestionStatesWhatItsAnswerWillBe(t *testing.T) {
	t.Parallel()
	d, _, _ := aWorld(t)
	want := map[string]string{"question-1": questions.Reversible, "question-2": questions.OneWay}
	listed := map[string]string{}
	rows, err := d.Client.Questions(t.Context(), api.RecordFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		listed[row.ID] = row.Reversibility
	}
	if !maps.Equal(listed, want) {
		t.Errorf("the list states %v, want %v", listed, want)
	}
	for id, reversibility := range want {
		if row, err := d.Client.Question(t.Context(), id); err != nil || row.Reversibility != reversibility {
			t.Errorf("%s reads %q, want %q: %v", id, row.Reversibility, reversibility, err)
		}
	}
	batched := map[string]string{}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Batch != nil {
			for _, question := range item.Batch.Items {
				batched[question.ID] = question.Reversibility
			}
		}
	}
	if !maps.Equal(batched, want) {
		t.Errorf("the batch states %v, want %v", batched, want)
	}
}

func TestABatchIsAnsweredOnceThroughItsRoute(t *testing.T) {
	t.Parallel()
	d, batch := aDeliveredBatch(t, "q-retries", "q-currency")
	for _, item := range batch.Items {
		if item.Reversibility != questions.Reversible || item.RecommendationRationale != "It is the common case." {
			t.Errorf("item %s states reversibility %q and rationale %q", item.ID, item.Reversibility, item.RecommendationRationale)
		}
	}
	decisions, err := d.Client.AnswerBatch(t.Context(), batch.Batch.ID, api.AnswerBatch{Answers: []api.BatchAnswerItem{
		{Question: "q-retries", ID: "d-retries", Accepted: true},
		{Question: "q-currency", ID: "d-currency", Answer: "The original currency.", Rationale: "Refunds go back as they were paid."},
	}})
	if err != nil || len(decisions) != 2 {
		t.Fatalf("decisions %+v: %v", decisions, err)
	}
	if got := decisions[0]; got.ID != "d-retries" || got.Statement != "Yes" || got.Rationale != "It is the common case." || got.QuestionID != "q-retries" {
		t.Errorf("the accepted recommendation became %+v", got)
	}
	if got := decisions[1]; got.Statement != "The original currency." || got.Rationale != "Refunds go back as they were paid." {
		t.Errorf("the member's own answer became %+v", got)
	}
	for _, id := range []string{"q-retries", "q-currency"} {
		if row, err := d.Client.Question(t.Context(), id); err != nil || row.Status != questions.StatusAnswered {
			t.Errorf("question %s is %q: %v", id, row.Status, err)
		}
	}
	if left, err := d.Client.Batches(t.Context(), ""); err != nil || len(left) != 0 {
		t.Errorf("an answered batch still waits: %+v: %v", left, err)
	}
}

func TestABatchWithOneRefusedAnswerRecordsNoneAndNamesIt(t *testing.T) {
	t.Parallel()
	d, batch := aDeliveredBatch(t, "q-retries", "q-currency")
	_, err := d.Client.AnswerBatch(t.Context(), batch.Batch.ID, api.AnswerBatch{Answers: []api.BatchAnswerItem{
		{Question: "q-retries", ID: "d-retries", Accepted: true},
		{Question: "q-currency", ID: "d-currency", Answer: "The original currency."},
	}})
	detail := refusal(t, err)
	if detail.Code != api.CodeInvalid || !strings.Contains(detail.Message, "q-currency") || !strings.Contains(detail.Message, "rationale") {
		t.Errorf("the refusal reads %+v, want one that names q-currency and its missing rationale", detail)
	}
	for _, id := range []string{"q-retries", "q-currency"} {
		if row, err := d.Client.Question(t.Context(), id); err != nil || row.Status != questions.StatusNeedsHuman {
			t.Errorf("question %s is %q after a refused request: %v", id, row.Status, err)
		}
	}
	if _, err := d.Client.AnswerBatch(t.Context(), "no-such-batch", api.AnswerBatch{Answers: []api.BatchAnswerItem{{Question: "q-retries", ID: "d-1", Accepted: true}}}); refusal(t, err).Code != api.CodeNotFound {
		t.Errorf("a batch nobody opened: %v", err)
	}
	if _, err := d.Client.AnswerBatch(t.Context(), batch.Batch.ID, api.AnswerBatch{}); refusal(t, err).Code != api.CodeInvalid {
		t.Errorf("a request with no answers: %v", err)
	}
}

// An answer that expects the reversibility its item stated is refused whole when a policy published since says
// otherwise, and records nothing.
func TestABatchAnswerThatExpectsAReversibilityThePolicyNoLongerGivesIsRefused(t *testing.T) {
	t.Parallel()
	d, batch := aDeliveredBatch(t, "q-retries", "q-currency")
	body := `[decision_policy]
version = 2
reversible_default = "assume"
one_way_categories = ["security", "compliance", "data", "product"]
max_assumptions_per_work_order = 5
`
	if _, err := d.Client.PublishPolicy(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	_, err := d.Client.AnswerBatch(t.Context(), batch.Batch.ID, api.AnswerBatch{Answers: []api.BatchAnswerItem{
		{Question: "q-retries", ID: "d-retries", Accepted: true},
		{Question: "q-currency", ID: "d-currency", Accepted: true, ExpectReversibility: questions.Reversible},
	}})
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeReversibilityChanged ||
		!strings.Contains(refused.Detail.Message, "answer 2, for question q-currency: ") || !strings.Contains(refused.Detail.Message, "it is one-way now") {
		t.Fatalf("got %v", err)
	}
	for _, id := range []string{"q-retries", "q-currency"} {
		if row, err := d.Client.Question(t.Context(), id); err != nil || row.Status != questions.StatusNeedsHuman {
			t.Errorf("question %s is %q after a refused request: %v", id, row.Status, err)
		}
	}
	if _, err := d.Client.AnswerBatch(t.Context(), batch.Batch.ID, api.AnswerBatch{Answers: []api.BatchAnswerItem{{Question: "q-currency", ID: "d-currency", Accepted: true, ExpectReversibility: "maybe"}}}); refusal(t, err).Code != api.CodeInvalid {
		t.Errorf("an expectation that is neither reversibility: %v", err)
	}
}

// Only a member answers a batch: a caller with no session, a worker's credential, and a delegate are each refused,
// and no question closes.
func TestOnlyAMemberAnswersABatch(t *testing.T) {
	t.Parallel()
	d, batch := aDeliveredBatch(t, "q-retries", "q-currency")
	body := api.AnswerBatch{Answers: []api.BatchAnswerItem{
		{Question: "q-retries", ID: "d-retries", Accepted: true}, {Question: "q-currency", ID: "d-currency", Accepted: true},
	}}
	path := apitest.Route("/v1/batches/" + batch.Batch.ID + "/answers")

	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	refusedWith(t, anonymous.Do(t.Context(), http.MethodPost, path, body, nil), http.StatusUnauthorized, api.CodeSessionRequired)

	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	refusedWith(t, delegate.Do(t.Context(), http.MethodPost, path, body, nil), http.StatusForbidden, api.CodeMemberOnly)

	for _, id := range []string{"q-retries", "q-currency"} {
		if row, err := d.Client.Question(t.Context(), id); err != nil || row.Status != questions.StatusNeedsHuman {
			t.Errorf("question %s is %q after refused callers: %v", id, row.Status, err)
		}
	}
}
