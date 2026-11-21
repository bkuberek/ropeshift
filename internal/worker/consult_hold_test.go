package worker_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/worker"
)

// holdLeaving moves database time behind this machine's, so a hold the ledger grants next ends this long after the
// worker receives it.
func holdLeaving(r *toolRig, t *testing.T, left time.Duration) {
	t.Helper()
	r.shiftDatabase(t, -(ledger.ConsultHold - left).Milliseconds())
}

// holdMargin is how long before the ledger's hold on a question ends a worker stops its consultation's session: the margin
// that lets the end reach the ledger while the consultation still holds the question, and the skew that this machine's
// clock may have against the ledger's, which the hold's end is counted on.
const holdMargin = worker.ConsultHoldMargin + worker.ConsultClockSkew

// FR-ORCH-9: a consultation's session never outlasts the ledger's hold on its question, so nobody decides the question
// while the session still reads it. The worker stops the session a margin before the hold ends, and a further margin
// for a clock that runs behind the ledger's, wherever the session has got to, and when the hold leaves it less than
// both, it starts none.
func TestAConsultationEndsBeforeTheHoldOnItsQuestionDoes(t *testing.T) {
	t.Parallel()
	t.Run("a session still running when the margin comes is stopped", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		holdLeaving(r, t, holdMargin+2*time.Second)
		r.nextSession([]fake.DriverStep{{Bound: true}, {Hold: true}})
		r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		ended := r.awaitConsulted(t, "q1")
		if ended.Outcome != ledger.ConsultationUnreported || !strings.Contains(ended.Detail, "hold") {
			t.Errorf("the consultation ended %+v", ended)
		}
	})
	t.Run("a hold that leaves less than the margin starts no session", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		holdLeaving(r, t, worker.ConsultHoldMargin-time.Second)
		r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.1)})
		r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		ended := r.awaitConsulted(t, "q1")
		if ended.Outcome != ledger.ConsultationFailed || !strings.Contains(ended.Detail, "hold") || ended.Recommendation != "" {
			t.Errorf("the consultation ended %+v", ended)
		}
		if specs := r.consultSpecs(); len(specs) != 0 {
			t.Errorf("a hold with no time left started %d sessions", len(specs))
		}
	})
	t.Run("a hold that leaves less than the margin and the clock skew starts no session", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		holdLeaving(r, t, holdMargin-time.Second)
		r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.1)})
		r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		ended := r.awaitConsulted(t, "q1")
		if ended.Outcome != ledger.ConsultationFailed || !strings.Contains(ended.Detail, "hold") || ended.Recommendation != "" {
			t.Errorf("the consultation ended %+v", ended)
		}
		if specs := r.consultSpecs(); len(specs) != 0 {
			t.Errorf("a hold that leaves %s started %d sessions", holdMargin-time.Second, len(specs))
		}
	})
}
