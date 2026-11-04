package exit

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd, request uintptr, value unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(value)); errno != 0 {
		return errno
	}
	return nil
}

// openPair opens a terminal: the end a person types on, and the end a process holds.
func openPair() (*os.File, *os.File, error) {
	typed, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlocked int32
	var number uint32
	if err := ioctl(typed.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlocked)); err != nil {
		_ = typed.Close()
		return nil, nil, err
	}
	if err := ioctl(typed.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&number)); err != nil {
		_ = typed.Close()
		return nil, nil, err
	}
	held, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = typed.Close()
		return nil, nil, err
	}
	return typed, held, nil
}
