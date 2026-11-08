package localauth

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pseudo-terminal and returns both of its ends.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := master.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var name [128]byte
	var ioctlErr error
	err = conn.Control(func(fd uintptr) {
		for _, request := range []uintptr{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, 0); errno != 0 {
				ioctlErr = errno
				return
			}
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
			ioctlErr = errno
		}
	})
	if err != nil || ioctlErr != nil {
		t.Fatal(err, ioctlErr)
	}
	slave, err = os.OpenFile(string(name[:bytes.IndexByte(name[:], 0)]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}
