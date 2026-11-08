// Package ptypair opens a terminal for a test that types at a prompt, as a person does: the exit tests,
// and the first run they start inside a Linux container.
package ptypair

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// The requests a darwin terminal pair takes, from its <sys/ttycom.h>.
const (
	tiocPtyGrant = 0x20007454
	tiocPtyUnlk  = 0x20007452
	tiocPtyGName = 0x40807453
)

func ioctl(fd, request, value uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, value); errno != 0 {
		return errno
	}
	return nil
}

// Open opens a terminal: the end a person types on, and the end a process holds.
func Open() (typed, held *os.File, err error) {
	typed, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var name [128]byte
	err = errors.Join(
		ioctl(typed.Fd(), tiocPtyGrant, 0),
		ioctl(typed.Fd(), tiocPtyUnlk, 0),
		ioctl(typed.Fd(), tiocPtyGName, uintptr(unsafe.Pointer(&name[0]))),
	)
	if err != nil {
		_ = typed.Close()
		return nil, nil, err
	}
	end := bytes.IndexByte(name[:], 0)
	held, err = os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = typed.Close()
		return nil, nil, err
	}
	return typed, held, nil
}
