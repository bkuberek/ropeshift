// Package launchd is the service manager on macOS: a user agent that runs the daemon, and the
// launchctl calls that load and remove it.
package launchd

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Agent is a launchd user agent that runs one program.
type Agent struct {
	// Label names the agent in its domain, and its file.
	Label string
	// Program is the absolute path of the program, then its arguments.
	Program []string
	// Environment is NAME=value, in the order it is written.
	Environment []string
	// Log is the absolute file where the program's output goes.
	Log string
}

// header is what every property list starts with.
const header = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by ropeshift service install. Lanes start in process sessions of their own, so
     launchd ends no lane when it stops this agent. -->
<plist version="1.0">
<dict>
`

// Render writes the agent's property list. The agent runs as interactive work, because launchd
// throttles CPU and I/O of a job with no process class. launchd starts it at load, restarts it
// after a crash, and leaves it stopped after a clean exit.
func (a Agent) Render() ([]byte, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(header)
	key(&b, 1, "Label")
	str(&b, 1, a.Label)
	key(&b, 1, "ProgramArguments")
	b.WriteString("\t<array>\n")
	for _, arg := range a.Program {
		str(&b, 2, arg)
	}
	b.WriteString("\t</array>\n")
	if len(a.Environment) > 0 {
		key(&b, 1, "EnvironmentVariables")
		b.WriteString("\t<dict>\n")
		for _, entry := range a.Environment {
			name, value, _ := strings.Cut(entry, "=")
			key(&b, 2, name)
			str(&b, 2, value)
		}
		b.WriteString("\t</dict>\n")
	}
	key(&b, 1, "ProcessType")
	str(&b, 1, "Interactive")
	key(&b, 1, "RunAtLoad")
	b.WriteString("\t<true/>\n")
	key(&b, 1, "KeepAlive")
	b.WriteString("\t<dict>\n")
	key(&b, 2, "SuccessfulExit")
	b.WriteString("\t\t<false/>\n\t</dict>\n")
	key(&b, 1, "StandardOutPath")
	str(&b, 1, a.Log)
	key(&b, 1, "StandardErrorPath")
	str(&b, 1, a.Log)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}

// check refuses what a property list cannot hold, or what launchd would not run.
func (a Agent) check() error {
	if a.Label == "" {
		return errors.New("an agent needs a label")
	}
	if len(a.Program) == 0 || !path.IsAbs(a.Program[0]) {
		return fmt.Errorf("an agent runs an absolute path: %q", a.Program)
	}
	if !path.IsAbs(a.Log) {
		return fmt.Errorf("an agent's log is an absolute path: %q", a.Log)
	}
	words := append([]string{a.Label, a.Log}, a.Program...)
	for _, entry := range a.Environment {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return fmt.Errorf("a variable needs a name and a value: %q", entry)
		}
		words = append(words, entry)
	}
	for _, word := range words {
		if strings.ContainsFunc(word, unicode.IsControl) {
			return fmt.Errorf("a property list cannot hold %q", word)
		}
	}
	return nil
}

func key(b *bytes.Buffer, depth int, name string) {
	element(b, depth, "key", name)
}

func str(b *bytes.Buffer, depth int, value string) {
	element(b, depth, "string", value)
}

func element(b *bytes.Buffer, depth int, tag, text string) {
	b.WriteString(strings.Repeat("\t", depth) + "<" + tag + ">")
	_ = xml.EscapeText(b, []byte(text))
	b.WriteString("</" + tag + ">\n")
}
