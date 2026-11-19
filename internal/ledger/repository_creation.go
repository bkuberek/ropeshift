package ledger

import (
	"crypto/sha256"
	"encoding/hex"
)

// RepositoryCreation is one repository a plan approval creates on the worker's machine.
type RepositoryCreation struct {
	// Key is the creation's journal name, from the approval and the entry name.
	Key string `json:"key"`
	// Project is the project's name, rendered and used in the first commit's message.
	Project string `json:"project"`
	// Name is the manifest's name for the repository.
	Name string `json:"name"`
	// Path is absolute and clean: the planning clone's root joined with the manifest path.
	Path string `json:"path"`
	// Bound is the resolved directory that holds the planning repository's clone.
	Bound string `json:"bound"`
	// PlanningClone is the resolved planning clone, which the path may not be.
	PlanningClone string `json:"planning_clone"`
	// Create is the forge, owner, and visibility of the repository to create.
	Create        RemoteCreate `json:"create"`
	ForgeName     string       `json:"forge_name"`
	DefaultBranch string       `json:"default_branch,omitempty"`
}

// CreationCheck is what the checks that come before a creation's first write found.
type CreationCheck struct {
	// Refusal is the report of the check that failed, or nil when every check passed.
	Refusal *TemplateWriteReport
	// Leads is where the path leads, every link followed: the folder the creation writes, which the preview shows.
	// It is empty only when the check refused before it read the path.
	Leads string
	// URL is the origin the creation adds, as gh's git protocol names it.
	URL   string
	Login string
	// Branch is the branch the first commit starts on.
	Branch string
}

// CreationOutcome is what a creation did, as a plan approval records it.
type CreationOutcome struct {
	Forge         string       `json:"forge"`
	Owner         string       `json:"owner"`
	ForgeName     string       `json:"forge_name"`
	Visibility    string       `json:"visibility"`
	DefaultBranch string       `json:"default_branch,omitempty"`
	URL           string       `json:"url,omitempty"`
	Login         string       `json:"login,omitempty"`
	Commit        string       `json:"commit,omitempty"`
	CommitError   string       `json:"commit_error,omitempty"`
	Steps         []RemoteStep `json:"steps,omitempty"`
	Remaining     []string     `json:"remaining,omitempty"`
	Remedy        string       `json:"remedy,omitempty"`
	// Makefile is the REPOSITORIES entry to add to the workspace Makefile, name=<url>=<manifest path>, set once the
	// repository is created.
	Makefile string `json:"makefile,omitempty"`
}

// CreationKey is the name of a creation's journal: the SHA-256 of the approval's ID and the entry's name, in lowercase
// hex. The same approval and name give the same key, and another of either gives another.
func CreationKey(approval, name string) string {
	sum := sha256.Sum256([]byte(approval + "\x00" + name))
	return hex.EncodeToString(sum[:])
}
