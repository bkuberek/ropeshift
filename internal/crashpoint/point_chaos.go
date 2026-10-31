//go:build chaos

package crashpoint

import "sync/atomic"

var handler atomic.Pointer[func(op, step string)]

// Arm installs the function a chaos child runs at every named step.
func Arm(fn func(op, step string)) {
	handler.Store(&fn)
}

// Hit calls the armed function, if there is one.
func Hit(op, step string) {
	if fn := handler.Load(); fn != nil {
		(*fn)(op, step)
	}
}
