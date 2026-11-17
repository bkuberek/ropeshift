package entry

import (
	"io"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
)

// The planning brief shows the manifest as the templates render it with the example data, and names the
// directory intake reads, which is the daemon's --dir. The worker never imports the templates, so the entry
// package renders the manifest once and hands it over, with the directory.
func TestTheLaneRuntimeCarriesTheManifestAndTheIntakeDirectoryForTheBrief(t *testing.T) {
	root := apitest.ShortDir(t)
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "worktrees")}
	want, err := templates.Render("workspace/plan/repositories.md", templates.Data{Example: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"", "plans"} {
		runtime, _, err := soloLanes(paths, soloOptions{directory: directory}, soloCheckouts(paths), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.ManifestTemplate != want {
			t.Errorf("the manifest template is %d bytes, want the %d bytes the templates render", len(runtime.ManifestTemplate), len(want))
		}
		if runtime.IntakeDirectory != directory {
			t.Errorf("the intake directory is %q, want the daemon's --dir %q", runtime.IntakeDirectory, directory)
		}
	}
}
