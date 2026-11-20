package worker_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// TestAFileALaneLeftOutIsTheOrchestratorsQuestion asks the orchestrator, in a category it may decide, whether the
// path is part of the project, and never a member.
func TestAFileALaneLeftOutIsTheOrchestratorsQuestion(t *testing.T) {
	t.Parallel()
	r := runOwning(t)
	var rows []questions.Question
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var asked []questions.Question
	for _, row := range rows {
		if slices.Contains(row.Categories, questions.CategoryFiles) {
			asked = append(asked, row)
		}
	}
	if len(asked) != 1 || !strings.Contains(asked[0].Text, "`.atl/`") || !strings.Contains(asked[0].Text, "repo-1") || asked[0].Status != questions.StatusOpen || asked[0].Blocking {
		t.Fatalf("questions %+v", asked)
	}
	if !questions.DefaultPolicy().MayDecide(asked[0]) {
		t.Errorf("the decision policy keeps %+v from the orchestrator", asked[0])
	}
}
