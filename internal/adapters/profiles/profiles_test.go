package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// TestALanesOwnLayerRunsNothingAndRaisesNoCap holds a lane's own layer, which any caller of the API can
// send, to what the user's and the repository's layers give: it changes no hook, no credential, and no
// place of the worktree or its branch, and it lowers a breaker's cap and never raises it. The same keys
// stay the user's and the repository's to set.
func TestALanesOwnLayerRunsNothingAndRaisesNoCap(t *testing.T) {
	const (
		hook       = "[hooks]\non_lane_created = [\"./scripts/setup.sh\"]\n"
		shell      = "[hooks]\non_lane_created = [\"/bin/sh\", \"-c\", \"id\"]\n"
		credential = "[agent.credentials]\nkind = \"api_key\"\nsecret_ref = \"env:ANTHROPIC_API_KEY\"\n"
	)
	tests := []struct {
		name, user, repository, lane string
		// refused names the key the refusal states, and empty passes.
		refused string
	}{
		{name: "a lane names a hook", lane: shell, refused: "[hooks]"},
		{name: "a lane replaces the repository's hook", repository: hook, lane: shell, refused: "[hooks]"},
		{name: "a lane replaces the user's hook", user: hook, lane: shell, refused: "[hooks]"},
		{name: "a lane clears the repository's hook", repository: hook, lane: "[hooks]\non_lane_created = []\n", refused: "[hooks]"},
		{name: "a lane names a hook nothing runs", lane: "hooks.on_anything = [\"id\"]\n", refused: "[hooks]"},
		{name: "a lane restates the repository's hook", repository: hook, lane: hook},
		{name: "a repository names a hook", repository: hook},
		{name: "a lane names a variable of the worker's", lane: "agent.credentials.secret_ref = \"env:HOME\"\n", refused: "agent.credentials"},
		{name: "a lane changes the sign-in kind", lane: "[agent.credentials]\nkind = \"api_key\"\n", refused: "agent.credentials"},
		{name: "a lane replaces the repository's credential", repository: credential, lane: "agent = { credentials = { secret_ref = \"env:OTHER\" } }\n", refused: "agent.credentials"},
		{name: "a repository names a credential", repository: credential},
		{name: "a lane puts its worktree anywhere", lane: "[worktree]\nroot = \"/\"\n", refused: "worktree.root"},
		{name: "a lane moves its worktree beside the repository", lane: "worktree.root = \"../elsewhere\"\n", refused: "worktree.root"},
		{name: "a lane changes its branch's prefix", lane: "[worktree]\nbranch_prefix = \"\"\n", refused: "worktree.branch_prefix"},
		// The driver picks the model a roster agent's class maps to, so a lane that moves to another driver
		// changes its model without setting agent.model (FR-AUTH-16).
		{name: "a lane changes its driver", lane: "driver = \"elsewhere\"\n", refused: "driver"},
		{name: "a lane changes the repository's driver", repository: "driver = \"elsewhere\"\n", lane: "driver = \"claude-code\"\n", refused: "driver"},
		{name: "a lane restates its driver", lane: "driver = \"claude-code\"\n"},
		{name: "a repository places its lanes", repository: "[worktree]\nroot = \"../lanes\"\nbranch_prefix = \"work/\"\n"},
		{name: "a lane raises the day's cap", lane: "[lifecycle]\nmax_generations_per_day = 9\n", refused: "lifecycle.max_generations_per_day"},
		{name: "a lane lifts the day's cap", lane: "[lifecycle]\nmax_generations_per_day = 0\n", refused: "lifecycle.max_generations_per_day"},
		{name: "a lane lowers the day's cap", lane: "[lifecycle]\nmax_generations_per_day = 2\n"},
		{name: "a lane caps a day the user left open", user: "[lifecycle]\nmax_generations_per_day = 0\n", lane: "[lifecycle]\nmax_generations_per_day = 3\n"},
		{name: "a lane raises the restarts", lane: "[lifecycle]\nmax_restarts = 6\n", refused: "lifecycle.max_restarts"},
		{name: "a lane raises past what the repository lowered", repository: "[lifecycle]\nmax_restarts = 2\n", lane: "[lifecycle]\nmax_restarts = 4\n", refused: "lifecycle.max_restarts"},
		{name: "a lane turns restarts off", lane: "[lifecycle]\nmax_restarts = 0\n"},
		{name: "a lane shortens the restart window", lane: "[lifecycle]\nrestart_window = \"1m\"\n", refused: "lifecycle.restart_window"},
		{name: "a lane names a restart window that is no time", lane: "[lifecycle]\nrestart_window = \"never\"\n", refused: "lifecycle.restart_window"},
		{name: "a lane lengthens the restart window", lane: "[lifecycle]\nrestart_window = \"1h\"\n"},
		{name: "a repository raises a cap for its lanes", repository: "[lifecycle]\nmax_generations_per_day = 20\nmax_restarts = 9\nrestart_window = \"1m\"\n"},
		{name: "a lane raises the review rounds past the default", lane: "[review]\nmax_review_rounds = 9\n", refused: "review.max_review_rounds"},
		{name: "a lane raises the review rounds past the repository's", repository: "[review]\nmax_review_rounds = 1\n", lane: "[review]\nmax_review_rounds = 2\n", refused: "review.max_review_rounds"},
		{name: "a lane unsets the review rounds the repository lowered", repository: "[review]\nmax_review_rounds = 1\n", lane: "[review]\nmax_review_rounds = 0\n", refused: "review.max_review_rounds"},
		{name: "a lane lowers the review rounds", lane: "[review]\nmax_review_rounds = 1\n"},
		{name: "a lane lowers the review rounds the user raised", user: "[review]\nmax_review_rounds = 6\n", lane: "[review]\nmax_review_rounds = 4\n"},
		{name: "a repository raises the review rounds", repository: "[review]\nmax_review_rounds = 6\n"},
		{name: "a lane takes the whole port pool", lane: "[resources]\nports_per_lane = 10000\n", refused: "resources.ports_per_lane"},
		{name: "a lane takes more ports than the repository gives", repository: "[resources]\nports_per_lane = 4\n", lane: "[resources]\nports_per_lane = 5\n", refused: "resources.ports_per_lane"},
		{name: "a lane takes ports the repository gave none", repository: "[resources]\nports_per_lane = 0\n", lane: "[resources]\nports_per_lane = 1\n", refused: "resources.ports_per_lane"},
		{name: "a lane takes fewer ports", lane: "[resources]\nports_per_lane = 2\n"},
		{name: "a lane takes no ports", lane: "[resources]\nports_per_lane = 0\n"},
		{name: "a lane takes the ports the repository raised to", repository: "[resources]\nports_per_lane = 20\n", lane: "[resources]\nports_per_lane = 20\n"},
		{name: "a lane names a file to read", lane: "[agent]\ninstructions_file = \"/etc/passwd\"\n", refused: "agent.instructions_file"},
		{name: "a lane clears the instructions file", lane: "agent.instructions_file = \"\"\n", refused: "agent.instructions_file"},
		{name: "a lane replaces the repository's instructions file", repository: "[agent]\ninstructions_file = \"CLAUDE.md\"\n", lane: "[agent]\ninstructions_file = \"AGENTS.md\"\n", refused: "agent.instructions_file"},
		{name: "a lane restates the instructions file", lane: "[agent]\ninstructions_file = \"AGENTS.md\"\n"},
		{name: "a repository names its instructions file", repository: "[agent]\ninstructions_file = \"CLAUDE.md\"\n"},
		{name: "a lane chooses its model and effort", lane: "[agent]\nmodel = \"haiku\"\neffort = \"low\"\n[lifecycle]\nstall_after = \"3m\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveLayers(tt.user, tt.repository, tt.lane)
			if tt.refused == "" {
				if err != nil {
					t.Errorf("refused: %v", err)
				}
				return
			}
			if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), tt.refused) {
				t.Errorf("error %v, want a refusal that names %s", err, tt.refused)
			}
		})
	}
}

