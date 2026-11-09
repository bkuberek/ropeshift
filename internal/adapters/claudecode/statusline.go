package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// The setting sources, as the CLI names them, from the lowest precedence up.
const (
	sourceUser    = "user"
	sourceProject = "project"
	sourceLocal   = "local"
)

// flagSettingSources limits what a session reads, so the line it composes with must come
// from those sources alone.
const flagSettingSources = "--setting-sources"

// statusLine is the setting of that name. Only `command` and these fields are read from
// a person's file, so nothing else in it passes through this process.
type statusLine struct {
	Type            string `json:"type"`
	Command         string `json:"command"`
	Padding         *int   `json:"padding,omitempty"`
	RefreshInterval *int   `json:"refreshInterval,omitempty"`
}

// settingSources are the sources a session reads, from the lowest precedence up.
func settingSources(spec ports.SessionSpec) []string {
	sources := []string{sourceUser, sourceProject, sourceLocal}
	extra := spec.Override.Args()
	for i := 0; i+1 < len(extra); i++ {
		if extra[i] == flagSettingSources {
			named := strings.Split(extra[i+1], ",")
			return slices.DeleteFunc(sources, func(source string) bool { return !slices.Contains(named, source) })
		}
	}
	return sources
}

func settingsPath(source, dir, home string) string {
	switch source {
	case sourceUser:
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".claude", "settings.json")
	case sourceProject:
		return filepath.Join(dir, ".claude", "settings.json")
	}
	return filepath.Join(dir, ".claude", "settings.local.json")
}

// existingStatusLine finds the line a person configured, in the CLI's precedence, because
// `--settings` replaces it and a person keeps the status line they set. A file that does
// not parse holds no line.
func existingStatusLine(spec ports.SessionSpec) *statusLine {
	home := ""
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			home = value
		}
	}
	var found *statusLine
	for _, source := range settingSources(spec) {
		path := settingsPath(source, spec.Dir, home)
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var file struct {
			StatusLine *statusLine `json:"statusLine"`
		}
		if json.Unmarshal(raw, &file) == nil && file.StatusLine != nil && file.StatusLine.Command != "" {
			found = file.StatusLine
		}
	}
	return found
}

// composedStatusLine forwards the input to the worker and then runs a person's own command
// on the same input, so it prints what they configured. The CLI runs the command through
// a shell, so a pipe composes the two and this binary starts no shell. The newline keeps
// a comment at the end of their command from closing the group.
func composedStatusLine(spec ports.SessionSpec) *statusLine {
	forward := lanes.ShellQuote(spec.Binary) + " statusline"
	out := &statusLine{Type: "command", Command: forward}
	existing := existingStatusLine(spec)
	if existing == nil {
		return out
	}
	out.Command = forward + " --pass | (" + existing.Command + "\n)"
	out.Padding, out.RefreshInterval = existing.Padding, existing.RefreshInterval
	return out
}
