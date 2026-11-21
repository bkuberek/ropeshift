package ptypair

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// Descriptor is where Controlling gives a command its terminal for the moment its session takes it: the first of its
// extra files.
const Descriptor = 3

// Controlling makes held the controlling terminal of cmd, in a session of its own, and leaves cmd no descriptor for it.
//
// The command runs behind a shell that closes the descriptor and then replaces itself with the command. The new
// session takes the terminal as its controlling terminal while the descriptor is open, and keeps it after the
// descriptor is closed, which is all a program that asks for a passphrase on /dev/tty needs. A program that kept the
// descriptor would hand it to everything it starts, and a daemon's terminal server outlives the daemon and the
// command that started it, and holds the terminal's slave side open for as long as it runs.
func Controlling(cmd *exec.Cmd, held *os.File) {
	cmd.ExtraFiles = []*os.File{held}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: Descriptor}
	program := cmd.Path
	cmd.Path = "/bin/sh"
	cmd.Args = append([]string{"sh", "-c", `exec ` + strconv.Itoa(Descriptor) + `<&-; exec "$0" "$@"`, program}, cmd.Args[1:]...)
}
