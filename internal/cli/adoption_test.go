package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
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

// A consolidation's request says what approving does beyond the plan (FR-ADOPT-13): the merge that imported the
// history, every path both repositories held with the version kept, the trees left out, the old design repository
// it removes, and at person_merges that only a merge commit keeps the history.
func TestPlanShowListsAConsolidation(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	consolidated := anAdoption
	consolidated.Consolidation = &planning.Consolidation{
		Design: "design", Merge: "1111111111111111111111111111111111111111", DesignTip: "2222222222222222222222222222222222222222",
		Collisions: []planning.Collision{{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "README.md", Kept: planning.KeptWorkspace}},
		LeftOut:    []string{"poc"}, Removes: &planning.RemovedRepository{ID: "design-root", Name: "design"}, PersonMerges: true,
	}
	detail, err := json.Marshal(planning.PlanDetail{Title: "Adopt shop", Ref: "shop", Adoption: &consolidated})
	if err != nil {
		t.Fatal(err)
	}
	approval.Detail = string(detail)
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
	for _, want := range []string{
		"  the adoption imports the history of design in merge commit 1111111111111111111111111111111111111111, whose second parent 2222222222222222222222222222222222222222 is the design repository's tip\n",
		"  the paths both repositories held, with the version kept:\n    .gitignore\tthe lines of both\n    README.md\tthe workspace's version\n",
		"  the design repository's code trees left out:\n    poc\n",
		"  approving removes repository design from the project, and keeps its record as history\n",
		"  at person_merges you merge this branch yourself: merge it with a merge commit. " + planning.MergeCommitKeepsHistory + "\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("plan show lacks %q:\n%s", want, stdout.String())
		}
	}
	// A consolidation whose merge could not be read says why, and what approving removes all the same.
	consolidated.Consolidation = &planning.Consolidation{Design: "design", LeftOut: []string{}, Unread: "git could not read the adoption branch", Removes: &planning.RemovedRepository{ID: "design-root"}}
	if detail, err = json.Marshal(planning.PlanDetail{Title: "Adopt shop", Adoption: &consolidated}); err != nil {
		t.Fatal(err)
	}
	approval.Detail = string(detail)
	stdout.Reset()
	if code := cli.Run([]string{"plan", "show", "p1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{
		"  the adoption imports the history of design, and its merge could not be read: git could not read the adoption branch\n",
		"  the design repository's code trees left out:\n    nothing\n",
		"  approving removes repository design-root from the project, and keeps its record as history\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("plan show lacks %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "person_merges") || strings.Contains(stdout.String(), "paths both repositories held") {
		t.Errorf("an unread consolidation at another level reads:\n%s", stdout.String())
	}
}

// A consolidation whose record under the design repository's first commit nothing shows is the imported clone says
// approving removes nothing and why, and a list of colliding paths that was cut short says so.
func TestPlanShowSaysWhyAConsolidationRemovesNothing(t *testing.T) {
	p := newPlane(t)
	approval := adoptedApproval(t)
	consolidated := anAdoption
	consolidated.Consolidation = &planning.Consolidation{
		Design: "design", Merge: "1111111111111111111111111111111111111111", DesignTip: "2222222222222222222222222222222222222222",
		Collisions: []planning.Collision{{Path: "README.md", Kept: planning.KeptWorkspace}}, CollisionsTruncated: true, LeftOut: []string{},
		RemovalUnread: "nothing shows repository starter is the clone the adoption imported",
	}
	detail, err := json.Marshal(planning.PlanDetail{Title: "Adopt shop", Adoption: &consolidated})
	if err != nil {
		t.Fatal(err)
	}
	approval.Detail = string(detail)
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
	for _, want := range []string{
		"    README.md\tthe workspace's version\n    and more, which this list leaves out\n",
		"  approving removes no repository: nothing shows repository starter is the clone the adoption imported\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("plan show lacks %q:\n%s", want, stdout.String())
		}
	}
}

// The reason a registration was refused is the server's text, so a control sequence in it never reaches the terminal.
func TestAdoptSaysWhyTheRepositoryItRunsInWasNotRegisteredWithNoControlSequence(t *testing.T) {
	p := newPlane(t)
	const root = "/work/shop"
	p.on("POST "+api.AdoptionPlanPath, func(*http.Request, []byte) (any, *api.Error) {
		return planning.AdoptionPlan{
			Layout:       planning.DetectedLayout{Root: root},
			Planning:     planning.PlanningRepository{Name: "shop", Path: root},
			Repositories: []planning.PlannedRepository{{Name: "shop", Path: root}},
		}, nil
	})
	p.on("POST "+api.ProjectsPath, func(*http.Request, []byte) (any, *api.Error) {
		return api.Project{Name: "shop", State: ledger.ProjectActive}, nil
	})
	p.on("POST /v1/projects/shop/repositories", func(*http.Request, []byte) (any, *api.Error) {
		return api.RepositoryAddReport{Repositories: []api.RepositoryAdded{{Path: root, Outcome: ledger.RegistrationRefused, Reason: "\x1b[31mrefused\x1b]0;title\x07 for \u202etxt"}}}, nil
	})
	var stdout, stderr bytes.Buffer
	opts := cli.Options{
		Socket: p.Socket, RepositoryRoot: func(context.Context) (string, error) { return root, nil },
		RepositoryHere: func(context.Context) (string, error) { return "identity", nil },
	}
	if code := cli.Run([]string{"adopt", "--project", "shop", "--no-attach"}, &stdout, &stderr, opts); code != cli.ExitConflict {
		t.Fatalf("exit %d: %s (%v)", code, stderr.String(), p.reads())
	}
	if want := "this repository was not registered: refused for txt\n"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr is %q, want it to hold %q", stderr.String(), want)
	}
}
