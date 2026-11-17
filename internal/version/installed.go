package version

import (
	"cmp"
	"strconv"
	"strings"
)

// Installed is the build found on disk at a daemon's own executable path, which a package manager
// replaces while the daemon keeps running the build it started from.
type Installed struct {
	// Path is where the build was read.
	Path string
	// Version is the release that build reports. It is empty when the build could not be read.
	Version string
}

// Compare orders two releases by semantic versioning precedence: negative when a is the earlier, zero
// when they are the same, and positive when a is the later. It reports false when either is not a
// release, as a development build and an unreadable one are not.
func Compare(a, b string) (int, bool) {
	left, ok := parseRelease(a)
	if !ok {
		return 0, false
	}
	right, ok := parseRelease(b)
	if !ok {
		return 0, false
	}
	for i := range left.core {
		if order := cmp.Compare(left.core[i], right.core[i]); order != 0 {
			return order, true
		}
	}
	return comparePreRelease(left.pre, right.pre), true
}

type parsedRelease struct {
	core [3]uint64
	pre  []string
}

func parseRelease(v string) (parsedRelease, bool) {
	var out parsedRelease
	if !isRelease(v) {
		return out, false
	}
	core, pre, hasPre := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, part := range strings.Split(core, ".") {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parsedRelease{}, false
		}
		out.core[i] = number
	}
	if hasPre {
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

// comparePreRelease applies the precedence rules for pre-release identifiers: a release is later than any
// of its pre-releases, numeric identifiers compare as numbers and are earlier than alphanumeric ones, and
// the longer list is later when every identifier before it is the same.
func comparePreRelease(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < min(len(a), len(b)); i++ {
		left, leftErr := strconv.ParseUint(a[i], 10, 64)
		right, rightErr := strconv.ParseUint(b[i], 10, 64)
		var order int
		switch {
		case leftErr == nil && rightErr == nil:
			order = cmp.Compare(left, right)
		case leftErr == nil:
			order = -1
		case rightErr == nil:
			order = 1
		default:
			order = strings.Compare(a[i], b[i])
		}
		if order != 0 {
			return order
		}
	}
	return cmp.Compare(len(a), len(b))
}

// brewServicePath is the path element of the opt link that `brew services` runs the daemon through. A
// service that `ropeshift service install` writes names the binary by another path, even inside a Homebrew
// prefix, and `brew services` does not manage it.
const brewServicePath = "/opt/ropeshift/bin/"

func homebrew(path string) bool { return strings.Contains(path, brewServicePath) }

// IsRelease reports whether v is a release version, as a build reports it.
func IsRelease(v string) bool { return isRelease(v) }

// Warning says that a running daemon is older than the build installed at its path, and how to run the
// installed one. A daemon that reports no version predates the report, so it is older than any release.
// The warning is empty when the daemon is as new as the installed build or newer, when either is not a
// release, and when the installed build could not be read.
func Warning(running string, installed Installed) string {
	if running == "" {
		if !isRelease(installed.Version) {
			return ""
		}
		return stale(installed, "an older build")
	}
	order, ok := Compare(running, installed.Version)
	if !ok || order >= 0 {
		return ""
	}
	return stale(installed, running)
}

func stale(installed Installed, running string) string {
	fix := "Restart the ropeshift service to run it."
	if homebrew(installed.Path) {
		fix = "Run brew services restart ropeshift to run it."
	}
	return "ropeshift " + installed.Version + " is installed, but the daemon is still running " + running + ". " + fix
}
