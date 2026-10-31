//go:build !chaos

package gitcli

// prunesBeforeCreate is true: a create always clears a dead registration first.
func prunesBeforeCreate() bool { return true }