// TestEveryLayersGatesJoinTheLayersBelow holds agent.gates to a union. A layer that sets the list adds to
// the gates of the layers below it and never replaces them, so a repository's layer that gates one more
// action need not restate the user's gates, and no layer can leave a gate out. The list keeps the order of
// the layers and names each gate once.
func TestEveryLayersGatesJoinTheLayersBelow(t *testing.T) {
	const (
		push = "Bash(git push *)"
		edit = "Edit"
		read = "Read"
	)
	gates := func(rules ...string) string {
		quoted := make([]string, len(rules))
		for i, rule := range rules {
			quoted[i] = strconv.Quote(rule)
		}
		return "[agent]\ngates = [" + strings.Join(quoted, ", ") + "]\n"
	}
	tests := []struct {
		name, user, repository, lane string
		want                         []string
	}{
		{name: "no layer sets a gate"},
		{name: "a user sets the gates", user: gates(push), want: []string{push}},
		{name: "a repository gates what the user left open", repository: gates(edit), want: []string{edit}},
		{name: "a repository adds a gate without restating the user's", user: gates(push), repository: gates(edit), want: []string{push, edit}},
		{name: "a repository restates the user's gate and adds one", user: gates(push), repository: gates(push, edit), want: []string{push, edit}},
		{name: "a repository clears the user's gates", user: gates(push), repository: gates(), want: []string{push}},
		{name: "a repository replaces the user's gates", user: gates(push, edit), repository: gates(edit), want: []string{push, edit}},
		{name: "a lane clears the user's gates", user: gates(push), lane: gates(), want: []string{push}},
		{name: "a lane clears them with a dotted key", user: gates(push), lane: "agent.gates = []\n", want: []string{push}},
		{name: "a lane replaces the user's gates", user: gates(push, edit), lane: gates(edit), want: []string{push, edit}},
		{name: "a lane clears the repository's gates", repository: gates(edit), lane: gates(), want: []string{edit}},
		{name: "a lane keeps the repository's gate and leaves the user's out", user: gates(push), repository: gates(push, edit), lane: gates(edit), want: []string{push, edit}},
		{name: "a lane adds a gate to the user's", user: gates(push), lane: gates(push, edit), want: []string{push, edit}},
		{name: "a lane adds a gate where none was", lane: gates(push), want: []string{push}},
		{name: "a lane restates the gates below in another order", user: gates(push), repository: gates(push, edit), lane: gates(edit, push), want: []string{push, edit}},
		{name: "every layer adds one", user: gates(push), repository: gates(edit), lane: gates(read), want: []string{push, edit, read}},
		{name: "a gate said twice is kept once", user: gates(push, push), repository: gates(edit, push, edit), want: []string{push, edit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := ResolveLayers(tt.user, tt.repository, tt.lane)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !slices.Equal(profile.Agent.Gates, tt.want) {
				t.Errorf("gates %q, want %q", profile.Agent.Gates, tt.want)
			}
		})
	}
}

