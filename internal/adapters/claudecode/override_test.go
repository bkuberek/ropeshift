//go:build realsession

package claudecode

import (
	"slices"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

func overrideSpec(t *testing.T, override string) ports.SessionSpec {
	t.Helper()
	parsed, err := lanes.ParseOverride(override)
	if err != nil {
		t.Fatal(err)
	}
	return ports.SessionSpec{PermissionMode: "default", Model: "haiku", ConfigDir: "/config", Generation: 1, Override: parsed}
}

// A run that starts both kinds of session names the guard flags of each: the probe's headless
// sessions take the headless ones, and a lane's pane takes the pane's, because a headless flag such
// as `--no-session-persistence` stops an interactive start.
func TestALanePaneTakesTheSuitesPaneFlags(t *testing.T) {
	spec := overrideSpec(t, `{"args":["--no-session-persistence","--max-turns","3"],"pane_args":["--tools","Bash"]}`)
	got := paneArgs(spec)
	if want := []string{"--tools", "Bash"}; len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		t.Errorf("the pane's flags are missing: %v", got)
	}
	for _, headless := range []string{"--no-session-persistence", "--max-turns"} {
		if slices.Contains(got, headless) {
			t.Errorf("a pane took the headless flag %s: %v", headless, got)
		}
	}
}

// A suite that names no pane flags gives a pane the one set it named, as every suite before
// `pane_args` existed did.
func TestALanePaneTakesTheOnlyFlagsASuiteNamed(t *testing.T) {
	spec := overrideSpec(t, `{"args":["--setting-sources","project","--tools","Bash"]}`)
	got := paneArgs(spec)
	if want := []string{"--setting-sources", "project", "--tools", "Bash"}; len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		t.Errorf("the suite's flags are missing: %v", got)
	}
}
