package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// `repository edits` prints each clone's paths that intake and lanes cannot read, the step each needs, and the exact
// commands of each step, as the web UI's Repositories view lists them.
func TestRepositoryEditsPrintsEachPathAndTheCommandsOfItsStep(t *testing.T) {
	edits := fake.NewCloneEdits()
	edits.Set("/srv/app", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin", Edits: []ledger.CloneEdit{
		{Path: "plans/plan.md", State: ledger.EditUncommitted}, {Path: "docs/sent.md", State: ledger.EditUnpushed}, {Path: "evil\u202e.md", State: ledger.EditUntracked},
	}})
	starts := &control.Starts{Bases: fake.NewWorktrees(), Checkout: func(_ context.Context, _ ledger.WorkspaceID, repository string) (string, error) {
		if repository == aRepositoryID {
			return "/srv/app", nil
		}
		return "", fmt.Errorf("%w: none", ports.ErrNotFound)
	}}
	s := newSessionWith(t, apitest.Options{Starts: starts, OutsideEdits: control.NewOutsideEdits(starts, edits, "lane/")})
	s.detect(aRepositoryID, "main")
	text := s.must("repository", "edits")
	for _, want := range []string{
		"app\t/srv/app\tbranch main\tremote origin",
		"\tuncommitted\tplans/plan.md\tcommit",
		"\tunpushed\tdocs/sent.md\tpush",
		"\tuntracked\tevil.md\tno command names it, because it holds a character that is not seen",
		"step\tcommit\tCommit them to a new branch",
		"\t  git update-index --add --remove -- plans/plan.md &&",
		"step\tpush\tPush the commits",
		"\tgit -C /srv/app push origin refs/heads/main:refs/heads/main",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("edits lack %q:\n%s", want, text)
		}
	}
	var list api.OutsideEdits
	if err := json.Unmarshal([]byte(s.must("repository", "edits", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Clones) != 1 || len(list.Clones[0].Edits) != 3 {
		t.Errorf("json %+v", list)
	}
}

func TestRepositoryEditsSaysWhatItCouldNotRead(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	code, _, stderr := s.run("repository", "edits")
	if code == cli.ExitOK || !strings.Contains(stderr, "reads no clone") {
		t.Errorf("a daemon that reads no clone: exit %d, %s", code, stderr)
	}
}

// `repository edits` prints each conflict between a lane and a person's edits with both sides and the commands of each
// way to settle it, as Needs you lists it.
func TestRepositoryEditsPrintsEachConflictAndTheWaysToSettleIt(t *testing.T) {
	edits := fake.NewCloneEdits()
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin", Edits: []ledger.CloneEdit{{Path: "plans/plan.md", State: ledger.EditUncommitted}}})
	edits.Diverge("/srv/repository-1", "lane/lane-wo-1", ledger.Divergence{Fork: "fork", Base: "origin/main", Lane: []string{"plans/plan.md"}})
	git := fake.NewWorktrees()
	git.Grow("/srv/repository-1", "main")
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/srv/repository-1", nil }}
	s := newSessionWith(t, apitest.Options{Starts: starts, LaneBranchPrefix: "lane/", OutsideEdits: control.NewOutsideEdits(starts, edits, "lane/")})
	w := apitest.NewWorld(t, s.daemon)
	w.Repository("repository-1", ledger.PersonMerges)
	w.Running("wo-1", "Retry within a budget")
	text := s.must("repository", "edits")
	for _, want := range []string{
		"conflict\two-1\tRetry within a budget\tlane lane-wo-1 on lane/lane-wo-1 in app, against origin/main",
		"\tuncommitted\tplans/plan.md",
		"\tThis clears when the lane's branch holds edits/wo-1-",
		"settle\tkeep_both\tKeep both changes",
		"settle\tkeep_person\tKeep the person's side",
		"settle\tstop\tStop the lane's work",
		"\tropeshift work cancel wo-1 --project default --reason",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("edits lack %q:\n%s", want, text)
		}
	}
}
