package templates_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/templates"
)

// The manifest holds each remote as a TOML string whatever the remote is, so a caller that skips
// projectfiles.Check still gets a manifest that parses, with the one repository it was given and its remote
// as it was.
func TestTheManifestHoldsAnyRemoteAsOneTOMLString(t *testing.T) {
	remotes := map[string]string{
		"a plain URL":          "https://example.invalid/orchid/orchid-api.git",
		"a double quote":       `https://example.invalid/a".git`,
		"a table injected":     "x\"\n[[repository]]\nname = \"injected\"\nremote = \"y",
		"a newline":            "https://example.invalid/a\nb.git",
		"a carriage return":    "https://example.invalid/a\rb.git",
		"a tab":                "https://example.invalid/a\tb.git",
		"a backslash":          `https://example.invalid/a\b.git`,
		"a trailing backslash": `https://example.invalid/a\`,
		"a unicode escape":     `https://example.invalid/A.git`,
		"a control character":  "https://example.invalid/a\x01\x1f\x7fb.git",
		"a NUL":                "https://example.invalid/a\x00b.git",
		"a non-ASCII path":     "https://example.invalid/café/日本.git",
		"a shell command":      "https://example.invalid/$(touch pwned)`id`.git",
		"a single quote":       "https://example.invalid/a'b.git",
	}
	for name, remote := range remotes {
		t.Run(name, func(t *testing.T) {
			data := templates.Data{Project: "orchid", Repositories: []templates.Repository{{Name: "orchid-api", Remote: remote}}}
			manifest, err := repofiles.ParseManifest([]byte(render(t, "workspace/plan/repositories.md", data)))
			if err != nil {
				t.Fatalf("the manifest does not parse: %v", err)
			}
			if len(manifest.Repositories) != 1 || manifest.Repositories[0].Name != "orchid-api" || manifest.Repositories[0].Remote != remote {
				t.Errorf("the manifest holds %+v, want orchid-api with the remote %q", manifest.Repositories, remote)
			}
		})
	}
}