// TestAGateSetToSomethingElseIsRefused keeps the union from hiding a layer whose gates are no list: it
// leaves the user's gates in place and fails the profile, whatever the layer after it says.
func TestAGateSetToSomethingElseIsRefused(t *testing.T) {
	const (
		user  = "[agent]\ngates = [\"Bash(git push *)\"]\n"
		fixed = "[agent]\ngates = [\"Edit\"]\n"
	)
	for name, layers := range map[string][3]string{
		"a repository's string":              {user, "[agent]\ngates = \"Edit\"\n", ""},
		"a lane's string":                    {user, "", "[agent]\ngates = \"Edit\"\n"},
		"a repository's string then a list":  {user, "[agent]\ngates = \"Edit\"\n", fixed},
		"a repository's number in the list":  {user, "[agent]\ngates = [1]\n", ""},
		"a repository's table":               {user, "[agent.gates]\nEdit = true\n", ""},
		"a repository's list then a string":  {user, fixed, "[agent]\ngates = \"Edit\"\n"},
		"a user's string then a lane's list": {"[agent]\ngates = \"x\"\n", "", fixed},
	} {
		t.Run(name, func(t *testing.T) {
			if profile, err := ResolveLayers(layers[0], layers[1], layers[2]); !errors.Is(err, lanes.ErrBadProfile) {
				t.Errorf("got gates %q and error %v", profile.Agent.Gates, err)
			}
		})
	}
}

