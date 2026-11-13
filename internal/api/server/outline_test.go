package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// atCommit reads a source as the branch's tip says: what a poll of a repository names as its cursor.
type atCommit struct {
	*fake.WorkSource
	commit string
}

func (a atCommit) Poll(ctx context.Context, cursor ports.Cursor) (ports.ChangeBatch, error) {
	batch, err := a.WorkSource.Poll(ctx, cursor)
	batch.Cursor = ports.Cursor(a.commit)
	return batch, err
}

const (
	outlinePlan = "plan-1"
	outlineLane = "lane-" + outlinePlan
	// laneBranch is where the lane under test commits, by the prefix its profile names.
	laneBranch = "lane/" + outlineLane
)

const outlineMilestone = "+++\nkind = \"milestone\"\nid = \"m4\"\ntitle = \"Saved searches v2\"\nplan = \"saved\"\npriority = 4\n+++\n\nWhat ships.\n"

const outlinePlanFile = `+++
kind = "plan"
id = "saved"
title = "Saved searches"

[[decision]]
id = "dd-10"
question = "Whose are they?"
answer = "One member's"
+++

How.
`

const outlineContract = `+++
kind = "contract"
id = "storage"
title = "Saved search storage"
plan = "saved"
milestone = "m4"

[[criteria]]
id = "c1"
text = "A search is kept"

[[criteria]]
id = "c2"
text = "A search is listed"

[[work_orders]]
id = "storage-order"
title = "Store searches"
size = "small"
+++

Storage.
`

