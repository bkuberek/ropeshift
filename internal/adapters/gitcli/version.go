package gitcli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// Probe reads the version of the git on PATH. Env is the environment git runs with, and nil is this process's.
type Probe struct {
	Env []string
}

var _ ports.GitProbe = Probe{}

// Version runs `git --version` and reads what it prints.
func (p Probe) Version(ctx context.Context) (lanes.GitVersion, error) {
	out, err := Git{Env: p.Env}.Text(ctx, "--version")
	if err != nil {
		return lanes.GitVersion{}, err
	}
	return ParseVersion(out)
}

// versionPrefix begins the line `git --version` prints, in every build.
const versionPrefix = "git version "

// ParseVersion reads the release from what `git --version` printed. It keeps the major, minor, and patch numbers and
// drops what a build adds after them: "2.45.1.windows.1", "2.39.5 (Apple Git-154)", "2.46.0.rc0", and
// "2.39.2+ds1" are 2.45.1, 2.39.5, 2.46.0, and 2.39.2. A release candidate reads as the release it leads to, because
// it holds the features that release's notes list. A line that names no major and minor number, or whose number runs
// into letters, is an error, so a program that is not git is never read as one.
func ParseVersion(output string) (lanes.GitVersion, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	rest, found := strings.CutPrefix(strings.TrimSpace(line), versionPrefix)
	if !found {
		return lanes.GitVersion{}, fmt.Errorf("git --version printed %q, which is no git version", clipVersion(output))
	}
	major, rest, ok := number(rest)
	if !ok || !strings.HasPrefix(rest, ".") {
		return lanes.GitVersion{}, fmt.Errorf("git --version printed %q, which names no major and minor number", clipVersion(output))
	}
	minor, rest, ok := number(rest[1:])
	if !ok {
		return lanes.GitVersion{}, fmt.Errorf("git --version printed %q, which names no major and minor number", clipVersion(output))
	}
	patch := 0
	if tail, dotted := strings.CutPrefix(rest, "."); dotted {
		if n, after, ok := number(tail); ok {
			patch, rest = n, after
		}
	}
	if rest != "" && isAlphanumeric(rest[0]) {
		return lanes.GitVersion{}, fmt.Errorf("git --version printed %q, whose version number runs into letters", clipVersion(output))
	}
	return lanes.GitVersion{Major: major, Minor: minor, Patch: patch}, nil
}

// number reads the decimal digits at the start of text, and what follows them. It reports false when there are none
// or they do not fit an int.
func number(text string) (int, string, bool) {
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, text, false
	}
	n, err := strconv.Atoi(text[:end])
	if err != nil {
		return 0, text, false
	}
	return n, text[end:], true
}

func isAlphanumeric(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// clipVersion keeps an error from quoting a long reply.
func clipVersion(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 80 {
		output = output[:80] + "..."
	}
	return output
}
