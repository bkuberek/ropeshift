package worker_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A planning lane reads every other repository of its project (ADR-0022, FR-REPO-9): the worker checks each
// one out with no branch at the commit the claim recorded, beside the lane's worktree, names it in the brief,
// lets the session read it and write nothing there, and removes it when the lane ends. A repository whose
// checkout this machine does not keep is named in the brief as one the lane could not read.
func TestAPlanningLaneReadsItsProjectsOtherRepositories(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	base.kind, base.repository = workorders.KindPlan, "repo-plan"
	worktrees := fake.NewWorktrees()
	checkouts := map[string]string{"repo-plan": "/repository", "repo-api": "/checkouts/api"}
	worktrees.Grow("/repository", "main")
	api := worktrees.Grow("/checkouts/api", "main")
	find := func(_ context.Context, id string) (string, error) {
		if path, ok := checkouts[id]; ok {
			return path, nil
		}
		return "", fmt.Errorf("no checkout of %s", id)
	}
	base.worktrees = worktrees
	base.starts = &control.Starts{Bases: worktrees, Checkout: func(ctx context.Context, _ ledger.WorkspaceID, id string) (string, error) { return find(ctx, id) }}
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	for id, directory := range map[string]string{"repo-plan": "plan", "repo-api": "api", "repo-web": "web"} {
		if _, err := control.NewRepositories(base.store).Detect(t.Context(), scope, control.DetectedRepository{ID: id, DefaultBranch: "main", Directory: directory}, system); err != nil {
			t.Fatal(err)
		}
	}
	r := newPlanningRigFor(t, base)
	r.config.Repository = func(ctx context.Context, _, id string) (string, error) { return find(ctx, id) }
	r.dispatched()
	r.runWith()

	created := worktrees.Created()
	if len(created) != 2 {
		t.Fatalf("the worker made %d worktrees: %+v", len(created), created)
	}
	own, read := created[0], created[1]
	want := filepath.Join(own.Path+".reads", "api")
	if !read.Detached || read.Base != api || read.Repository != "/checkouts/api" || read.Path != want || read.Branch != "" {
		t.Errorf("the read-only worktree was asked for as %+v, want detached at %s in %s", read, api, want)
	}
	sessions := r.driver.Specs()
	if len(sessions) != 1 {
		t.Fatalf("%d sessions", len(sessions))
	}
	session := sessions[0]
	if !slices.Equal(session.ReadOnlyDirs, []string{want}) || session.ReadOnly {
		t.Errorf("the session reads %v, read-only %v", session.ReadOnlyDirs, session.ReadOnly)
	}
	for _, text := range []string{want, api, "read-only", "web: not available"} {
		if !strings.Contains(session.Brief, text) {
			t.Errorf("the brief lacks %q:\n%s", text, session.Brief)
		}
	}
	if _, err := worktrees.Inspect(t.Context(), want); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the read-only worktree outlived the lane: %v", err)
	}
	if _, err := worktrees.Inspect(t.Context(), own.Path); err != nil {
		t.Errorf("the lane's own worktree went with it: %v", err)
	}
}
