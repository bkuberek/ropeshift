package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/planning"
)

// anAdoption is what an adopt lane's plan holds, grouped as its approval shows it.
var anAdoption = planning.Adoption{
	Exists: []planning.ExistingRecord{
		{Ref: "requirement:SHOP-1", Title: "One client", Evidence: []string{"shop:client.go", "shop:client_test.go"}},
		{Ref: "contract:client", Title: "Payments client", Cites: []string{"requirement:SHOP-1"}},
	},
	Continues: []planning.ContinuedWork{{Contract: "contract:retry", WorkOrder: "retry-finish", Title: "Finish the retry branch", Repo: ".", Branch: "feature/retry"}},
	New:       []planning.NewRecord{{Ref: "contract:retry", Title: "Retry failed calls", WorkOrders: []string{"retry-docs"}}},
	LeftOut:   []planning.LeftOut{{Ref: "branch:spike/old-ui", Reason: "An abandoned experiment"}},
	Changes: []planning.FileChange{
		{Status: planning.ChangeChanged, Path: "docs/requirements.md"},
		{Status: planning.ChangeMoved, Path: "docs/payments.md", From: "notes/payments.md"},
		{Status: planning.ChangeAdded, Path: "plans/plan.md"},
	},
}

// adoptedApproval is the request to approve an adopted plan that nothing has read yet.
func adoptedApproval(t *testing.T) authority.Approval {
	t.Helper()
	detail, err := json.Marshal(planning.PlanDetail{Title: "Adopt shop", Ref: "shop", Adoption: &anAdoption, Difference: planning.Difference{Added: []string{"plan:shop"}}})
	if err != nil {
		t.Fatal(err)
	}
	return authority.Approval{
		ID: "approval-1", Kind: authority.KindPlan, WorkOrder: "adopt-1", Subject: "shop@1", SubjectDigest: "sha256:abc", Summary: "Plan Adopt shop, version 1",
		RequestedBy: "lane:lane-1", State: authority.ApprovalPending, PlanVersion: 1, Detail: string(detail),
	}
}

// wantAdoptionText are the lines a member reads of an adopted plan before approving it (ADR-0023): every
// claim with its evidence, each continued branch, what is new, what is left out with its reason, and each
// file the adoption branch changed.
var wantAdoptionText = []string{
	"what exists, by the plan's claim:\n",
	"  requirement:SHOP-1\tOne client\tevidence shop:client.go, shop:client_test.go\n",
	"  contract:client\tPayments client\trests on requirement:SHOP-1\n",
	"what continues:\n",
	"  contract:retry\tretry-finish\tFinish the retry branch\tcontinues feature/retry in .\n",
	"what is new:\n",
	"  contract:retry\tRetry failed calls\twork orders retry-docs\n",
	"what is left out:\n",
	"  branch:spike/old-ui\tAn abandoned experiment\n",
	"the adoption branch changes 3 files:\n",
	"  changed\tdocs/requirements.md\n",
	"  moved\tnotes/payments.md -> docs/payments.md\n",
	"  added\tplans/plan.md\n",
}

func TestApprovalShowGroupsAnAdoptedPlan(t *testing.T) {
	p := newPlane(t)
	p.on("GET /v1/approvals/{id}", func(*http.Request, []byte) (any, *api.Error) { return adoptedApproval(t), nil })
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"approval", "show", "approval-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range wantAdoptionText {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("approval show lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestPlanShowGroupsAnAdoptedPlan(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	p.on("GET /v1/plans/{id}", func(*http.Request, []byte) (any, *api.Error) {
		return api.PlanView{
			Plan:     planning.Plan{ID: "p1", Title: "Adopt shop", Status: "active"},
			Approval: &api.PlanApprovalView{Approval: approval, Detail: api.PlanDetailOf(approval)},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"plan", "show", "p1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range wantAdoptionText {
		if !strings.Contains(stdout.String(), "  "+want) {
			t.Errorf("plan show lacks %q:\n%s", "  "+want, stdout.String())
		}
	}
}

// A group with nothing in it says so, so a member does not take it for one the view left out.
func TestAnEmptyGroupSaysSo(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	detail, err := json.Marshal(planning.PlanDetail{Title: "Adopt shop", Adoption: &planning.Adoption{ChangesUnread: "the lane recorded no commit its branch began at"}})
	if err != nil {
		t.Fatal(err)
	}
	approval.Detail = string(detail)
	p.on("GET /v1/approvals/{id}", func(*http.Request, []byte) (any, *api.Error) { return approval, nil })
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"approval", "show", "approval-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{"what exists, by the plan's claim:\n  nothing\n", "what is left out:\n  nothing\n", "the adoption branch's changes could not be read: the lane recorded no commit its branch began at\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("approval show lacks %q:\n%s", want, stdout.String())
		}
	}
}
