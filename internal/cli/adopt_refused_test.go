package cli

import (
	"bytes"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

// The reason a registration was refused comes from the API, so a control sequence in it never reaches the terminal.
func TestRefusedPlanningPrintsTheReasonAsPlainText(t *testing.T) {
	var stderr bytes.Buffer
	c := &invocation{name: "adopt", stderr: &stderr}
	plan := planning.AdoptionPlan{Planning: planning.PlanningRepository{Name: "design"}}

	code := c.refusedPlanning(plan, "held by \x1b[31mother\x1b[0m\x07\u202e project")

	if code != ExitUsage {
		t.Errorf("exit %d, want %d", code, ExitUsage)
	}
	got := stderr.String()
	if strings.ContainsAny(got, "\x1b\x07\u202e") {
		t.Errorf("the refusal carries a control sequence: %q", got)
	}
	if !strings.Contains(got, "was refused: held by other project;") {
		t.Errorf("the refusal reads %q, want its reason as plain text", got)
	}
}
