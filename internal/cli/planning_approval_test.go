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

// The records a version differs in are named by refs a plan file wrote, so `plan show` prints each ref, each draft
// decision's ID, and each part a contract differs in as plain text too.
func TestPlanShowPrintsTheRefsAndIDsOfAnApprovalAsPlainLines(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	detail := api.PlanDetailOf(approval)
	detail.Difference = planning.Difference{
		Base: 1, Added: []string{"plan:a\nb"}, Changed: []string{"x\x1b[2Jy"}, Removed: []string{"z\x07"},
		Contracts: []planning.ContractDifference{{
			Ref:        "contract:c\nforged",
			Criteria:   planning.PartDifference{Added: []string{"crit\none"}},
			WorkOrders: planning.PartDifference{Changed: []string{"wo\x1b[31mx"}, Removed: []string{"w\to"}},
		}},
	}
	p.on("GET /v1/plans/{id}", func(*http.Request, []byte) (any, *api.Error) {
		return api.PlanView{
			Plan: planning.Plan{ID: "p1", Title: "Adopt shop", Status: "active"},
			Approval: &api.PlanApprovalView{
				Approval: approval, Detail: detail, Drafts: []planning.DraftDecision{{ID: "d\n1", Question: "q", Answer: "a"}},
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"plan", "show", "p1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"\n  added: plan:a b\n", "\n  changed: xy\n", "\n  removed: z\n",
		"\n  contract:c forged: criteria +crit one; work orders ~wox -w o\n", "\n  draft decision d 1: q -> a\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan show lacks %q:\n%q", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("plan show prints a control character:\n%q", out)
	}
}

// A decision's ID and statement came from a plan file or a member, so both revision views print them as plain text: the
// plan's own revision, and the revision an approval is read beside.
func TestPlanShowPrintsTheDecisionsOfARevisionAsPlainLines(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	revision := &api.PlanRevisionView{
		Plan: "p1", Title: "Adopt shop", Revision: &planning.Revision{ApprovedVersion: 1},
		Decisions: []api.RevisionDecision{{ID: "d\n1", Statement: "use\x1b[2J this\none\x07", Scope: "plan", Reversibility: "two_way"}},
	}
	p.on("GET /v1/plans/{id}", func(*http.Request, []byte) (any, *api.Error) {
		return api.PlanView{
			Plan:     planning.Plan{ID: "p1", Title: "Adopt shop", Status: "active"},
			Revision: revision,
			Approval: &api.PlanApprovalView{Approval: approval, Detail: api.PlanDetailOf(approval), Revision: revision},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"plan", "show", "p1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"\n  decision d 1 (two_way, plan): use this one\n", "\n    decision d 1 (two_way, plan): use this one\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan show lacks %q:\n%q", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("plan show prints a control character:\n%q", out)
	}
}
