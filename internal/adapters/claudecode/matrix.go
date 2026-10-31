package claudecode

import (
	"slices"
	"strings"
)

// qualifiedVersions lists the versions the qualification matrix pins. A version
// outside it runs in degraded mode, on documented surfaces only.
var qualifiedVersions = []string{"2.1.316"}

// Qualified reports a version the matrix holds.
func Qualified(version string) bool { return slices.Contains(qualifiedVersions, version) }

// parseVersion reads the first word of `claude --version`.
func parseVersion(output string) string {
	version, _, _ := strings.Cut(strings.TrimSpace(output), " ")
	return version
}
