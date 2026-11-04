//go:build darwin

package localauth

import "golang.org/x/sys/unix"

// The requests that read and set a terminal's modes.
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
