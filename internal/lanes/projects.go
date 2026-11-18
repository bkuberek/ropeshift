package lanes

import (
	"fmt"
	"maps"
	"path"
	"slices"

	"ropeshift.com/ropeshift/internal/planning"
)

// ProjectSettings is one project's `[projects.<project ID>]` table, which only the user's profile layer sets,
// because it names a folder on this machine.
type ProjectSettings struct {
	// Workspace is the absolute path of the folder that holds the project's clones on this machine
	// (FR-PROF-6). An empty path says the project has no workspace here, which stops the worker from looking
	// for one. Nil leaves the worker to find it.
	Workspace *string `toml:"workspace" json:"workspace,omitempty"`
}

// CheckProjects refuses a workspace that is not an absolute path, naming the key.
func CheckProjects(projects map[string]ProjectSettings) error {
	for _, id := range slices.Sorted(maps.Keys(projects)) {
		workspace := projects[id].Workspace
		if workspace != nil && *workspace != "" && !path.IsAbs(*workspace) {
			return fmt.Errorf("%w: projects.%s.workspace %q is not an absolute path", ErrBadProfile, id, *workspace)
		}
	}
	return nil
}

// SameProjects says two layers' `[projects]` tables name the same workspaces, for a layer past the user's that
// may restate the table and cannot change it.
func SameProjects(a, b map[string]ProjectSettings) bool {
	if len(a) != len(b) {
		return false
	}
	for id, left := range a {
		right, held := b[id]
		if !held || !sameWorkspace(left.Workspace, right.Workspace) {
			return false
		}
	}
	return true
}

func sameWorkspace(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// The ways a project's workspace was found, as the doctor names them.
const (
	WorkspaceFromProfile  = "the user's profile"
	WorkspaceFromPlanning = "the planning repository's clone"
)

// Workspace is a project's workspace on one worker: the folder that holds its clones.
type Workspace struct {
	// Path is the workspace's folder.
	Path string `json:"path"`
	// Found says how the worker found it: WorkspaceFromProfile or WorkspaceFromPlanning.
	Found string `json:"found"`
	// Clone says the workspace is a git clone, so its folder for worktrees must be ignored. A plain folder
	// needs no check.
	Clone bool `json:"clone"`
	// Ignored says lane worktrees may go in the workspace's .ropeshift/worktrees/ folder: a plain folder, or a
	// clone whose repository ignores it.
	Ignored bool `json:"ignored"`
}

// The reasons a built-in root of a clone inside a workspace stays the root outside every clone.
const (
	// FallbackUnignored says the workspace does not ignore its folder for worktrees.
	FallbackUnignored = "unignored"
	// FallbackEscapes says a symbolic link leads the workspace's folder for worktrees, or the repository's folder
	// in it, out of the workspace, so a lane there would not be in the workspace.
	FallbackEscapes = "escapes"
	// FallbackUnreadable says the workspace could not be checked, as git cannot answer for one whose .ropeshift is
	// a link.
	FallbackUnreadable = "unreadable"
)

// RootFallback says why a lane's built-in root stayed the root outside every clone though the lane's clone lies in
// a workspace, for the lane's start notice and the doctor. The zero value says nothing fell back.
type RootFallback struct {
	// Workspace is the workspace's folder. Empty when it could not be found.
	Workspace string `json:"workspace,omitempty"`
	// Reason is one of the Fallback constants.
	Reason string `json:"reason,omitempty"`
	// Detail is what went wrong, for FallbackUnreadable.
	Detail string `json:"detail,omitempty"`
}

// Notice says, in a line, why the lane's root fell back and what to change. It is empty when nothing fell back.
func (f RootFallback) Notice() string {
	switch f.Reason {
	case FallbackUnignored:
		return UnignoredNotice(f.Workspace)
	case FallbackEscapes:
		return fmt.Sprintf("a symbolic link leads `%s/` of the workspace %s out of it, so this repository's lanes run outside the workspace; remove the link so its lanes run inside the workspace",
			planning.WorktreesFolder, f.Workspace)
	case FallbackUnreadable:
		return fmt.Sprintf("the project's workspace could not be checked (%s), so this repository's lanes run outside it until it can", f.Detail)
	}
	return ""
}

// UnignoredNotice is what a lane's start says when its built-in root fell back to the root outside every clone
// because the workspace does not ignore the folder for worktrees.
func UnignoredNotice(workspace string) string {
	return fmt.Sprintf("the workspace %s does not ignore `%s/`; add `%s` to its .gitignore so this repository's lanes run inside the workspace",
		workspace, planning.WorktreesFolder, planning.WorktreesLine)
}

// WorkspaceRoot is the built-in root of a clone inside a workspace: the workspace's folder for worktrees, by
// clone folder.
func WorkspaceRoot(workspace string) string {
	return path.Join(workspace, planning.WorktreesFolder, RepositoryToken)
}
