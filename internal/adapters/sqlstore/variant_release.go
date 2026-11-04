//go:build !chaos

package sqlstore

// open starts a write transaction: the revision first, then the clock.
func (t *writeTx) open() error { return t.openRevisionFirst() }

// rewritten returns a statement's text.
func rewritten(_, text string) string { return text }

// commitsWithoutEvents is false: a transaction with no event never commits.
func commitsWithoutEvents() bool { return false }