// TestTwoSpellingsOfOneSettingAreRefusedEveryTime holds the profile to one spelling of each key. The
// decoder reads a key under Unicode case folding, so a layer that holds `[hooks]` and `[Hooks]`, or two
// layers that spell one key two ways, would resolve to whichever the decoder met first, which changes from
// one run to the next. Each is refused, with both spellings named, on every run.
func TestTwoSpellingsOfOneSettingAreRefusedEveryTime(t *testing.T) {
	const runs = 100
	tests := []struct {
		name, user, repository, lane string
		// spellings are the two the refusal names.
		spellings [2]string
	}{
		{name: "a table spelled two ways in one layer", user: "[hooks]\non_lane_created = [\"a\"]\n[Hooks]\non_lane_created = [\"b\"]\n", spellings: [2]string{"hooks", "Hooks"}},
		{name: "a key spelled two ways in one table", user: "[worktree]\nroot = \"../a\"\nROOT = \"../b\"\n", spellings: [2]string{"ROOT", "root"}},
		{name: "a dotted key spelled two ways", user: "agent.model = \"a\"\nAgent.model = \"b\"\n", spellings: [2]string{"Agent", "agent"}},
		{name: "a repository's layer", repository: "[hooks]\non_lane_created = [\"a\"]\n[Hooks]\non_lane_created = [\"b\"]\n", spellings: [2]string{"hooks", "Hooks"}},
		{name: "a lane's layer", lane: "[worktree]\nroot = \"../a\"\nROOT = \"../b\"\n", spellings: [2]string{"ROOT", "root"}},
		{name: "a table in two layers", user: "[hooks]\non_lane_created = [\"a\"]\n", repository: "[Hooks]\non_lane_created = [\"b\"]\n", spellings: [2]string{"Hooks", "hooks"}},
		{name: "a key in the user's layer and the repository's", user: "[worktree]\nroot = \"../a\"\n", repository: "[worktree]\nROOT = \"../b\"\n", spellings: [2]string{"ROOT", "root"}},
		{name: "a key in the repository's layer and a lane's", repository: "[review]\nmax_review_rounds = 2\n", lane: "[review]\nMAX_REVIEW_ROUNDS = 1\n", spellings: [2]string{"MAX_REVIEW_ROUNDS", "max_review_rounds"}},
		{name: "a table the built-in layer spells another way", user: "[Agent]\nmodel = \"haiku\"\n", spellings: [2]string{"Agent", "agent"}},
		{name: "a nested table", user: "[agent.credentials]\nkind = \"api_key\"\n[agent.Credentials]\nkind = \"api_key\"\n", spellings: [2]string{"Credentials", "credentials"}},
		{name: "a gate list in two layers", user: "[agent]\ngates = [\"Edit\"]\n", repository: "[agent]\nGATES = [\"Read\"]\n", spellings: [2]string{"GATES", "gates"}},
		{name: "a key spelled with the Kelvin sign", user: "[hooks]\non_lane_created = [\"a\"]\n", repository: "[\"hoo\u212as\"]\non_lane_created = [\"b\"]\n", spellings: [2]string{"hoo\u212as", "hooks"}},
		{name: "a key spelled with the long s", user: "[hooks]\non_lane_created = [\"a\"]\n", repository: "[\"hook\u017f\"]\non_lane_created = [\"b\"]\n", spellings: [2]string{"hook\u017f", "hooks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for run := range runs {
				_, err := ResolveLayers(tt.user, tt.repository, tt.lane)
				if !errors.Is(err, lanes.ErrBadProfile) {
					t.Fatalf("run %d accepted the layers: %v", run, err)
				}
				for _, spelling := range tt.spellings {
					if !strings.Contains(err.Error(), strconv.Quote(spelling)) {
						t.Fatalf("run %d: error %q does not name %s", run, err, strconv.Quote(spelling))
					}
				}
			}
		})
	}
}

// TestOneSpellingPerKeyStillResolves keeps the refusal to keys that differ only in case: two settings that
// merely begin alike, a key repeated in one spelling across layers, and each layer's own tables resolve.
func TestOneSpellingPerKeyStillResolves(t *testing.T) {
	const (
		user       = "[agent]\nmodel = \"haiku\"\n[worktree]\nroot = \"../a\"\nbranch_prefix = \"x/\"\n"
		repository = "[agent]\nmodel = \"sonnet\"\n[worktree]\nroot = \"../b\"\n"
	)
	profile, err := ResolveLayers(user, repository, "[agent]\neffort = \"low\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Agent.Model != "sonnet" || profile.Agent.Effort != "low" || profile.Worktree.Root != "../b" || profile.Worktree.BranchPrefix != "x/" {
		t.Errorf("resolved %+v", profile)
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
