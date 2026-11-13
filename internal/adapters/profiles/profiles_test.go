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

// TestPushOnStopIsTheUsersAlone holds that work leaves the machine only when the person chose it:
// pushing is off unless the user's layer turns it on, and no later layer changes that choice.
func TestPushOnStopIsTheUsersAlone(t *testing.T) {
	const (
		on  = "[worktree]\npush_on_stop = true\n"
		off = "[worktree]\npush_on_stop = false\n"
	)
	tests := []struct {
		name, user, repository, lane string
		refused, pushes              bool
	}{
		{name: "nothing is pushed by default"},
		{name: "a user turns it on", user: on, pushes: true},
		{name: "a repository cannot turn it on", repository: on, refused: true},
		{name: "a lane cannot turn it on", lane: on, refused: true},
		{name: "a repository cannot turn a user's choice off", user: on, repository: off, refused: true},
		{name: "a lane cannot turn a user's choice off", user: on, lane: off, refused: true},
		{name: "a repository may say what is already so", user: on, repository: on, pushes: true},
		{name: "a repository may restate the default", repository: off},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := ResolveLayers(tt.user, tt.repository, tt.lane)
			if refused := errors.Is(err, lanes.ErrBadProfile); refused != tt.refused {
				t.Fatalf("error %v, want refused=%v", err, tt.refused)
			}
			if err == nil && profile.Worktree.PushOnStop != tt.pushes {
				t.Errorf("push_on_stop %v, want %v", profile.Worktree.PushOnStop, tt.pushes)
			}
		})
	}
}

