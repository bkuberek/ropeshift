//go:build !chaos

package worker

import "strconv"

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

// staysCompacting is false: a lane that parks from a compaction leaves it, whichever process goes on.
func staysCompacting() bool { return false }

// splitsPark is false: a normal build keeps a parked commit and its upload together.
func splitsPark() bool { return false }

// splitsResume is false: a normal build keeps a resume and its upload together.
func splitsResume() bool { return false }

// leavesParked is false: a normal build moves a lane's state with its resume.
func leavesParked() bool { return false }

// splitsMessage is false: a normal build keeps a message's write and what it proves together.
func splitsMessage() bool { return false }

// dropsFacts is false: a generation's first prompt states what it inherited.
func dropsFacts() bool { return false }

// splitsSignal is false: a normal build keeps a limit's end and its signal together.
func splitsSignal() bool { return false }

// forgetsLimit is false: what a worker knows of a sign-in outlives its process.
func forgetsLimit() bool { return false }

// messageKey is one key per message, so a restart cannot write one twice.
func messageKey(id string) string { return messageLabel + id }

// instructionLabel is the one label, so a generation sends its instruction once.
func instructionLabel() string { return handoffInput }

// holdsNoTools is false: a running turn is held before it is told.
func holdsNoTools() bool { return false }

// splitsClear stands for no write that could leave a clear the ledger never hears of.
func splitsClear() bool { return false }

// splitsApproval stands for no write that could leave a request the ledger never hears of.
func splitsApproval() bool { return false }

// clearedKey is one key per generation, so a restart does not tell a session its work twice.
func clearedKey() string { return clearedInput }

// compactionKey is one key per compaction, so a restart sends neither of its inputs twice.
func compactionKey(label string, n int64) string { return label + strconv.FormatInt(n, 10) }
