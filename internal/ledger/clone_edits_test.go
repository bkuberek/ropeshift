package ledger

import (
	"slices"
	"strings"
	"testing"
)

func TestAPathIsCommandableOnlyWhenThePageShowsWhatTheShellRuns(t *testing.T) {
	for _, path := range []string{"plans/plan.md", "a b.md", "it's.md", "naïve/ü.md", "-x.md"} {
		if !Commandable(path) {
			t.Errorf("%q is not commandable", path)
		}
	}
	for _, path := range []string{"", "back\\slash.md", "a\nb.md", "tab\t.md", "evil\u202e.md", "zero\u200b.md", "line\u2028.md", "\xff.md", "esc\x1b[31m.md"} {
		if Commandable(path) {
			t.Errorf("%q is commandable", path)
		}
	}
}

func TestAShellWordIsOneWord(t *testing.T) {
	for text, want := range map[string]string{
		"plans/plan.md": "plans/plan.md",
		"a b.md":        "'a b.md'",
		"it's":          `'it'\''s'`,
		"$(rm -rf /)":   "'$(rm -rf /)'",
		"":              "''",
		"=main":         "'=main'",
		"a=b":           "a=b",
	} {
		if got := ShellWord(text); got != want {
			t.Errorf("ShellWord(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestTheCommitCommandsBuildTheCommitInAnIndexOfTheirOwn(t *testing.T) {
	got := strings.Join(CommitCommands("/srv/my clone", "HEAD", "edits/20261120-153000", []string{"a.md", "b c.md"}), "\n")
	for _, want := range []string{
		"(cd '/srv/my clone' &&",
		`export GIT_INDEX_FILE="$(git rev-parse --absolute-git-dir)/ropeshift-edits-index" &&`,
		"git read-tree HEAD &&",
		"git update-index --add --remove -- a.md 'b c.md' &&",
		`git branch edits/20261120-153000 "$(git commit-tree "$(git write-tree)" -p HEAD -m 'Commit files from a clone that ropeshift could not read')"`,
		`code=$?; rm -f "$GIT_INDEX_FILE"; exit $code)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the commands lack %q:\n%s", want, got)
		}
	}
	empty := strings.Join(CommitCommands("/srv/new", "", "edits/x", []string{"a.md"}), "\n")
	if !strings.Contains(empty, "git read-tree --empty &&") || strings.Contains(empty, " -p ") {
		t.Errorf("a clone with no commit gets:\n%s", empty)
	}
}

func TestEachEditGetsTheStepItsStateNeeds(t *testing.T) {
	edits := CloneEdits{Branch: "main", Head: "abc", Remote: "origin", Edits: []CloneEdit{
		{Path: "notes.md", State: EditUncommitted}, {Path: "new.md", State: EditUntracked}, {Path: "pushed.md", State: EditUnpushed},
		{Path: "hidden\u202e.md", State: EditUntracked},
	}}
	place := EditPlace{Project: "harbor", Clone: "/srv/harbor", Stamp: "20261120-153000", Planning: []PlanningBranch{{WorkOrder: "plan-1", Lane: "lane-1", Branch: "lane/lane-1"}}}
	steps := EditSteps(edits, place)
	kinds := []string{}
	for _, step := range steps {
		kinds = append(kinds, step.Kind)
	}
	if !slices.Equal(kinds, []string{StepCommit, StepPush, StepCopy}) {
		t.Fatalf("steps %v", kinds)
	}
	commit, push, copied := steps[0], steps[1], steps[2]
	if !slices.Equal(commit.Paths, []string{"notes.md", "new.md"}) || commit.Branch != "edits/20261120-153000" {
		t.Errorf("commit %+v", commit)
	}
	if last := commit.Commands[len(commit.Commands)-1]; last != "git -C /srv/harbor push origin refs/heads/edits/20261120-153000:refs/heads/edits/20261120-153000" {
		t.Errorf("the commit step ends with %q", last)
	}
	if !slices.Equal(push.Paths, []string{"pushed.md"}) || !slices.Equal(push.Commands, []string{"git -C /srv/harbor push origin refs/heads/main:refs/heads/main"}) {
		t.Errorf("push %+v", push)
	}
	if !slices.Equal(copied.Paths, []string{"notes.md", "new.md", "pushed.md"}) || copied.Branch != "edits/20261120-153000-lane-1" {
		t.Errorf("copy %+v", copied)
	}
	joined := strings.Join(copied.Commands, "\n")
	if !strings.Contains(joined, "git read-tree refs/heads/lane/lane-1 &&") || !strings.Contains(joined, "-p refs/heads/lane/lane-1") {
		t.Errorf("the copy does not start from the planning branch:\n%s", joined)
	}
	message := copied.Commands[len(copied.Commands)-1]
	if !strings.HasPrefix(message, "ropeshift message send lane-1 --project harbor --kind instruction --body 'Merge the branch edits/20261120-153000-lane-1 into your branch") {
		t.Errorf("the copy tells the lane %q", message)
	}
	for _, step := range steps {
		if strings.Contains(strings.Join(step.Commands, "\n"), "hidden") {
			t.Errorf("a step names a path that hides a character: %+v", step)
		}
	}
}

func TestAClonesStepsNameNothingTheyCannotShow(t *testing.T) {
	edits := CloneEdits{Head: "abc", Remote: "-evil", Edits: []CloneEdit{{Path: "a.md", State: EditUntracked}, {Path: "b.md", State: EditUnpushed}}}
	steps := EditSteps(edits, EditPlace{Project: "p", Clone: "/srv/a", Stamp: "s"})
	if len(steps) != 1 || steps[0].Kind != StepCommit || len(steps[0].Commands) != 6 {
		t.Errorf("a remote named like an option is pushed to: %+v", steps)
	}
	if got := EditSteps(edits, EditPlace{Clone: "/srv/\u202ea"}); len(got) != 0 {
		t.Errorf("a clone whose path hides a character has steps %+v", got)
	}
}

func TestADetachedHeadIsNamedBeforeItIsPushed(t *testing.T) {
	edits := CloneEdits{Head: "abc", Remote: "origin", Edits: []CloneEdit{{Path: "b.md", State: EditUnpushed}}}
	steps := EditSteps(edits, EditPlace{Project: "p", Clone: "/srv/a", Stamp: "s"})
	if len(steps) != 1 || !slices.Equal(steps[0].Commands, []string{"git -C /srv/a branch edits/s-head HEAD", "git -C /srv/a push origin refs/heads/edits/s-head:refs/heads/edits/s-head"}) {
		t.Errorf("steps %+v", steps)
	}
}

func TestBriefPathsCountsWhatItDoesNotName(t *testing.T) {
	var paths []string
	for i := range 12 {
		paths = append(paths, string(rune('a'+i))+".md")
	}
	if got := BriefPaths(paths); !strings.HasSuffix(got, "j.md, and 2 more") {
		t.Errorf("BriefPaths = %q", got)
	}
}
