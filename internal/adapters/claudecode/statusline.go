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

// sourceProject is the one setting source a session reads, as the CLI names it. The person's own `user` and
// `local` sources are never read (laneSources).
const sourceProject = "project"

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

// settingSources are the sources a session reads, from the lowest precedence up: the lane's own, which a
// real-session suite's list can narrow.
func settingSources(spec ports.SessionSpec) []string {
	sources := slices.Clone(laneSources)
	extra := spec.Override.Args()
	for i := 0; i+1 < len(extra); i++ {
		if extra[i] == flagSettingSources {
			named := strings.Split(extra[i+1], ",")
			return slices.DeleteFunc(sources, func(source string) bool { return !slices.Contains(named, source) })
		}
	}
	return sources
}

// existingStatusLine finds the line the repository's shared settings configure, because `--settings`
// replaces it and a repository keeps the status line it set. The person's own line is in their user or
// local settings, which a session does not read, and a suite that names no project source reads none. A file
// that does not parse holds no line.
func existingStatusLine(spec ports.SessionSpec) *statusLine {
	if !slices.Contains(settingSources(spec), sourceProject) {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(spec.Dir, ".claude", "settings.json"))
	if err != nil {
		return nil
	}
	var file struct {
		StatusLine *statusLine `json:"statusLine"`
	}
	if json.Unmarshal(raw, &file) != nil || file.StatusLine == nil || file.StatusLine.Command == "" {
		return nil
	}
	return file.StatusLine
}

// composedStatusLine forwards the input to the worker and then runs the repository's own
// command on the same input, so it prints what the repository configured. The CLI runs the
// command through a shell, so a pipe composes the two and this binary starts no shell. The
// newline keeps a comment at the end of that command from closing the group.
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