// aPlanningSession is a daemon whose `plan` work order runs a lane that began at the base and committed
// documents to its branch: a requirements file changed, a milestone and a contract new.
func aPlanningSession(t *testing.T) (*apitest.Daemon, *apitest.World, string) {
	t.Helper()
	git := fake.NewWorktrees()
	base := git.Touch("/repo", "main", "docs/requirements.md")
	source := fake.NewWorkSource()
	git.Point("/repo", laneBranch, base)
	git.Touch("/repo", laneBranch, "docs/requirements.md")
	git.Touch("/repo", laneBranch, "plans/m4.md")
	git.Touch("/repo", laneBranch, "plans/storage.md")
	tip := git.Touch("/repo", laneBranch, "plans/saved.md")
	source.Write("docs/requirements.md", []byte("+++\nkind = \"requirements\"\nid = \"search\"\ntitle = \"Search\"\n+++\n\n"))
	source.Write("plans/m4.md", []byte(outlineMilestone))
	source.Write("plans/storage.md", []byte(outlineContract))
	source.Write("plans/saved.md", []byte(outlinePlanFile))
	source.Write("notes/unrelated.md", []byte("Not a planning document.\n"))
	opened := func(_ context.Context, _ ledger.WorkspaceID, repository, ref string) (ports.WorkSource, error) {
		if repository != "repository-1" || ref != laneBranch {
			return nil, errors.New("no such branch " + ref)
		}
		return atCommit{WorkSource: source, commit: tip}, nil
	}
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/repo", nil }}
	d := apitest.StartWith(t, apitest.Options{Starts: starts, Branches: opened, LaneBranchPrefix: "lane/"})
	w := apitest.NewWorld(t, d)
	if _, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{
		ID: outlinePlan, Kind: workorders.KindPlan, Title: "Saved searches", Statement: "Plan saved searches.", RepositoryID: "repository-1",
		Criteria: []planning.Criterion{{ID: "c1", Text: "The plan is written."}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Review(t.Context(), outlinePlan); err != nil {
		t.Fatal(err)
	}
	w.Run(outlinePlan)
	return d, w, tip
}

// askedBy records a question the planning session's lane raised, which belongs to its work order.
func askedBy(t *testing.T, w *apitest.World, id, text string, blocking bool) {
	t.Helper()
	_, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
		ID: id, Lane: outlineLane, Generation: 1, Text: text, Blocking: blocking,
		SubjectKind: workorders.SubjectKind, SubjectID: outlinePlan,
	}, ledger.Actor{Kind: ledger.ActorLane, ID: outlineLane})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPlanningLaneOutlineShowsWhatItsBranchHolds(t *testing.T) {
	d, _, tip := aPlanningSession(t)
	outline, err := d.Client.Outline(t.Context(), outlineLane)
	if err != nil {
		t.Fatal(err)
	}
	if outline.Unread != "" || outline.Commit != tip {
		t.Fatalf("outline %+v", outline)
	}
	paths := map[string]string{}
	for _, document := range outline.Documents {
		paths[document.Path] = document.Status + " " + document.Kind
	}
	want := map[string]string{
		"docs/requirements.md": "changed requirements", "plans/m4.md": "new milestone", "plans/saved.md": "new plan", "plans/storage.md": "new contract",
	}
	if len(paths) != len(want) {
		t.Errorf("documents %v, want %v", paths, want)
	}
	for path, kind := range want {
		if paths[path] != kind {
			t.Errorf("%s is %q, want %q", path, paths[path], kind)
		}
	}
	if len(outline.Milestones) != 1 || outline.Milestones[0] != (api.OutlineMilestone{ID: "m4", Title: "Saved searches v2", Priority: 4}) {
		t.Errorf("milestones %+v", outline.Milestones)
	}
	if len(outline.Contracts) != 1 || outline.Contracts[0] != (api.OutlineContract{ID: "storage", Title: "Saved search storage", Criteria: 2, WorkOrders: 1}) {
		t.Errorf("contracts %+v", outline.Contracts)
	}
	if len(outline.Decisions) != 1 || outline.Decisions[0].ID != "dd-10" || outline.Decisions[0].Answer != "One member's" {
		t.Errorf("decisions %+v", outline.Decisions)
	}
	if len(outline.Questions) != 0 || outline.Approval != nil || outline.NotSubmitted != "" {
		t.Errorf("a session with no question and no request says %+v", outline)
	}
}

func TestAnOutlineNamesTheQuestionsThatHoldTheVersionBack(t *testing.T) {
	d, w, _ := aPlanningSession(t)
	askedBy(t, w, "question-open", "Should alerts email?", false)
	asked, err := d.Client.Outline(t.Context(), outlineLane)
	if err != nil || len(asked.Questions) != 1 || asked.Questions[0].Blocking || asked.NotSubmitted != "" {
		t.Fatalf("an open question that blocks nothing: %+v, %v", asked, err)
	}
	askedBy(t, w, "question-blocking", "Hourly or daily?", true)
	blocked, err := d.Client.Outline(t.Context(), outlineLane)
	if err != nil || len(blocked.Questions) != 2 || blocked.NotSubmitted == "" || blocked.Approval != nil {
		t.Fatalf("a blocking question: %+v, %v", blocked, err)
	}
}

func TestAnOutlineSaysARequestStandsForTheTip(t *testing.T) {
	d, w, tip := aPlanningSession(t)
	askedBy(t, w, "question-blocking", "Hourly or daily?", true)
	raise := func(id, commit string, state authority.ApprovalState) {
		t.Helper()
		request := authority.Approval{
			ID: id, Kind: authority.KindPlan, WorkOrder: outlinePlan, Subject: "saved@1", SubjectDigest: "sha256:plan", Summary: "Saved searches",
			RequestedBy: "system:intake", State: state, PlanVersion: 1, Detail: `{"title":"Saved searches","source":"` + commit + `"}`,
		}
		err := d.Store.Write(t.Context(), apitest.Scope, "test.approval", func(tx ports.WriteTx) error {
			if _, err := tx.Approvals().Insert(request); err != nil {
				return err
			}
			event, err := authority.ApprovalEvent(request, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
			if err != nil {
				return err
			}
			_, err = tx.Append(event)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	raise("approval-old", "an-earlier-commit", authority.ApprovalPending)
	older, err := d.Client.Outline(t.Context(), outlineLane)
	if err != nil || older.Approval != nil || older.NotSubmitted == "" {
		t.Fatalf("a request for an earlier commit does not stand for the tip: %+v, %v", older, err)
	}
	raise("approval-tip", tip, authority.ApprovalPending)
	standing, err := d.Client.Outline(t.Context(), outlineLane)
	if err != nil {
		t.Fatal(err)
	}
	if standing.Approval == nil || standing.Approval.ID != "approval-tip" || standing.Approval.State != authority.ApprovalPending || standing.Approval.Version != 1 || standing.NotSubmitted != "" {
		t.Errorf("outline %+v", standing)
	}
}

func withMain() *fake.Worktrees {
	git := fake.NewWorktrees()
	git.Grow("/repo", "main")
	return git
}

func TestAnOutlineOfABranchItCannotReadSaysWhyAndNothingElse(t *testing.T) {
	tests := []struct {
		name   string
		config apitest.Options
		reason string
	}{
		{"a control plane that reads no repository", apitest.Options{}, "this control plane reads no repository's history"},
		{"a branch the checkout does not hold", apitest.Options{
			Starts: &control.Starts{Bases: withMain(), Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/repo", nil }},
			Branches: func(context.Context, ledger.WorkspaceID, string, string) (ports.WorkSource, error) {
				return nil, errors.New("branch lane/lane-plan-1 is not in this machine's checkout")
			},
		}, "branch lane/lane-plan-1 is not in this machine's checkout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := apitest.StartWith(t, tt.config)
			w := apitest.NewWorld(t, d)
			if _, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{
				ID: outlinePlan, Kind: workorders.KindPlan, Title: "Saved searches", Statement: "Plan.", RepositoryID: "repository-1",
				Criteria: []planning.Criterion{{ID: "c1", Text: "The plan is written."}},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Client.Review(t.Context(), outlinePlan); err != nil {
				t.Fatal(err)
			}
			w.Run(outlinePlan)
			askedBy(t, w, "question-blocking", "Hourly or daily?", true)
			outline, err := d.Client.Outline(t.Context(), outlineLane)
			if err != nil {
				t.Fatal(err)
			}
			if outline.Unread != tt.reason {
				t.Errorf("unread %q, want %q", outline.Unread, tt.reason)
			}
			if outline.Commit != "" || len(outline.Documents)+len(outline.Milestones)+len(outline.Contracts)+len(outline.Decisions)+len(outline.Questions) != 0 || outline.NotSubmitted != "" {
				t.Errorf("an unread branch carries content: %+v", outline)
			}
		})
	}
}

func TestAnOutlineOfALaneThatPlansNothingIsRefused(t *testing.T) {
	d, w, _ := aWorld(t)
	lane := w.Running("wo-change", "Add the budget")
	var outline api.PlanOutline
	err := d.Client.Do(t.Context(), http.MethodGet, d.Client.Route("/v1/lanes/"+lane.ID+"/outline"), nil, &outline)
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	err = d.Client.Do(t.Context(), http.MethodGet, d.Client.Route("/v1/lanes/no-such-lane/outline"), nil, &outline)
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}
