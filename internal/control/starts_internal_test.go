package control

import (
	"context"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A lane reads another repository at the commit its dependency there merged as. With several dependencies
// in that repository it reads the merge that holds the others, and with none that holds them all, or no
// merge on record, it reads the base branch tip (ADR-0022).
func TestAReadStandsAtTheMergeThatHoldsTheOthers(t *testing.T) {
	t.Parallel()
	git := fake.NewWorktrees()
	const path = "/work/api"
	first := git.Grow(path, "main")
	second := git.Grow(path, "main")
	aside := git.Add(first)
	tip := git.Grow(path, "main")
	s := &Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return path, nil }}
	api := ledger.Repository{ID: "repo-api", Name: "api", DefaultBranch: "main"}
	tests := map[string]struct {
		merged []string
		want   string
	}{
		"one merge":                   {[]string{first}, first},
		"the later of two merges":     {[]string{first, second}, second},
		"the later merge named first": {[]string{second, first}, second},
		"merges on separate lines":    {[]string{second, aside}, tip},
		"no merge on record":          {nil, tip},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reads := s.readsAt(t.Context(), []readWant{{record: api, name: "api", merged: tt.merged}}, newPass("project"))
			want := ledger.RepositoryRead{Repository: "repo-api", Name: "api", Ref: "main", Commit: tt.want}
			if len(reads) != 1 || reads[0] != want {
				t.Errorf("the lane reads %+v, want %+v", reads, want)
			}
		})
	}
}

// A base branch a repository kept from before branch names grew stricter, one that holds a character git or the forge
// reads differently, starts no lane: the work holds as base_unavailable, naming the branch with escapes in place of
// those characters and the command that names a base branch in its place.
func TestABaseBranchWithAHiddenCharacterHoldsTheLaneWithTheRemedy(t *testing.T) {
	t.Parallel()
	git := fake.NewWorktrees()
	const path = "/work/api"
	base := "ma\u200bin"
	git.Grow(path, base)
	s := &Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return path, nil }}
	in := startInput{order: workorders.WorkOrder{ID: "wo-1", RepositoryID: "repo-api"}, repository: "api", base: base, startFrom: ledger.StartFromMerged}
	start, hold := s.place(t.Context(), in, newPass("project"))
	if hold == nil || hold.Reason != workorders.HoldBaseUnavailable {
		t.Fatalf("the lane starts at %+v (hold %+v), want it held as base_unavailable", start, hold)
	}
	for _, want := range []string{`"ma\u200bin"`, "holds a character git or the forge reads differently", "`ropeshift repository set api --base <branch>`"} {
		if !strings.Contains(hold.Detail, want) {
			t.Errorf("the hold says %q, which does not hold %q", hold.Detail, want)
		}
	}
	if strings.ContainsRune(hold.Detail, '\u200b') {
		t.Errorf("the hold says %q, which holds the hidden character itself", hold.Detail)
	}
}
