package lanes

import "strings"

// SettingsChange is what a lane's worktree changed of the project settings its agent CLI reads, in a key that runs code
// or widens access, and of the hooks its skills, subagents, and commands declare, against the repository's at the
// commit the lane's work began from, its claim's base. A lane can edit those files, and its sessions would run what it
// wrote outside every rule ropeshift gives it, so a generation that finds one is held for a member before its session
// starts, and a session whose reload would apply one is held where it stands (ADR-0040).
type SettingsChange struct {
	// Generation is the generation that found it, which parks before its session starts.
	Generation int64 `json:"generation"`
	// Files are the settings files that differ, and the skills, subagents, and commands whose frontmatter hooks the lane
	// added or changed, as paths in the worktree.
	Files []string `json:"files"`
	// Keys are the top-level keys that differ, sorted.
	Keys []string `json:"keys"`
	// Summary says what changed of each key, a line each, as plain text with no value a setting could hide a
	// credential in.
	Summary []string `json:"summary,omitempty"`
	// Digest names what the worktree's files hold in those keys, so a member's choice binds to the change they read.
	Digest string `json:"digest"`
}

// The bounds of what a hold records: the lines its summary keeps, and the bytes of each.
const (
	MaxSettingsLines = 32
	MaxSettingsText  = 240
)

// What a member can do about a lane whose settings widen what it can run. Each is a member's choice, never an
// orchestrator's or a delegate's.
const (
	// SettingsBase runs the lane with the repository's settings: the files are restored to the base commit's before
	// its session starts, and the lane is told so. It is the recommended choice.
	SettingsBase = "base"
	// SettingsLane runs the lane with the settings it wrote, recorded as the member's decision for that change.
	SettingsLane = "lane"
	// SettingsStop blocks the lane's work, so nothing runs with the change until a member resolves or replans it.
	SettingsStop = "stop"
)

// SettingsChoices lists the choices, the recommended one first.
var SettingsChoices = []string{SettingsBase, SettingsLane, SettingsStop}

// SettingsAdvice is the choice a member is recommended for a lane whose settings widen what it can run, and why.
func SettingsAdvice() (choice, rationale string) {
	return SettingsBase, "the lane wrote what its next session would run outside every rule ropeshift gives it, and the repository's settings run what was reviewed and merged"
}

// SettingsRestoredNote is what a generation whose settings a member restored to the repository's is told first: which
// files changed under it, and why, so it does not take the change as its own.
func SettingsRestoredNote(files []string) string {
	return "ropeshift restored " + strings.Join(files, ", ") + " to the repository's version at the commit this lane's work began from, " +
		"because a member chose to run this lane with the repository's settings: the change this lane made to them widened " +
		"what its sessions run, such as hooks, plugins, or permissions. That change is no longer in the worktree. Do not make " +
		"it again; if the work needs it, say so in your handoff."
}
