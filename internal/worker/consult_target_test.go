package worker_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-ORCH-9: a consultation is named by its target, which a decision cites and which names the hold on its question, so
// a target that began a consultation of one question names no consultation of another. The call that began it, sent
// again for its own question, is still the same call.
func TestAConsultationTargetUsedOnOneQuestionIsRefusedOnAnother(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	r.asked(t, "q2", "Which log level?")
	first := r.grants(t, "q1", "c-shared")
	_, err := r.plane.Orchestrate(t.Context(), ledger.VerbConsult, r.consultCall(t, "q2", "c-shared"))
	if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "q1") {
		t.Fatalf("a target of one question's consultation, sent for another: %v", err)
	}
	if row := r.question(t, "q2"); row.ClaimedBy != "" || r.startsOf(t, "q2") != 0 {
		t.Errorf("the refused call held or recorded something of q2: %+v", row)
	}
	if again := r.grants(t, "q1", "c-shared"); again.ID != first.ID || again.Question != "q1" {
		t.Errorf("the call sent again for its own question answers %+v", again)
	}
	if other := r.grants(t, "q2", "c-other"); other.Question != "q2" || other.ID != "c-other" {
		t.Errorf("a target of its own answers %+v", other)
	}
}
