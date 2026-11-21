package ledger

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// PersonPushed says a person changed a path in commits pushed to the base, which no merge of ropeshift made.
const PersonPushed = "pushed"

// ConflictPath is a path a lane's branch changed that a person changed too (FR-UI-14).
type ConflictPath struct {
	Path string `json:"path"`
	// Person is how the person changed it: uncommitted, untracked, or unpushed in their clone, or pushed to the base.
	Person string `json:"person"`
}

// Divergence is what a lane's branch and the base it began from each changed since they last shared a commit.
type Divergence struct {
	// Fork is the commit they last shared, and Base the ref the base was read at, such as origin/main.
	Fork string `json:"fork"`
	Base string `json:"base"`
	// Lane lists the paths the branch's commits changed since Fork.
	Lane []string `json:"lane"`
	// Pushed lists the paths that commits on the base changed since Fork, leaving out every commit a merge of ropeshift
	// made and every commit a merge brought in.
	Pushed []string `json:"pushed"`
	// Cut says git listed more paths of a side than a read keeps.
	Cut bool `json:"cut,omitempty"`
}

// ConflictPaths are the paths a lane changed that the person changed too, in their clone or in pushes to the base, in
// order of path and each once. A path the clone changed is named by the clone's state of it.
func ConflictPaths(lane []string, edits []CloneEdit, pushed []string) []ConflictPath {
	changed := map[string]bool{}
	for _, path := range lane {
		changed[path] = true
	}
	found := map[string]string{}
	for _, path := range pushed {
		if changed[path] {
			found[path] = PersonPushed
		}
	}
	for _, edit := range edits {
		if changed[edit.Path] {
			found[edit.Path] = string(edit.State)
		}
	}
	out := make([]ConflictPath, 0, len(found))
	for path, person := range found {
		out = append(out, ConflictPath{Path: path, Person: person})
	}
	slices.SortFunc(out, func(a, b ConflictPath) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// The ways a member settles a conflict.
const (
	SettleKeepBoth   = "keep_both"
	SettleKeepPerson = "keep_person"
	SettleStop       = "stop"
)

// ConflictPlace is what a conflict's options are made from, apart from its paths.
type ConflictPlace struct {
	// Project names the project in the commands of the command line.
	Project string
	// Clone is the path of the person's clone, which shares its branches with the lane's worktree.
	Clone     string
	WorkOrder string
	Lane      string
	// Base is the ref the base was read at, such as origin/main.
	Base string
	// Branch is the branch the person's files are committed on, which EditsBranch names.
	Branch string
	// Ended says the lane's session ended, so the work goes back to a new attempt rather than taking a message.
	Ended bool
}

// editsDigest is how many hexadecimal digits of the digest of the files an edits branch's name holds.
const editsDigest = 12

// EditsBranch names the branch a person's files are committed on to settle a conflict with a work order's lane: the
// work order and a digest of each path with the blob its file holds, empty for a file that is gone. The name stays the
// same while the files do, so the lane's branch holding it says the conflict is settled, and changes once they change.
func EditsBranch(workOrder string, contents map[string]string) string {
	digest := sha256.New()
	for _, path := range slices.Sorted(maps.Keys(contents)) {
		fmt.Fprintf(digest, "%s\x00%s\n", path, contents[path])
	}
	return "edits/" + workOrder + "-" + hex.EncodeToString(digest.Sum(nil))[:editsDigest]
}

// ConflictClears says what ends a conflict (FR-UI-13): the lane's branch holding the person's side, or no longer changing
// the files, or the lane's work merged or stopped.
func ConflictClears(paths []ConflictPath, place ConflictPlace) string {
	local, pushed := false, false
	for _, conflict := range paths {
		if conflict.Person == PersonPushed {
			pushed = true
		} else {
			local = true
		}
	}
	const end = ", or when the lane's work is merged or stopped."
	switch {
	case local && pushed:
		return fmt.Sprintf("This clears when the lane's branch holds %s and the commits of %s that changed these files, or no longer changes them%s", place.Branch, place.Base, end)
	case pushed:
		return fmt.Sprintf("This clears when the lane's branch holds the commits of %s that changed these files or no longer changes them%s", place.Base, end)
	}
	return fmt.Sprintf("This clears when the lane's branch holds %s or no longer changes these files%s", place.Branch, end)
}

// WorkCommand runs `ropeshift work <verb>` on a work order with a reason, as the member who runs it.
func WorkCommand(verb, project, workOrder, reason string) string {
	return "ropeshift work " + verb + " " + ShellWord(workOrder) + " --project " + ShellWord(project) + " --reason " + ShellWord(reason)
}

// ConflictOptions are the ways a member settles a conflict, each with its exact commands: keep both changes, which
// commits the person's edits in their clone on a new branch and asks the lane to merge them and the base and keep
// both; keep the person's side, which asks the lane to revert its change to the paths; or stop the lane's work. None
// overwrites either side, and a path that is not Commandable is named in no command.
func ConflictOptions(paths []ConflictPath, place ConflictPlace) []EditStep {
	var named, local []string
	pushed := false
	for _, conflict := range paths {
		if !Commandable(conflict.Path) {
			continue
		}
		named = append(named, conflict.Path)
		if conflict.Person == PersonPushed {
			pushed = true
		} else {
			local = append(local, conflict.Path)
		}
	}
	if len(named) == 0 || !Commandable(place.Clone) || !Commandable(place.Lane) || !Commandable(place.WorkOrder) {
		return []EditStep{}
	}
	ask := func(body string) string {
		if place.Ended {
			return WorkCommand("rework", place.Project, place.WorkOrder, body)
		}
		return MessageCommand(place.Project, place.Lane, body)
	}
	both := EditStep{Kind: SettleKeepBoth, Title: "Keep both changes", Paths: named}
	body := fmt.Sprintf("A person changed %s too.", BriefPaths(named))
	if len(local) > 0 {
		if !Commandable(place.Branch) {
			return []EditStep{}
		}
		both.Branch = place.Branch
		both.Commands = CommitCommands(place.Clone, "HEAD", both.Branch, local)
		body += fmt.Sprintf(" Merge the branch %s into your branch in your own worktree: it holds their files.", both.Branch)
	}
	if pushed && Commandable(place.Base) {
		body += fmt.Sprintf(" Merge %s into your branch: it holds the commits they pushed.", place.Base)
	}
	body += " Keep both changes: overwrite neither side, and ask when they cannot both stand."
	both.Commands = append(both.Commands, ask(body))
	both.Detail = "Asks the lane to merge the person's side into its branch and keep both changes."
	if len(local) > 0 {
		both.Detail = fmt.Sprintf("Commits the person's files in the clone on the new branch %s, leaving the clone's checkout, index, and files as they are, then asks the lane to merge that branch and keep both changes.", both.Branch)
	}
	keep := EditStep{
		Kind: SettleKeepPerson, Title: "Keep the person's side", Paths: named,
		Detail:   "Asks the lane to revert its branch's change to these files and keep the rest of its work, so the person's change stands.",
		Commands: []string{ask(fmt.Sprintf("A person changed %s too. Keep their change: revert your branch's change to %s, and keep the rest of your work.", BriefPaths(named), BriefPaths(named)))},
	}
	stop := EditStep{
		Kind: SettleStop, Title: "Stop the lane's work", Paths: named,
		Detail:   "Cancels the work order, so its branch merges nowhere and the person's change stands. The branch stays.",
		Commands: []string{WorkCommand("cancel", place.Project, place.WorkOrder, fmt.Sprintf("A person changed the same files: %s.", strings.TrimSuffix(BriefPaths(named), ".")))},
	}
	return []EditStep{both, keep, stop}
}
