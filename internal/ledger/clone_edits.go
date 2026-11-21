package ledger

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EditState says why intake and lanes cannot read a path of a person's clone. A lane starts from the remote's copy of
// its base branch and intake reads the remote's copy of the default branch, so they read only what a commit holds and,
// in a clone with a remote, what a push shared (FR-UI-14).
type EditState string

const (
	// EditUncommitted is a tracked path whose file or index entry differs from the commit the clone has checked out.
	EditUncommitted EditState = "uncommitted"
	// EditUntracked is a path git does not track and the clone does not ignore.
	EditUntracked EditState = "untracked"
	// EditUnpushed is a path that a commit of the checked-out branch changed, which none of the remote's branches hold.
	EditUnpushed EditState = "unpushed"
)

// CloneEdit is one path of a clone that intake and lanes cannot read, relative to the clone's top, with slashes.
type CloneEdit struct {
	Path  string    `json:"path"`
	State EditState `json:"state"`
}

// CloneEdits is what a clone holds that intake and lanes cannot read, as git said when it was read.
type CloneEdits struct {
	// Branch is the branch the clone has checked out, and empty for a detached head.
	Branch string `json:"branch,omitempty"`
	// Head is the commit the clone has checked out, and empty in a clone with no commit yet.
	Head string `json:"head,omitempty"`
	// Remote is the remote a push goes to, and empty for a clone with none, where nothing is unpushed.
	Remote string `json:"remote,omitempty"`
	// Edits lists each path once, in order of path. A path with uncommitted changes and unpushed commits is uncommitted.
	Edits []CloneEdit `json:"edits"`
	// Cut says git listed more paths than the read keeps, so Edits is not every path.
	Cut bool `json:"cut,omitempty"`
}

// The kinds of step that make a clone's paths readable.
const (
	StepCommit = "commit"
	StepCopy   = "copy"
	StepPush   = "push"
)

// EditStep is one step that makes paths of a clone readable, with the exact commands that take it.
type EditStep struct {
	// Kind is StepCommit, StepCopy, or StepPush.
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Branch is the branch the step makes or pushes.
	Branch string `json:"branch"`
	// Paths are the paths its commands take.
	Paths    []string `json:"paths"`
	Commands []string `json:"commands"`
}

// PlanningBranch is the branch of an open planning session in a repository, which a copy step lands files beside.
type PlanningBranch struct {
	WorkOrder string `json:"work_order"`
	Lane      string `json:"lane"`
	Branch    string `json:"branch"`
}

// EditPlace is what the steps of a clone are made from, apart from its edits.
type EditPlace struct {
	// Project names the project in the commands of the command line.
	Project string
	// Clone is the path of the clone's top.
	Clone string
	// Stamp names the branches the steps make, such as 20261120-153000.
	Stamp string
	// Planning are the open planning sessions' branches of the repository, for a copy step each.
	Planning []PlanningBranch
}

// editsIndex is the file in a clone's git directory that the commands build a commit in, apart from the clone's own
// index.
const editsIndex = "ropeshift-edits-index"

// EditsMessage is the message of the commit a commit or copy step makes.
const EditsMessage = "Commit files from a clone that ropeshift could not read"

// briefPaths is how many paths a message to a lane names before it counts the rest.
const briefPaths = 10

// Commandable says a path can be named in a command a person copies from a page into POSIX sh, bash, or zsh: it is valid
// UTF-8 and holds no character that PlainText drops or changes, so the command a page shows is the command the shell
// runs, and no backslash, which another shell, such as fish, reads as an escape inside single quotes.
func Commandable(text string) bool {
	if text == "" || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if HiddenRune(r) || r == '\u2028' || r == '\u2029' || r == '\\' {
			return false
		}
	}
	return true
}

// ShellWord spells text so POSIX sh, bash, and zsh read it as one word: as it is when it holds only characters none of
// them treats specially, and otherwise in single quotes, each single quote closed, escaped, and opened again. A word
// that starts with = is quoted too, because zsh expands it to the path of a command.
func ShellWord(text string) string {
	if text != "" && !strings.HasPrefix(text, "=") && strings.IndexFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_@%+=:,./-", r)
	}) < 0 {
		return text
	}
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// CommitCommands are the commands that commit paths of the clone at clone, as its files hold them now, on a new branch
// that starts at onto: HEAD, a branch named in full under refs/heads/, or empty for a branch with no parent in a clone
// with no commit. They build the commit in an index of their own inside a subshell, so the clone's checkout, index,
// branches, and files stay as they were, and the shell that runs them keeps no variable. A path the files no longer
// hold is left out of the commit when onto lacks it and removed when onto holds it. Git refuses a branch that exists
// already, so nothing is overwritten.
func CommitCommands(clone, onto, branch string, paths []string) []string {
	read, parent := "git read-tree --empty", ""
	if onto != "" {
		read, parent = "git read-tree "+ShellWord(onto), " -p "+ShellWord(onto)
	}
	words := make([]string, 0, len(paths))
	for _, path := range paths {
		words = append(words, ShellWord(path))
	}
	return []string{
		"(cd " + ShellWord(clone) + " &&",
		`  export GIT_INDEX_FILE="$(git rev-parse --absolute-git-dir)/` + editsIndex + `" &&`,
		"  " + read + " &&",
		"  git update-index --add --remove -- " + strings.Join(words, " ") + " &&",
		"  git branch " + ShellWord(branch) + ` "$(git commit-tree "$(git write-tree)"` + parent + " -m " + ShellWord(EditsMessage) + `)"`,
		`  code=$?; rm -f "$GIT_INDEX_FILE"; exit $code)`,
	}
}

