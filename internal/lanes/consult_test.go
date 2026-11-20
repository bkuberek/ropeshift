package lanes_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// FR-ORCH-9: the role says to consult on a decision the orchestrator cannot settle with confidence before it
// escalates, that the answer comes on a later wake, and that the decision cites the consultation.
func TestTheRoleConsultsOnAHardDecisionBeforeItEscalates(t *testing.T) {
	for _, want := range []string{
		lanes.ToolConsult, "cannot settle a question with confidence", "before you escalate", "consultation:<id>", "end your turn",
	} {
		if !strings.Contains(lanes.OrchestratorRole, want) {
			t.Errorf("the role does not say %q", want)
		}
	}
}
