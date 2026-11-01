package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

const bypassMode = "bypassPermissions"

// TestNoDefaultBypass walks every layer a lane can have and finds no bypass until
// someone with the right to choose it names it (NFR-SEC-3).
func TestNoDefaultBypass(t *testing.T) {
	if strings.Contains(builtin, bypassMode) {
		t.Error("the built-in layer names a bypass mode")
	}
	layers := []struct {
		name string
		load func() (lanes.Profile, error)
	}{
		{"the built-in layer", func() (lanes.Profile, error) { return Resolve() }},
		{"an empty user layer", func() (lanes.Profile, error) { return ResolveRepository("name = \"mine\"\n", "") }},
		{"an empty repository layer", func() (lanes.Profile, error) { return ResolveRepository("", "name = \"theirs\"\n") }},
		{"both empty", func() (lanes.Profile, error) { return ResolveRepository("", "") }},
	}
	for _, tt := range layers {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := tt.load()
			if err != nil {
				t.Fatal(err)
			}
			if profile.Agent.PermissionMode != "default" {
				t.Errorf("mode %q", profile.Agent.PermissionMode)
			}
		})
	}
}

func TestARepositoryCannotRaiseThePermissionMode(t *testing.T) {
	const layer = "[agent]\npermission_mode = \"%s\"\n"
	raise := func(mode string) string { return strings.Replace(layer, "%s", mode, 1) }
	tests := []struct {
		name       string
		user       string
		repository string
		refused    bool
	}{
		{name: "a repository says what is already so", repository: raise("default")},
		{name: "a repository narrows", user: raise("auto"), repository: raise("default")},
		{name: "a user chooses auto", user: raise("auto")},
		{name: "a repository raises to auto", repository: raise("auto"), refused: true},
		{name: "a repository raises to bypass", repository: raise(bypassMode), refused: true},
		{name: "a repository raises past a user's auto", user: raise("auto"), repository: raise(bypassMode), refused: true},
		{name: "a repository names a mode nobody knows", repository: raise("everything"), refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveRepository(tt.user, tt.repository)
			if refused := errors.Is(err, lanes.ErrBadProfile); refused != tt.refused {
				t.Errorf("err %v", err)
			}
		})
	}
}

func TestFilesystemReadsBothLayers(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, UserFile)
	if err := os.WriteFile(user, []byte("[agent]\npermission_mode = \"auto\"\ngates = [\"Bash(git push *)\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := Filesystem{User: user}.Load(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if profile.Agent.PermissionMode != "auto" || len(profile.Agent.Gates) != 1 {
		t.Errorf("agent %+v", profile.Agent)
	}
}
