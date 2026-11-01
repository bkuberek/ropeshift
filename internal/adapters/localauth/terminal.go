package localauth

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"
)

// controllingTerminal is the process's own terminal, whatever its standard streams are.
const controllingTerminal = "/dev/tty"

// ErrNoTerminal means this process has no terminal to ask on.
var ErrNoTerminal = errors.New("no terminal to ask on")

// ReadPassphrase asks on the controlling terminal, with echo off. It is the only source of
// a passphrase: a pipe, a variable, and an argument can all be written by a lane, or read
// from a process list or a shell's history.
func ReadPassphrase(prompt string) (string, error) {
	tty, err := os.OpenFile(controllingTerminal, os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	defer tty.Close()
	if _, err := fmt.Fprint(tty, prompt); err != nil {
		return "", err
	}
	raw, err := term.ReadPassword(int(tty.Fd()))
	_, _ = fmt.Fprintln(tty)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	return string(raw), nil
}
