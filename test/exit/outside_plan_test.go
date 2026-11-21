package exit

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestAPlanPushedFromOutsideIsANewVersionToReview holds FR-UI-14 on the real daemon with real git: a plan, and then a
// document the plan lists, that a person commits in another clone and pushes to the remote reach intake on its next
// fetch, and each is a new version of the plan with a request that waits for a member, while the clone the daemon
// reads is never moved.
func TestAPlanPushedFromOutsideIsANewVersionToReview(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("goal.md", contract.GoalText("a-goal", "The goal"))
	repo.Commit("plan.md", contract.PlanText("a-plan", "The plan", "a-goal"))
	repo.Commit("design.md", contract.DocumentText("architecture", "design", "The design", "a-plan", "The first design.\n"))
	bare := filepath.Join(t.TempDir(), "plans.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	held := repo.Git("rev-parse", "refs/heads/main")
	in.waitReady(in.start("--source", repo.Dir, "--intake-tick", "50ms"))

	var plans api.PlanList
	in.waitFor("the plan", func() bool {
		if err := json.Unmarshal([]byte(in.must("plan", "list", "--json")), &plans); err != nil {
			t.Fatal(err)
		}
		return len(plans.Plans) == 1
	})
	plan := plans.Plans[0].ID
	versions := func() []api.PlanVersionLine {
		var view api.PlanView
		if err := json.Unmarshal([]byte(in.must("plan", "show", plan, "--json")), &view); err != nil {
			t.Fatal(err)
		}
		return view.Versions
	}
	in.waitFor("the first version's request", func() bool {
		got := versions()
		return len(got) == 1 && got[0].State == authority.ApprovalPending
	})

	personDir := filepath.Join(t.TempDir(), "person")
	repo.Git("clone", "--quiet", bare, personDir)
	person := &gittest.Repo{T: t, Dir: personDir, Home: repo.Home}
	pushed := func(name, text string) {
		t.Helper()
		person.Commit(name, text)
		person.Git("push", "--quiet", "origin", "main")
	}
	pushed("plan.md", strings.Replace(contract.PlanText("a-plan", "The plan", "a-goal"), "How.", "How, as a person wrote it outside ropeshift.", 1))
	in.waitFor("the version a person pushed", func() bool {
		got := versions()
		return len(got) == 2 && got[0].Version == 2 && got[0].State == authority.ApprovalPending
	})
	pushed("design.md", contract.DocumentText("architecture", "design", "The design", "a-plan", "The second design, pushed from outside.\n"))
	in.waitFor("the version a pushed document makes", func() bool {
		got := versions()
		return len(got) == 3 && got[0].Version == 3 && got[0].State == authority.ApprovalPending
	})
	if got := versions(); got[1].State == authority.ApprovalPending {
		t.Errorf("the version a newer one replaced still waits: %+v", got)
	}
	if now := repo.Git("rev-parse", "refs/heads/main"); now != held {
		t.Errorf("reading the pushes moved the clone's main from %s to %s", held, now)
	}
}
