package localauth

import (
	"os"
	"strconv"
	"syscall"
	"testing"

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
	var number uint32
	var ioctlErr error
	err = conn.Control(func(fd uintptr) {
		if ioctlErr = unix.IoctlSetPointerInt(int(fd), unix.TIOCSPTLCK, 0); ioctlErr != nil {
			return
		}
		number, ioctlErr = unix.IoctlGetUint32(int(fd), unix.TIOCGPTN)
	})
	if err != nil || ioctlErr != nil {
		t.Fatal(err, ioctlErr)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}
