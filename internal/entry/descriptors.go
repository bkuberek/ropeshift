package entry

import (
	"os"
	"strconv"
	"syscall"
)

// fallbackDescriptors is how many descriptors above 2 are tried when the directory that lists the open ones
// cannot be read.
const fallbackDescriptors = 1024

// keepDescriptorsFromChildren marks every file descriptor above 2 that this process holds close-on-exec, so no
// process it starts afterwards inherits one. Go opens every file close-on-exec, so what is left is what this
// process was started with, such as the terminal a test harness or a shell passed down on a descriptor of its
// own. A daemon that handed it on would hand it to the terminal server, which outlives the daemon and then
// holds that terminal open for as long as it runs, whatever ended the process that owned it.
//
// The process still uses what it holds: the mark takes effect only when a child is started.
func keepDescriptorsFromChildren() {
	for _, fd := range openDescriptors() {
		if fd > syscall.Stderr {
			syscall.CloseOnExec(fd)
		}
	}
}

// openDescriptors lists the descriptors this process holds, as the operating system's directory of them says,
// and the first fallbackDescriptors otherwise. The directory's own descriptor is among them, and marking it is
// harmless: it is closed before the mark is made, or marked with the rest.
func openDescriptors() []int {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		all := make([]int, 0, fallbackDescriptors)
		for fd := range fallbackDescriptors {
			all = append(all, fd)
		}
		return all
	}
	fds := make([]int, 0, len(entries))
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil {
			fds = append(fds, fd)
		}
	}
	return fds
}
