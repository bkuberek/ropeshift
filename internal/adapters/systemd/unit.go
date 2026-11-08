package systemd

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Unit is a systemd user service that runs one program.
type Unit struct {
	Description string
	// Command is the absolute path of the program, then its arguments.
	Command []string
	// Environment is NAME=value, in the order it is written.
	Environment []string
}

// note heads every unit, for a person who reads the file.
const note = `# Written by ropeshift service install. Lanes and the worker's terminal server start in scopes
# of their own, so a stop of this unit ends no lane.
`

// Render writes the unit file. systemd restarts the daemon after a crash, and never after a usage
// error, which no restart mends. Each word is quoted, so a space, a specifier, a variable, a quote,
// or a backslash reaches the daemon as it was written.
func (u Unit) Render() ([]byte, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(note)
	b.WriteString("[Unit]\nDescription=" + strings.ReplaceAll(u.Description, "%", "%%") + "\n\n[Service]\n")
	words := make([]string, 0, len(u.Command))
	for _, word := range u.Command {
		words = append(words, quote(word, true))
	}
	b.WriteString("ExecStart=" + strings.Join(words, " ") + "\n")
	for _, entry := range u.Environment {
		b.WriteString("Environment=" + quote(entry, false) + "\n")
	}
	b.WriteString("Restart=on-failure\nRestartSec=5\nRestartPreventExitStatus=2\n\n[Install]\nWantedBy=default.target\n")
	return b.Bytes(), nil
}

// check refuses what a unit file cannot hold, or what systemd would not run.
func (u Unit) check() error {
	if len(u.Command) == 0 || !path.IsAbs(u.Command[0]) {
		return fmt.Errorf("a unit runs an absolute path: %q", u.Command)
	}
	for _, entry := range u.Environment {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return fmt.Errorf("a variable needs a name and a value: %q", entry)
		}
	}
	for _, word := range append(append([]string{u.Description}, u.Command...), u.Environment...) {
		if strings.ContainsFunc(word, unicode.IsControl) {
			return fmt.Errorf("a unit file cannot hold %q", word)
		}
	}
	if u.Description == "" {
		return errors.New("a unit needs a description")
	}
	return nil
}

// quote makes one word of a unit's line. Inside double quotes, systemd reads C escapes, and it
// expands specifiers everywhere and variables on a command line.
func quote(word string, command bool) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range word {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '%':
			b.WriteString("%%")
		case r == '$' && command:
			b.WriteString("$$")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
