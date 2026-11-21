package control

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// TestAConsultationsCostLineNamesItsAgentClassAndApproval is FR-ORCH-9: what reaches the orchestrator says which
// agent ran, its class, that its approval was per use, the model, and the cost.
func TestAConsultationsCostLineNamesItsAgentClassAndApproval(t *testing.T) {
	end := ledger.ConsultationEnd{
		ID: "c1", Question: "q1", Agent: "consultant", Class: "frontier", Approval: "per_use", Model: "m", CostUSD: 0.4,
		Outcome: ledger.ConsultationAtCap,
	}
	if text := consultationText(end); !strings.Contains(text, "by consultant (frontier, approval per_use, m, $0.40)") {
		t.Errorf("the cost line reads %q", text)
	}
}
