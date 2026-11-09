package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
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
	profile, err := Filesystem{User: user}.Load(t.Context(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Agent.PermissionMode != "auto" || len(profile.Agent.Gates) != 1 {
		t.Errorf("agent %+v", profile.Agent)
	}
}

// TestLayersApplyInOrder sets one key in every layer. Each layer wins over the one before
// it, and each changes the digest a lane records.
func TestLayersApplyInOrder(t *testing.T) {
	const (
		user       = "[agent]\nmodel = \"from-user\"\n[lifecycle]\nstall_after = \"1m\"\nmax_restarts = 1\nrestart_window = \"1m\"\n"
		repository = "[agent]\nmodel = \"from-repository\"\n[lifecycle]\nstall_after = \"2m\"\nmax_restarts = 2\n"
		lane       = "[agent]\nmodel = \"from-lane\"\n[lifecycle]\nstall_after = \"3m\"\n"
	)
	steps := []struct {
		name                   string
		user, repository, lane string
		model, stall           string
		restarts               int64
		window                 string
	}{
		{name: "built-in", model: "opus", stall: "20m", restarts: 5, window: "10m"},
		{name: "user", user: user, model: "from-user", stall: "1m", restarts: 1, window: "1m"},
		{name: "repository", user: user, repository: repository, model: "from-repository", stall: "2m", restarts: 2, window: "1m"},
		{name: "lane", user: user, repository: repository, lane: lane, model: "from-lane", stall: "3m", restarts: 2, window: "1m"},
	}
	seen := map[string]string{}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			profile, err := ResolveLayers(step.user, step.repository, step.lane)
			if err != nil {
				t.Fatal(err)
			}
			got := profile.Lifecycle
			if profile.Agent.Model != step.model || got.StallAfter != step.stall || got.MaxRestarts != step.restarts || got.RestartWindow != step.window {
				t.Errorf("profile %s %+v", profile.Agent.Model, got)
			}
			digest, err := profile.Digest()
			if err != nil {
				t.Fatal(err)
			}
			if earlier, repeated := seen[digest]; repeated {
				t.Errorf("the %s layer left the digest of the %s layer", step.name, earlier)
			}
			seen[digest] = step.name
		})
	}
}

func TestALaneLayerNarrowsAndCannotWiden(t *testing.T) {
	tests := []struct {
		name, user, repository, lane string
		refused                      bool
	}{
		{name: "a lane can narrow", user: "[agent]\npermission_mode = \"auto\"\n", lane: "[agent]\npermission_mode = \"default\"\n"},
		{name: "a lane cannot raise", lane: "[agent]\npermission_mode = \"auto\"\n", refused: true},
		{name: "a lane cannot reach bypass", lane: "[agent]\npermission_mode = \"bypassPermissions\"\n", refused: true},
		{name: "a repository cannot raise for a lane", repository: "[agent]\npermission_mode = \"auto\"\n", refused: true},
		{name: "a lane cannot admit more", lane: "[admission]\nmax_lanes = 99\n", refused: true},
		{name: "a repository cannot admit more", repository: "[admission]\nmax_lanes_per_sign_in = 99\n", refused: true},
		{name: "a repository cannot lower the floor", user: "[admission]\nmin_free_memory_mb = 512\n", repository: "[admission]\nmin_free_memory_mb = 0\n", refused: true},
		{name: "a user sets what a machine admits", user: "[admission]\nmax_lanes = 9\n"},
		{name: "a user turns content capture on", user: "[telemetry]\ncontent = true\n"},
		{name: "a lane cannot turn content capture on", lane: "[telemetry]\ncontent = true\n", refused: true},
		{name: "a repository cannot turn content capture on", repository: "[telemetry]\ncontent = true\n", refused: true},
		{name: "a repository can turn content capture off", user: "[telemetry]\ncontent = true\n", repository: "[telemetry]\ncontent = false\n"},
		{name: "a repository asks for traces", repository: "[telemetry]\ntraces = true\nomit_repository = true\n"},
		{name: "a repository cannot keep account identifiers on export", repository: "[telemetry]\nexport_account_attributes = true\n", refused: true},
		{name: "a lane cannot change retention", lane: "[telemetry]\nretention_age = \"1h\"\n", refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveLayers(tt.user, tt.repository, tt.lane)
			if refused := errors.Is(err, lanes.ErrBadProfile); refused != tt.refused {
				t.Errorf("error %v, want refused=%v", err, tt.refused)
			}
		})
	}
}
