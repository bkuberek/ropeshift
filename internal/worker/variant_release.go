//go:build !chaos

package worker

// chargeKey is the key: a normal build charges one act once.
func chargeKey(key string) string { return key }

// skipsGuard is false: a normal build reads every worktree.
func skipsGuard() bool { return false }

// keepsResources is false: a normal build gives every block back.
func keepsResources() bool { return false }

// removesBlind is false: a normal build reads a worktree before it removes one.
func removesBlind() bool { return false }
