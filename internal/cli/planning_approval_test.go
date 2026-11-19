package cli_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/planning"
)

// What a plan version's request says of who asked and of each draft decision came from a lane or a plan file, so
// `plan show` prints each as one field of one line, with no line break, escape sequence, or control character.
func TestPlanShowPrintsTheApprovalTextAsPlainLines(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	approval.RequestedBy = "lane:lane-1\napproval\tforged\x1b[31m red"
	p.on("GET /v1/plans/{id}", func(*http.Request, []byte) (any, *api.Error) {
		return api.PlanView{
			Plan: planning.Plan{ID: "p1", Title: "Adopt shop", Status: "active"},
			Approval: &api.PlanApprovalView{
				Approval: approval, Detail: api.PlanDetailOf(approval),
				Drafts: []planning.DraftDecision{{ID: "d1", Question: "Which\nclient?\x1b[2J", Answer: "this\none\x07"}},
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"plan", "show", "p1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"\trequested by lane:lane-1 approval forged red\n", "  draft decision d1: Which client? -> this one\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan show lacks %q:\n%q", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("plan show prints a control character:\n%q", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "approval\tforged") || line == "client? -> this" {
			t.Errorf("a field made a line of its own: %q", line)
		}
	}
}
