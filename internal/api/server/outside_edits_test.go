package server_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// outsideWorld serves a daemon whose worker keeps a clone of each repository at /srv/<repository>, read through edits.
func outsideWorld(t *testing.T, edits *fake.CloneEdits, kept ...string) (*apitest.Daemon, *apitest.World) {
	t.Helper()
	return outsideWorldWith(t, apitest.Options{}, edits, kept...)
}

// outsideWorldWith is outsideWorld with options, such as a clock, and any reader of clones.
func outsideWorldWith(t *testing.T, options apitest.Options, edits ports.CloneEdits, kept ...string) (*apitest.Daemon, *apitest.World) {
	t.Helper()
	checkout := func(_ context.Context, _ ledger.WorkspaceID, repository string) (string, error) {
		if slices.Contains(kept, repository) {
			return "/srv/" + repository, nil
		}
		return "", fmt.Errorf("%w: no checkout of %s", ports.ErrNotFound, repository)
	}
	git := fake.NewWorktrees()
	for _, repository := range kept {
		git.Grow("/srv/"+repository, "main")
	}
	starts := &control.Starts{Bases: git, Checkout: checkout}
	options.Starts, options.LaneBranchPrefix, options.OutsideEdits = starts, "lane/", control.NewOutsideEdits(starts, edits, "lane/")
	d := apitest.StartWith(t, options)
	return d, apitest.NewWorld(t, d)
}

func cloneOf(t *testing.T, list api.OutsideEdits, repository string) api.CloneEditsView {
	t.Helper()
	for _, clone := range list.Clones {
		if clone.Repository == repository {
			return clone
		}
	}
	t.Fatalf("the list holds no clone of %s: %+v", repository, list.Clones)
	return api.CloneEditsView{}
}

// FR-UI-14: each registered clone lists the paths intake and lanes cannot read, by path, with the step that makes each
// readable and the exact commands of that step.
func TestTheOutsideEditsListEachClonesUnreadPathsWithTheirSteps(t *testing.T) {
	t.Parallel()
	edits := fake.NewCloneEdits()
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin", Edits: []ledger.CloneEdit{
		{Path: "plans/plan.md", State: ledger.EditUncommitted}, {Path: "notes/new.md", State: ledger.EditUntracked},
		{Path: "docs/sent.md", State: ledger.EditUnpushed}, {Path: "red\x1b[31m.md", State: ledger.EditUntracked},
	}})
	d, w := outsideWorld(t, edits, "repository-1")
	w.Repository("repository-1", ledger.PersonMerges)
	w.Repository("repository-2", ledger.PersonMerges)
	list, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Clones) != 2 || list.Limit != control.CloneEditsLimit {
		t.Fatalf("the list %+v", list)
	}
	clone := cloneOf(t, list, "repository-1")
	if clone.Path != "/srv/repository-1" || clone.Branch != "main" || clone.Remote != "origin" || clone.Problem != "" {
		t.Errorf("the clone %+v", clone)
	}
	byPath := map[string]api.EditView{}
	for _, edit := range clone.Edits {
		byPath[edit.Path] = edit
	}
	if got := byPath["plans/plan.md"]; got.State != ledger.EditUncommitted || !slices.Equal(got.Steps, []string{ledger.StepCommit}) {
		t.Errorf("plans/plan.md %+v", got)
	}
	if got := byPath["docs/sent.md"]; got.State != ledger.EditUnpushed || !slices.Equal(got.Steps, []string{ledger.StepPush}) {
		t.Errorf("docs/sent.md %+v", got)
	}
	// A path is shown as plain text, and a path that held a character a page cannot show is in no command.
	if got, ok := byPath["red.md"]; !ok || !got.Hidden || len(got.Steps) != 0 {
		t.Errorf("the path with an escape reads %+v in %+v", got, clone.Edits)
	}
	if len(clone.Steps) != 2 || clone.Steps[0].Kind != ledger.StepCommit || clone.Steps[1].Kind != ledger.StepPush {
		t.Fatalf("steps %+v", clone.Steps)
	}
	if commands := strings.Join(clone.Steps[0].Commands, "\n"); !strings.Contains(commands, "git update-index --add --remove -- notes/new.md plans/plan.md &&") {
		t.Errorf("the commit step runs:\n%s", commands)
	}
	if other := cloneOf(t, list, "repository-2"); other.Path != "" || !strings.Contains(other.Problem, "keeps no clone") || len(other.Steps) != 0 {
		t.Errorf("a repository with no clone here reads %+v", other)
	}
}

// A clone git cannot read says why, and the others are read all the same.
func TestACloneGitCannotReadSaysWhyBesideTheOthers(t *testing.T) {
	t.Parallel()
	edits := fake.NewCloneEdits()
	edits.Fail("/srv/repository-1", errors.New("fatal: \x1b]8;;evil\x07bad object HEAD"))
	edits.Set("/srv/repository-2", ledger.CloneEdits{Edits: []ledger.CloneEdit{{Path: "a.md", State: ledger.EditUntracked}}})
	d, w := outsideWorld(t, edits, "repository-1", "repository-2")
	w.Repository("repository-1", ledger.PersonMerges)
	w.Repository("repository-2", ledger.PersonMerges)
	list, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if broken := cloneOf(t, list, "repository-1"); !strings.Contains(broken.Problem, "bad object HEAD") || strings.ContainsRune(broken.Problem, '\x1b') {
		t.Errorf("the clone git cannot read says %q", broken.Problem)
	}
	if read := cloneOf(t, list, "repository-2"); len(read.Edits) != 1 || read.Problem != "" {
		t.Errorf("the other clone reads %+v", read)
	}
}

