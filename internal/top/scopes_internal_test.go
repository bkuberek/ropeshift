package top

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/questions"
)

// A batch's line names what its answers bind, each scope once and in the batch's order, with the title of the
// record it names, so a person at a terminal knows what a batch is about before opening it.
func TestABatchLineNamesWhatItsAnswersBind(t *testing.T) {
	batch := func(items ...api.QuestionItem) api.NeedItem {
		return api.NeedItem{Kind: api.NeedQuestions, ID: "batch-1", Batch: &api.QuestionBatch{Items: items}}
	}
	item := func(id, scope, title string) api.QuestionItem {
		return api.QuestionItem{Item: questions.Item{ID: id, Scope: scope}, ScopeTitle: title}
	}
	tests := []struct {
		name string
		need api.NeedItem
		want string
	}{
		{"titled and alone", batch(item("q1", "plan:p1", "Checkout retries"), item("q2", "wo:w2", ""), item("q3", "plan:p1", "Checkout retries"), item("q4", "", "")),
			"4 to answer  plan:p1 · Checkout retries, wo:w2"},
		{"about nothing", batch(item("q1", "", "")), "1 to answer"},
		{"a title that says too much", batch(item("q1", "wo:w1", "Trim\nthe log")), "1 to answer  wo:w1 · Trim the log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if line := what(tt.need); line != tt.want {
				t.Errorf("line %q, want %q", line, tt.want)
			}
		})
	}
}
