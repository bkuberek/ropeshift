//go:build !chaos

package claudecode

// sabotagedFocus is the focus a screen shows: a normal build acts on nothing else.
func sabotagedFocus(focus focusLine) focusLine { return focus }

// repeatsBrief is false: a brief that may have arrived is never sent again.
func repeatsBrief() bool { return false }