// A clone that lists more paths than a reading keeps lists the first and says it cut the rest.
func TestAClonesListIsCutAtTheLimitAndSaysSo(t *testing.T) {
	t.Parallel()
	edits := fake.NewCloneEdits()
	var many []ledger.CloneEdit
	for i := range control.CloneEditsLimit + 3 {
		many = append(many, ledger.CloneEdit{Path: fmt.Sprintf("f%04d.md", i), State: ledger.EditUntracked})
	}
	edits.Set("/srv/repository-1", ledger.CloneEdits{Head: "abc", Edits: many})
	d, w := outsideWorld(t, edits, "repository-1")
	w.Repository("repository-1", ledger.PersonMerges)
	list, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if clone := cloneOf(t, list, "repository-1"); len(clone.Edits) != control.CloneEditsLimit || !clone.Cut {
		t.Errorf("the clone lists %d, cut %v", len(clone.Edits), clone.Cut)
	}
}

// A daemon that reads no clone, as a group server, refuses the list by name and never answers an empty one.
func TestADaemonThatReadsNoCloneRefusesTheList(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	_, err := d.Client.OutsideEdits(t.Context())
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Detail.Code != api.CodeRepositoryReadsUnavailable {
		t.Errorf("a daemon with no clone answered %v", err)
	}
}

// planningSession runs a planning lane of the work order plan-1 in repository-1, which a world registered as the
// project's only repository, so it is the planning repository.
func planningSession(t *testing.T, d *apitest.Daemon, w *apitest.World) {
	t.Helper()
	if _, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{
		ID: "plan-1", Kind: workorders.KindPlan, Title: "Plan the retry budget", Statement: "Plan it.", RepositoryID: "repository-1",
		Criteria: []planning.Criterion{{ID: "c1", Text: "The plan is written."}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Review(t.Context(), "plan-1"); err != nil {
		t.Fatal(err)
	}
	w.Run("plan-1")
}

// The planning repository's clone offers to copy its files into the branch of the planning session that runs, from
// that branch's tip, and to tell the session to merge it.
func TestThePlanningClonesFilesCanBeCopiedIntoTheRunningSessionsBranch(t *testing.T) {
	t.Parallel()
	edits := fake.NewCloneEdits()
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin", Edits: []ledger.CloneEdit{{Path: "plans/plan.md", State: ledger.EditUncommitted}}})
	d, w := outsideWorld(t, edits, "repository-1")
	w.Repository("repository-1", ledger.PersonMerges)
	planningSession(t, d, w)
	list, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	clone := cloneOf(t, list, "repository-1")
	if !clone.Planning || len(clone.Steps) != 2 || clone.Steps[1].Kind != ledger.StepCopy {
		t.Fatalf("the planning clone %+v", clone)
	}
	copied := strings.Join(clone.Steps[1].Commands, "\n")
	if !strings.Contains(copied, "git read-tree refs/heads/lane/lane-plan-1 &&") || !strings.Contains(copied, "ropeshift message send lane-plan-1 --project default --kind instruction") {
		t.Errorf("the copy step runs:\n%s", copied)
	}
	if got := clone.Edits[0].Steps; !slices.Equal(got, []string{ledger.StepCommit, ledger.StepCopy}) {
		t.Errorf("plans/plan.md takes the steps %v", got)
	}
}

// TestGoldenOutsideEdits writes what the Repositories and Plans views read of the clones: a planning clone with every
// kind of path and a running planning session, a clone with nothing to read, and a repository the machine keeps no
// clone of.
func TestGoldenOutsideEdits(t *testing.T) {
	t.Parallel()
	edits := fake.NewCloneEdits()
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "4f1c2a9e0b7d6c5e4f3a2b1c0d9e8f7a6b5c4d3e", Remote: "origin", Edits: []ledger.CloneEdit{
		{Path: "plans/retry.md", State: ledger.EditUncommitted}, {Path: "plans/notes on retries.md", State: ledger.EditUntracked},
		{Path: "docs/design.md", State: ledger.EditUnpushed}, {Path: "plans/\u202edm.yrter", State: ledger.EditUntracked},
	}})
	edits.Set("/srv/repository-2", ledger.CloneEdits{Branch: "main", Head: "9e8f7a6b5c4d3e4f1c2a9e0b7d6c5e4f3a2b1c0d", Remote: "origin"})
	clock := apitest.NewClock()
	checkout := func(_ context.Context, _ ledger.WorkspaceID, repository string) (string, error) {
		if repository != "repository-3" {
			return "/srv/" + repository, nil
		}
		return "", fmt.Errorf("%w: this worker keeps no checkout of repository-3", ports.ErrNotFound)
	}
	git := fake.NewWorktrees()
	git.Grow("/srv/repository-1", "main")
	starts := &control.Starts{Bases: git, Checkout: checkout}
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect, Starts: starts, LaneBranchPrefix: "lane/", OutsideEdits: control.NewOutsideEdits(starts, edits, "lane/")})
	w := apitest.NewWorld(t, d)
	w.Repository("repository-1", ledger.PersonMerges)
	planning := true
	if _, err := control.NewRepositories(d.Store).Change(t.Context(), apitest.Scope, "repository-1", control.RepositoryChange{Planning: &planning}, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}); err != nil {
		t.Fatal(err)
	}
	w.Repository("repository-2", ledger.PersonMerges)
	w.Repository("repository-3", ledger.PersonMerges)
	planningSession(t, d, w)
	list, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "outside-edits", steadyTimes(pretty(t, list)))
}
