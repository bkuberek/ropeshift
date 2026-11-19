//go:build screenshots

package screenshots

import "path/filepath"

// The clone the Best practices guide reads in the dry run of an import. No guide registers it, and it sits in a
// folder of its own, so the dry runs of guides 2 and 4 never see it. It holds eight findings of the practices
// check: an env file not ignored, no CI and no pre-commit configuration, a go.mod that requires a module with no
// go.sum, no dependency update configuration, no SECURITY.md, no CODEOWNERS, and no tests.

// practices lays out the clone the Best practices guide dry-runs an import of, and answers the variable it adds to
// the world's.
func (h *harbor) practices() map[string]string {
	h.in.t.Helper()
	clone := h.in.newCloneAt(filepath.Join(guidesFolder, "lighthouse", "lighthouse-api"), harborRemoteBase, lighthouseAPIFiles())
	return map[string]string{"lighthouse": clone.Dir}
}

// lighthouseAPIFiles is the API of Lighthouse, which tells boats the state of the harbor's lights, before any
// practice is met.
func lighthouseAPIFiles() map[string]string {
	return map[string]string{
		"README.md": lines("# lighthouse-api", "", "The state of the harbor's lights, for the boats that come in at night."),
		"go.mod":    "module example.com/lighthouse-api\n\ngo 1.24\n\nrequire example.com/beacon v1.0.0\n",
		"internal/lights/lights.go": lines("package lights", "",
			"// State answers whether each light of the harbor is lit.", "func State() {}"),
	}
}
