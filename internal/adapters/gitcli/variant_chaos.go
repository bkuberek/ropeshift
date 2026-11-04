//go:build chaos

package gitcli

import "sync/atomic"

// Sabotage is a broken variant of the adapter, used only to prove that a check can
// fail. The chaos build holds this seam, and a normal build has none.
type Sabotage struct {
	// SkipPrune leaves a dead registration in place before a create.
	SkipPrune bool
	// TrackedOnly leaves untracked files out of a snapshot.
	TrackedOnly bool
	// ForcePush forces every push.
	ForcePush bool
	// OwnIndex builds a snapshot in the lane's own index.
	OwnIndex bool
}

var sabotage atomic.Pointer[Sabotage]

// Sabotaged installs a variant for this process.
func Sabotaged(s Sabotage) { sabotage.Store(&s) }

func prunesBeforeCreate() bool {
	s := sabotage.Load()
	return s == nil || !s.SkipPrune
}

// snapshotIndexVariable is how a snapshot names its index. The broken variant names none,
// so it builds in the lane's own.
func snapshotIndexVariable(file string) []string {
	if s := sabotage.Load(); s != nil && s.OwnIndex {
		return nil
	}
	return []string{"GIT_INDEX_FILE=" + file}
}

// snapshotAdd is what a snapshot adds. The broken variant takes only what git tracks, as
// `git stash create` does.
func snapshotAdd() []string {
	if s := sabotage.Load(); s != nil && s.TrackedOnly {
		return []string{"add", "--update"}
	}
	return []string{"add", "--all"}
}

// pushArguments is one push. The broken variant forces it.
func pushArguments(remote, ref string) []string {
	args := []string{"push", "--quiet", "--no-verify", remote, ref + ":" + ref}
	if s := sabotage.Load(); s != nil && s.ForcePush {
		return append(args, "--force")
	}
	return args
}
