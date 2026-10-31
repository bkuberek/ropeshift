//go:build chaos

package gitcli

import "sync/atomic"

// Sabotage is a broken variant of the adapter, used only to prove that a check can
// fail. The chaos build holds this seam, and a normal build has none.
type Sabotage struct {
	// SkipPrune leaves a dead registration in place before a create.
	SkipPrune bool
}

var sabotage atomic.Pointer[Sabotage]

// Sabotaged installs a variant for this process.
func Sabotaged(s Sabotage) { sabotage.Store(&s) }

func prunesBeforeCreate() bool {
	s := sabotage.Load()
	return s == nil || !s.SkipPrune
}
