//go:build chaos

package sqlstore

import "sync/atomic"

// Sabotage is a broken variant of the adapter, used only to prove that a check
// can fail. The chaos build holds these seams, and a normal build has none.
type Sabotage struct {
	// Rewrite replaces a statement's text.
	Rewrite func(name, text string) string
	// RevisionLast takes the revision when the first event is appended, after
	// every read in the transaction.
	RevisionLast bool
	// CommitEmpty commits a transaction that appended no event.
	CommitEmpty bool
}

var sabotage atomic.Pointer[Sabotage]

// Sabotaged installs a variant for this process.
func Sabotaged(s Sabotage) { sabotage.Store(&s) }

func (t *writeTx) open() error {
	if s := sabotage.Load(); s != nil && s.RevisionLast {
		var err error
		t.now, err = t.readClock()
		return err
	}
	return t.openRevisionFirst()
}

func rewritten(name, text string) string {
	if s := sabotage.Load(); s != nil && s.Rewrite != nil {
		return s.Rewrite(name, text)
	}
	return text
}

func commitsWithoutEvents() bool {
	s := sabotage.Load()
	return s != nil && s.CommitEmpty
}
