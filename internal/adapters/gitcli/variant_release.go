//go:build !chaos

package gitcli

// prunesBeforeCreate is true: a create always clears a dead registration first.
func prunesBeforeCreate() bool { return true }

// snapshotIndexVariable gives a snapshot an index of its own.
func snapshotIndexVariable(file string) []string { return []string{"GIT_INDEX_FILE=" + file} }

// snapshotAdd takes everything git would show, and leaves out what a repository ignores.
func snapshotAdd() []string { return []string{"add", "--all"} }

// pushArguments is one push, which never forces.
func pushArguments(remote, ref string) []string {
	return []string{"push", "--quiet", "--no-verify", remote, ref + ":" + ref}
}