// TestAnEmptyRepositoryReadsTheUsersLayerAlone is how a worker reads a choice only the user's
// layer makes. A layer where the process happens to run is no repository's.
func TestAnEmptyRepositoryReadsTheUsersLayerAlone(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, UserFile)
	if err := os.WriteFile(user, []byte("[worktree]\npush_on_stop = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	here := t.TempDir()
	if err := os.MkdirAll(filepath.Join(here, filepath.Dir(RepositoryFile)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(here, RepositoryFile), []byte("not a setting = \"at all\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	profile, err := Filesystem{User: user}.Load(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Worktree.PushOnStop {
		t.Errorf("worktree %+v", profile.Worktree)
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
		{name: "a lane cannot admit more", user: "[admission]\nmax_lanes = 4\n", lane: "[admission]\nmax_lanes = 99\n", refused: true},
		{name: "a repository cannot admit more", user: "[admission]\nmax_lanes_per_sign_in = 2\n", repository: "[admission]\nmax_lanes_per_sign_in = 99\n", refused: true},
		{name: "a repository cannot lift a cap", user: "[admission]\nmax_lanes_per_sign_in = 2\n", repository: "[admission]\nmax_lanes_per_sign_in = 0\n", refused: true},
		{name: "a repository can lower a cap", user: "[admission]\nmax_lanes_per_sign_in = 3\nmax_lanes = 8\n", repository: "[admission]\nmax_lanes_per_sign_in = 1\nmax_lanes = 2\n"},
		{name: "a repository can cap what the user left open", repository: "[admission]\nmax_lanes_per_sign_in = 2\n"},
		{name: "a lane can lower a cap", user: "[admission]\nmax_lanes = 4\n", lane: "[admission]\nmax_lanes = 1\n"},
		{name: "a repository cannot lower the floor", user: "[admission]\nmin_free_memory_mb = 512\n", repository: "[admission]\nmin_free_memory_mb = 0\n", refused: true},
		{name: "a user sets what a machine admits", user: "[admission]\nmax_lanes = 9\n"},
		{name: "a repository cannot lift the recommendation", repository: "[admission]\nmax_lanes = 0\n", refused: true},
		{name: "a repository can lower the recommendation", repository: "[admission]\nmax_lanes = 2\n"},
		{name: "a repository cannot change the estimates", repository: "[admission]\nlane_cpu_estimate = 0.5\n", refused: true},
		{name: "a repository cannot change the reserve", repository: "[admission]\nreserve_memory = \"1GiB\"\n", refused: true},
		{name: "a lane cannot change the report interval", lane: "[resources]\nreport_interval = \"1s\"\n", refused: true},
		{name: "a repository cannot quiet a warning", repository: "[resources]\ndisk_free_warn = \"0\"\n", refused: true},
		{name: "a repository still sizes its lanes' ports", repository: "[resources]\nports_per_lane = 20\n"},
		{name: "a user sets the machine's settings", user: "[admission]\nlane_cpu_estimate = 2\nlane_memory_estimate = \"3GiB\"\nreserve_cpu = 0\nreserve_memory = \"8GiB\"\nobserved_window = 20\nobserved_min = 5\n[resources]\nreport_interval = \"30s\"\ndisk_free_warn = \"20GiB\"\nmemory_available_warn = 0.2\nmemory_pressure_warn = 5\ncpu_load_warn = 2\n"},
		{name: "a user's interval that is no time is refused", user: "[resources]\nreport_interval = \"often\"\n", refused: true},
		{name: "a user's negative estimate is refused", user: "[admission]\nlane_cpu_estimate = -1\n", refused: true},
		{name: "a negative cap is refused", user: "[admission]\nmax_lanes = -1\n", refused: true},
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

// TestALayerLowersTheLaneCapsTheUserSet holds ADR-0021's rule for a repository's layer: it can lower
// either lane cap for its own lanes, and what it lowers is what the lane is admitted by.
func TestALayerLowersTheLaneCapsTheUserSet(t *testing.T) {
	profile, err := ResolveLayers("[admission]\nmax_lanes_per_sign_in = 3\n", "[admission]\nmax_lanes_per_sign_in = 1\nmax_lanes = 2\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Admission.MaxLanesPerSignIn != 1 || profile.Admission.MaxLanes == nil || *profile.Admission.MaxLanes != 2 {
		t.Errorf("admission %+v", profile.Admission)
	}
	_, err = ResolveLayers("[admission]\nmax_lanes_per_sign_in = 1\n", "[admission]\nmax_lanes_per_sign_in = 2\n", "")
	if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), "max_lanes_per_sign_in") {
		t.Errorf("a repository that raises the sign-in cap: %v", err)
	}
}

// TestTheBuiltInCapsFollowTheRecommendation holds FR-PROJ-7's defaults: no per-worker cap, so the worker's
// recommendation stands, and no per-sign-in cap. Zero is a choice a person makes, and reads back as set.
func TestTheBuiltInCapsFollowTheRecommendation(t *testing.T) {
	profile, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if profile.Admission.MaxLanes != nil || profile.Admission.MaxLanesPerSignIn != 0 {
		t.Errorf("admission %+v", profile.Admission)
	}
	profile, err = Resolve("[admission]\nmax_lanes = 0\n")
	if err != nil || profile.Admission.MaxLanes == nil || *profile.Admission.MaxLanes != 0 {
		t.Errorf("no cap reads as %+v: %v", profile.Admission, err)
	}
}

// NFR-SEC-3 and FR-LANE-19: no layer names a mode that bypasses every check, not even the user's, because
// a lane in it would push and run every gated action with nobody asked. A mode this does not know is
// refused too, because it could be one. Every other mode is the user's to choose.
func TestNoLayerChoosesABypassMode(t *testing.T) {
	const layer = "[agent]\npermission_mode = \"%s\"\n"
	mode := func(name string) string { return strings.Replace(layer, "%s", name, 1) }
	tests := []struct {
		name, user, repository, lane string
		refused                      bool
	}{
		{name: "a user chooses bypass", user: mode(bypassMode), refused: true},
		{name: "a repository chooses bypass over a user's bypass", user: mode(bypassMode), repository: mode(bypassMode), refused: true},
		{name: "a lane chooses bypass", lane: mode(bypassMode), refused: true},
		{name: "a user names a mode nobody knows", user: mode("everything"), refused: true},
		{name: "a user names no mode", user: mode(""), refused: true},
		{name: "a user chooses plan", user: mode("plan")},
		{name: "a user chooses default", user: mode("default")},
		{name: "a user chooses acceptEdits", user: mode("acceptEdits")},
		{name: "a user chooses dontAsk", user: mode("dontAsk")},
		{name: "a user chooses auto", user: mode("auto")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := ResolveLayers(tt.user, tt.repository, tt.lane)
			if refused := errors.Is(err, lanes.ErrBadProfile); refused != tt.refused {
				t.Errorf("error %v, want refused=%v", err, tt.refused)
			}
			if err == nil && profile.Agent.PermissionMode == bypassMode {
				t.Error("a lane would bypass every check")
			}
		})
	}
	if _, err := Resolve(mode(bypassMode)); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("a layer resolved on its own chose bypass: %v", err)
	}
}

// TestTheClassMappingIsTheUsersAlone is FR-AGENT-2: the built-in layer maps every capability class to a
// model, the user's layer may change that, and no repository or lane layer may, because a file a plan or
// an agent can edit must not make a cheap agent expensive.
func TestTheClassMappingIsTheUsersAlone(t *testing.T) {
	const cheaper = "[agent.classes]\nfrontier = \"sonnet\"\n"
	tests := []struct {
		name, user, repository, lane string
		refused                      bool
		frontier                     string
	}{
		{name: "the built-in mapping", frontier: "opus"},
		{name: "a user changes a class", user: cheaper, frontier: "sonnet"},
		{name: "a repository cannot", repository: cheaper, refused: true},
		{name: "a lane cannot", lane: cheaper, refused: true},
		{name: "a repository may say what is already so", user: cheaper, repository: cheaper, frontier: "sonnet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := ResolveLayers(tt.user, tt.repository, tt.lane)
			if (err != nil) != tt.refused {
				t.Fatalf("error %v, want refused=%v", err, tt.refused)
			}
			if err != nil {
				return
			}
			if profile.Agent.Classes["frontier"] != tt.frontier || profile.Agent.Classes["small"] != "haiku" ||
				profile.Agent.Classes["standard"] != "sonnet" || profile.Agent.Classes["large"] != "opus" {
				t.Errorf("classes %v", profile.Agent.Classes)
			}
		})
	}
}