// PushCommand pushes a branch of the clone at clone to the branch of the same name on remote. It names both in full, so
// a branch whose name starts with + is no forced push, and it never forces.
func PushCommand(clone, remote, branch string) string {
	ref := "refs/heads/" + branch
	return "git -C " + ShellWord(clone) + " push " + ShellWord(remote) + " " + ShellWord(ref+":"+ref)
}

// MessageCommand sends a lane of a project an instruction from the command line, as the member who runs it.
func MessageCommand(project, lane, body string) string {
	return "ropeshift message send " + ShellWord(lane) + " --project " + ShellWord(project) + " --kind instruction --body " + ShellWord(body)
}

// BriefPaths names paths in a sentence: each of the first few, and how many more there are.
func BriefPaths(paths []string) string {
	if len(paths) <= briefPaths {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(paths[:briefPaths], ", "), len(paths)-briefPaths)
}

// EditSteps are the steps that make a clone's edits readable, with their commands: commit the uncommitted and untracked
// paths to a new branch, push the unpushed commits, and, for each open planning session in the repository, copy every
// path into a new branch from the session's branch and tell the session to merge it. A path that is not Commandable is
// in no step, and a clone whose path or remote is not Commandable gets no step that names it.
func EditSteps(edits CloneEdits, place EditPlace) []EditStep {
	if !Commandable(place.Clone) {
		return []EditStep{}
	}
	var local, unpushed, every []string
	for _, edit := range edits.Edits {
		if !Commandable(edit.Path) {
			continue
		}
		every = append(every, edit.Path)
		if edit.State == EditUnpushed {
			unpushed = append(unpushed, edit.Path)
		} else {
			local = append(local, edit.Path)
		}
	}
	pushes := edits.Remote != "" && Commandable(edits.Remote) && !strings.HasPrefix(edits.Remote, "-")
	steps := []EditStep{}
	if len(local) > 0 {
		branch := "edits/" + place.Stamp
		onto := "HEAD"
		if edits.Head == "" {
			onto = ""
		}
		step := EditStep{
			Kind: StepCommit, Title: "Commit them to a new branch", Branch: branch, Paths: local,
			Commands: CommitCommands(place.Clone, onto, branch, local),
			Detail:   fmt.Sprintf("Commits these files as the clone holds them now on the new branch %s, from the commit the clone has checked out. The clone's checkout, index, and files stay as they are.", branch),
		}
		if pushes {
			step.Commands = append(step.Commands, PushCommand(place.Clone, edits.Remote, branch))
			step.Detail += fmt.Sprintf(" The last command pushes the branch to %s.", edits.Remote)
		}
		steps = append(steps, step)
	}
	if len(unpushed) > 0 && pushes {
		step := EditStep{Kind: StepPush, Title: "Push the commits", Branch: edits.Branch, Paths: unpushed}
		if edits.Branch != "" && Commandable(edits.Branch) {
			step.Commands = []string{PushCommand(place.Clone, edits.Remote, edits.Branch)}
			step.Detail = fmt.Sprintf("Pushes %s to %s, which holds no commit that changed these files yet. Git refuses a push that would drop the remote's commits.", edits.Branch, edits.Remote)
		} else {
			step.Branch = "edits/" + place.Stamp + "-head"
			step.Commands = []string{"git -C " + ShellWord(place.Clone) + " branch " + ShellWord(step.Branch) + " HEAD", PushCommand(place.Clone, edits.Remote, step.Branch)}
			step.Detail = fmt.Sprintf("The clone has no branch checked out that a command can name, so the first command names its commit with the new branch %s, and the second pushes it to %s.", step.Branch, edits.Remote)
		}
		steps = append(steps, step)
	}
	for _, planning := range place.Planning {
		if len(every) == 0 || !Commandable(planning.Branch) || !Commandable(planning.Lane) {
			continue
		}
		branch := "edits/" + place.Stamp + "-" + planning.Lane
		body := fmt.Sprintf("Merge the branch %s into your branch in your own worktree. A member copied these files onto it from their clone of the planning repository: %s.", branch, BriefPaths(every))
		steps = append(steps, EditStep{
			Kind: StepCopy, Title: "Copy them into the planning branch " + planning.Branch, Branch: branch, Paths: every,
			Commands: append(CommitCommands(place.Clone, "refs/heads/"+planning.Branch, branch, every), MessageCommand(place.Project, planning.Lane, body)),
			Detail: fmt.Sprintf("Commits these files as the clone holds them now on the new branch %s, from the tip of %s, which the planning lane %s works on, and tells the lane to merge it in its own worktree. Ropeshift never moves a branch a worktree has checked out.",
				branch, planning.Branch, planning.Lane),
		})
	}
	return steps
}
