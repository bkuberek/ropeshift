package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/practices"
)

// PracticeSnapshots reads a checkout at one commit for the practices check. It runs only read-only git commands.
type PracticeSnapshots interface {
	// Snapshot returns the tracked paths at a commit, a reader of a tracked file's text, and the ignore answer
	// for each path of practices.Probes, by the `.gitignore` files committed at that commit. The commit is a
	// commit ID, or the name of the base branch, which the adapter resolves to its tip; the snapshot names the
	// commit it read.
	Snapshot(ctx context.Context, dir, commit string) (practices.Snapshot, error)
}

// SecretScan is what a scan of a repository for secrets found.
type SecretScan struct {
	Matches []practices.SecretMatch
	// ScannedFrom is the commit an incremental scan started after, empty for a scan of the whole history.
	ScannedFrom string
	// Skipped counts the files skipped for their size or binary content.
	Skipped int
}

// SecretScans scans the tracked files at a commit and the history reachable from it for secrets (FR-PRAC-4).
type SecretScans interface {
	// Scan scans the tree at tip, and the history reachable from it that is not reachable from `from`, when
	// `from` is an ancestor of tip; otherwise the whole history. An empty `from` scans the whole history. No
	// match, and no error, holds the matched text.
	Scan(ctx context.Context, dir, tip, from string) (SecretScan, error)
}

// ForgeSettings reads a repository's settings on its forge for the practices check, under the person's existing
// sign-in. A setting it cannot read is left unknown in the facts with its reason, and is no error.
type ForgeSettings interface {
	Settings(ctx context.Context, repo ForgeRepo, branch string) (practices.ForgeFacts, error)
}

// Practices is the set of ports a practices check reads through.
type Practices interface {
	PracticeSnapshots
	SecretScans
	ForgeSettings
}
