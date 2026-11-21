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
