package ptypair_test

import (
	"bytes"
	"os/exec"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/test/exit/ptypair"
)

// A command that is given a terminal has it as its controlling terminal, which is all ropeshift init and login need
// to read a passphrase, and holds no descriptor for it, so no process the command starts holds the terminal open
// once the command ends. A terminal server that outlives the command would otherwise keep the terminal's slave
// side open for as long as it runs.
func TestACommandOnATerminalHasItAsItsControllingTerminalAndHoldsNoDescriptorForIt(t *testing.T) {
	typed, held, err := ptypair.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = typed.Close(); _ = held.Close() })
	script := "if ( : >&3 ) 2>/dev/null; then echo descriptor open; else echo descriptor closed; fi\n" +
		"if ( : >/dev/tty ) 2>/dev/null; then echo terminal; else echo no terminal; fi\n"
	args := []string{"/bin/sh", "-c", script}
	cmd := exec.Command(args[0], args[1:]...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	ptypair.Controlling(cmd, held)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if want := "descriptor closed\nterminal\n"; stdout.String() != want {
		t.Errorf("the command saw %q, want %q", stdout.String(), want)
	}
	if !slices.Equal(cmd.Args[len(cmd.Args)-len(args):], args) {
		t.Errorf("the command's arguments end %q, want the command it was asked to run, %q", cmd.Args, args)
	}
}
