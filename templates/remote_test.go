package templates_test

import (
	"strings"
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

// A repository with no remote has no remote key in the manifest and name= in the Makefile, so approval registers
// the clone whatever its remote is, and make setup never clones from an empty address.
func TestARepositoryWithNoRemoteHasNoRemoteKeyAndAnEmptyMakefileRemote(t *testing.T) {
	data := templates.Data{Project: "orchid", Repositories: []templates.Repository{
		{Name: "orchid-api", Remote: "git@example.invalid:orchid/orchid-api.git"}, {Name: "orchid-web"},
	}}
	text := render(t, "workspace/plan/repositories.md", data)
	manifest, err := repofiles.ParseManifest([]byte(text))
	if err != nil {
		t.Fatalf("the manifest does not parse: %v", err)
	}
	if len(manifest.Repositories) != 2 || manifest.Repositories[0].Remote == "" || manifest.Repositories[1].Name != "orchid-web" || manifest.Repositories[1].Remote != "" {
		t.Errorf("the manifest holds %+v", manifest.Repositories)
	}
	if strings.Count(text, "remote = ") != 1 {
		t.Errorf("a repository with no remote has a remote key:\n%s", text)
	}
	makefile := render(t, "workspace/makefile", data)
	for _, want := range []string{"orchid-api=git@example.invalid:orchid/orchid-api.git", "orchid-web=\n", `elif [ -z "$$remote" ]; then echo "$$name: no remote and no clone"; exit 1;`} {
		if !strings.Contains(makefile, want) {
			t.Errorf("the Makefile lacks %q", want)
		}
	}
}
