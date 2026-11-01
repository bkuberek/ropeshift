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

// storesAgain is false: a normal build stores a handoff once.
func storesAgain() bool { return false }

// forgetsHandoff is false: a normal build finishes a handoff that lost its session.
func forgetsHandoff() bool { return false }

// splitsHandoff is false: a normal build keeps a document and its upload together.
func splitsHandoff() bool { return false }

// parksFirst is false: a normal build stops a process before it gives its lease up.
func parksFirst() bool { return false }

// forgetsWake is false: a normal build finishes parking that lost its session.
func forgetsWake() bool { return false }

// splitsPark is false: a normal build keeps a parked commit and its upload together.
func splitsPark() bool { return false }

// splitsResume is false: a normal build keeps a resume and its upload together.
func splitsResume() bool { return false }

// leavesParked is false: a normal build moves a lane's state with its resume.
func leavesParked() bool { return false }

// instructionLabel is the one label, so a generation sends its instruction once.
func instructionLabel() string { return handoffInput }
