package ledger

import (
	"slices"
	"strings"
	"testing"
)

func TestTheConflictPathsAreThoseBothSidesChanged(t *testing.T) {
	edits := []CloneEdit{{Path: "a.md", State: EditUncommitted}, {Path: "b.md", State: EditUnpushed}, {Path: "mine.md", State: EditUntracked}}
	got := ConflictPaths([]string{"a.md", "b.md", "c.md", "lane-only.md"}, edits, []string{"c.md", "a.md", "theirs.md"})
	want := []ConflictPath{{Path: "a.md", Person: string(EditUncommitted)}, {Path: "b.md", Person: string(EditUnpushed)}, {Path: "c.md", Person: PersonPushed}}
	if !slices.Equal(got, want) {
		t.Errorf("conflict paths %+v, want %+v", got, want)
	}
	if got := ConflictPaths([]string{"lane.md"}, edits, []string{"theirs.md"}); len(got) != 0 {
		t.Errorf("paths only one side changed conflict: %+v", got)
	}
}

func conflictPlace() ConflictPlace {
	return ConflictPlace{Project: "harbor", Clone: "/srv/harbor", WorkOrder: "wo-1", Lane: "lane-1", Base: "origin/main", Branch: "edits/wo-1-0123456789ab"}
}

func TestTheOptionsOfAConflictKeepBothSidesOrOneAndOverwriteNeither(t *testing.T) {
	paths := []ConflictPath{{Path: "a.md", Person: string(EditUncommitted)}, {Path: "c.md", Person: PersonPushed}}
	options := ConflictOptions(paths, conflictPlace())
	kinds := []string{}
	for _, option := range options {
		kinds = append(kinds, option.Kind)
	}
	if !slices.Equal(kinds, []string{SettleKeepBoth, SettleKeepPerson, SettleStop}) {
		t.Fatalf("options %v", kinds)
	}
	both := strings.Join(options[0].Commands, "\n")
	for _, want := range []string{
		"git read-tree HEAD &&",
		"git update-index --add --remove -- a.md &&",
		"git branch edits/wo-1-0123456789ab ",
		"ropeshift message send lane-1 --project harbor --kind instruction --body 'A person changed a.md, c.md too.",
		"Merge the branch edits/wo-1-0123456789ab into your branch",
		"Merge origin/main into your branch",
		"Keep both changes: overwrite neither side",
	} {
		if !strings.Contains(both, want) {
			t.Errorf("keeping both lacks %q:\n%s", want, both)
		}
	}
	if keep := strings.Join(options[1].Commands, "\n"); !strings.Contains(keep, `revert your branch'\''s change to a.md, c.md`) || strings.Contains(keep, "git ") {
		t.Errorf("keeping the person's side runs:\n%s", keep)
	}
	if stop := options[2].Commands; !slices.Equal(stop, []string{"ropeshift work cancel wo-1 --project harbor --reason 'A person changed the same files: a.md, c.md.'"}) {
		t.Errorf("stopping runs %v", stop)
	}
}

func TestAConflictOnWorkThatEndedSendsItBack(t *testing.T) {
	place := conflictPlace()
	place.Ended = true
	options := ConflictOptions([]ConflictPath{{Path: "c.md", Person: PersonPushed}}, place)
	for _, option := range options[:2] {
		last := option.Commands[len(option.Commands)-1]
		if !strings.HasPrefix(last, "ropeshift work rework wo-1 --project harbor --reason ") {
			t.Errorf("%s ends with %q", option.Kind, last)
		}
	}
	if strings.Contains(strings.Join(options[0].Commands, "\n"), "git ") {
		t.Errorf("a conflict with only pushed files commits nothing: %v", options[0].Commands)
	}
}

func TestAConflictNamesInNoCommandAPathItCannotShow(t *testing.T) {
	options := ConflictOptions([]ConflictPath{{Path: "evil\u202e.md", Person: string(EditUntracked)}, {Path: "ok.md", Person: PersonPushed}}, conflictPlace())
	for _, option := range options {
		if strings.Contains(strings.Join(option.Commands, "\n"), "evil") {
			t.Errorf("%s names the hidden path: %v", option.Kind, option.Commands)
		}
	}
}

// The branch the person's files are committed on is named by the work order and what the files hold, so it is the same
// branch at every reading while the files stay as they are, and another once they change.
func TestTheEditsBranchIsNamedByTheWorkAndWhatTheFilesHold(t *testing.T) {
	first := EditsBranch("wo-1", map[string]string{"a.md": "blob-a", "b.md": ""})
	again := EditsBranch("wo-1", map[string]string{"b.md": "", "a.md": "blob-a"})
	if first != again || !strings.HasPrefix(first, "edits/wo-1-") || len(first) != len("edits/wo-1-")+12 {
		t.Errorf("the branch is %q, then %q", first, again)
	}
	for _, other := range []string{EditsBranch("wo-1", map[string]string{"a.md": "blob-a2", "b.md": ""}), EditsBranch("wo-2", map[string]string{"a.md": "blob-a", "b.md": ""})} {
		if other == first {
			t.Errorf("other files or other work name the same branch %q", first)
		}
	}
	if err := CheckBranch(first); err != nil {
		t.Errorf("the branch is no branch git takes: %v", err)
	}
}

// What ends a conflict is said with it (FR-UI-13): the lane's branch holding the person's side, or no longer changing
// the files, or the work merged or stopped.
func TestAConflictSaysWhatClearsIt(t *testing.T) {
	place := conflictPlace()
	for _, test := range []struct {
		paths []ConflictPath
		want  string
	}{
		{[]ConflictPath{{Path: "a.md", Person: string(EditUncommitted)}}, "This clears when the lane's branch holds edits/wo-1-0123456789ab or no longer changes these files, or when the lane's work is merged or stopped."},
		{[]ConflictPath{{Path: "c.md", Person: PersonPushed}}, "This clears when the lane's branch holds the commits of origin/main that changed these files or no longer changes them, or when the lane's work is merged or stopped."},
		{[]ConflictPath{{Path: "a.md", Person: string(EditUntracked)}, {Path: "c.md", Person: PersonPushed}}, "This clears when the lane's branch holds edits/wo-1-0123456789ab and the commits of origin/main that changed these files, or no longer changes them, or when the lane's work is merged or stopped."},
	} {
		if got := ConflictClears(test.paths, place); got != test.want {
			t.Errorf("clears %q, want %q", got, test.want)
		}
	}
}
