package doubles

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ptyNameSize is the buffer TIOCPTYGNAME fills.
const ptyNameSize = 128

// openPTY opens the master and the terminal end of a new pair.
func openPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := master.Fd()
	for _, request := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if err := unix.IoctlSetInt(int(fd), request, 0); err != nil {
			_ = master.Close()
			return nil, nil, err
		}
	}
	name := make([]byte, ptyNameSize)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		_ = master.Close()
		return nil, nil, errno
	}
	end := 0
	for end < len(name) && name[end] != 0 {
		end++
	}
	terminal, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	size := &unix.Winsize{Row: ptyRows, Col: ptyColumns}
	if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, size); err != nil {
		_ = master.Close()
		_ = terminal.Close()
		return nil, nil, err
	}
	return master, terminal, nil
}
