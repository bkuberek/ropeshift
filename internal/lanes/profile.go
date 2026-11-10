package lanes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/messages"
)

// ErrBadProfile means a layer cannot become settings.
var ErrBadProfile = errors.New("bad profile")

// DigestPrefix names a profile digest's kind.
const DigestPrefix = "sha256:"

// Credentials names the sign-in a lane uses, by reference only.
type Credentials struct {
	Kind      string `toml:"kind" json:"kind"`
	SecretRef string `toml:"secret_ref" json:"secret_ref"`
}

// AgentSettings configures the agent CLI.
type AgentSettings struct {
	Model            string      `toml:"model" json:"model"`
	Effort           string      `toml:"effort" json:"effort"`
	PermissionMode   string      `toml:"permission_mode" json:"permission_mode"`
	InstructionsFile string      `toml:"instructions_file" json:"instructions_file"`
	Credentials      Credentials `toml:"credentials" json:"credentials"`
	// Gates are permission rules for actions a person must approve. On a lane that would
	// not prompt, the driver writes each as an `ask` rule, because `PermissionRequest`
	// fires only for a call that would.
	Gates []string `toml:"gates" json:"gates"`
}

// RoleThresholds is the context policy of one role.
type RoleThresholds struct {
	WarnTokens    int64 `toml:"warn_tokens" json:"warn_tokens"`
	CompactTokens int64 `toml:"compact_tokens" json:"compact_tokens"`
	HandoffTokens int64 `toml:"handoff_tokens" json:"handoff_tokens"`
}

// ContextSettings configures the context policy.
type ContextSettings struct {
	Roles             map[string]RoleThresholds `toml:"roles" json:"roles"`
	MinHeadroomTokens int64                     `toml:"min_headroom_tokens" json:"min_headroom_tokens"`
	// PackBudgetTokens is how much of a context pack carries its text, as the control plane
	// estimates it. Only the user layer reaches a claim.
	PackBudgetTokens int64 `toml:"pack_budget_tokens" json:"pack_budget_tokens"`
}

// MessageWindow is one kind's window, as a profile says it.
type MessageWindow struct {
	StaleAfter  string `toml:"stale_after" json:"stale_after"`
	ExpireAfter string `toml:"expire_after" json:"expire_after"`
}

// MessageWindows reads what a profile sets. A window that does not parse, or that ends before it
// goes stale, leaves its kind on the default, because a bad value must not lose a message.
func (p Profile) MessageWindows() messages.Windows {
	out := messages.Windows{}
	for kind, window := range p.Messages {
		stale, staleErr := time.ParseDuration(window.StaleAfter)
		expire, expireErr := time.ParseDuration(window.ExpireAfter)
		if staleErr == nil && expireErr == nil && stale >= 0 && expire > stale {
			out[messages.Kind(kind)] = messages.Window{StaleAfter: stale, ExpireAfter: expire}
		}
	}
	return out
}

// LifecycleSettings holds the lane's timers and limits. Durations stay text, so a
// domain value carries no parsing rule.
type LifecycleSettings struct {
	StallAfter           string `toml:"stall_after" json:"stall_after"`
	MaxRestarts          int64  `toml:"max_restarts" json:"max_restarts"`
	RestartWindow        string `toml:"restart_window" json:"restart_window"`
	MaxGenerationsPerDay int64  `toml:"max_generations_per_day" json:"max_generations_per_day"`
	AskWait              string `toml:"ask_wait" json:"ask_wait"`
	DrainDeadline        string `toml:"drain_deadline" json:"drain_deadline"`
	WakeDebounce         string `toml:"wake_debounce" json:"wake_debounce"`
	WakeMinInterval      string `toml:"wake_min_interval" json:"wake_min_interval"`
	HandoffTimeout       string `toml:"handoff_timeout" json:"handoff_timeout"`
	UsageBackoff         string `toml:"usage_backoff" json:"usage_backoff"`
	ReadyTimeout         string `toml:"ready_timeout" json:"ready_timeout"`
	BriefGrace           string `toml:"brief_grace" json:"brief_grace"`
	ApprovalWait         string `toml:"approval_wait" json:"approval_wait"`
	RestartBackoff       string `toml:"restart_backoff" json:"restart_backoff"`
	HumanBatchWindow     string `toml:"human_batch_window" json:"human_batch_window"`
	HumanBatchMaxWait    string `toml:"human_batch_max_wait" json:"human_batch_max_wait"`
}

// WorktreeSettings says where a lane's worktree goes, and whether its work may leave the machine.
type WorktreeSettings struct {
	Root         string `toml:"root" json:"root"`
	BranchPrefix string `toml:"branch_prefix" json:"branch_prefix"`
	// PushOnStop sends a lane's branch and its checkpoint refs to the repository's remote when the
	// lane stops or its worktree is removed. A push sends work off the machine, so it is off until
	// the user's own layer turns it on, and no other layer can change it.
	PushOnStop bool `toml:"push_on_stop" json:"push_on_stop"`
}

// ResourceSettings sizes what each lane gets.
type ResourceSettings struct {
	PortsPerLane         int64  `toml:"ports_per_lane" json:"ports_per_lane"`
	ComposeProjectPrefix string `toml:"compose_project_prefix" json:"compose_project_prefix"`
}

// Profile is the resolved settings of a lane. It configures a lane and holds no
// records. RunMode stays empty until a layer sets it, because the default follows
// the sign-in kind. A lane on a `plan` work order ignores it and runs in a pane.
type Profile struct {
	Name      string              `toml:"name" json:"name"`
	Driver    string              `toml:"driver" json:"driver"`
	RunMode   string              `toml:"run_mode" json:"run_mode"`
	Agent     AgentSettings       `toml:"agent" json:"agent"`
	Context   ContextSettings     `toml:"context" json:"context"`
	Lifecycle LifecycleSettings   `toml:"lifecycle" json:"lifecycle"`
	Worktree  WorktreeSettings    `toml:"worktree" json:"worktree"`
	Resources ResourceSettings    `toml:"resources" json:"resources"`
	Admission Admission           `toml:"admission" json:"admission"`
	Hooks     map[string][]string `toml:"hooks" json:"hooks"`
	// Messages says how long a message stays useful, per kind.
	Messages map[string]MessageWindow `toml:"messages" json:"messages"`
	// Telemetry says what a lane's agent sends to the worker's receiver, and how long the worker keeps it.
	Telemetry TelemetrySettings `toml:"telemetry" json:"telemetry"`
}

// The values of `run_mode`. The default waits for the doctor, and until then an empty
// value runs headless.
const (
	ProfileRunTmux     = "tmux"
	ProfileRunHeadless = "headless"
)

// Digest identifies the settings. It is recorded on the lane, so the bytes must not
// depend on the order of a file. encoding/json sorts map keys, and a struct has one
// order.
func (p Profile) Digest() (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return DigestPrefix + hex.EncodeToString(sum[:]), nil
}

// MergeLayers folds layers in order, later over earlier. A table merges key by key,
// so a layer can change one threshold and keep its neighbors, and any other value
// replaces what was there.
func MergeLayers(layers ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, layer := range layers {
		mergeInto(merged, layer)
	}
	return merged
}

func mergeInto(into, from map[string]any) {
	for key, value := range from {
		table, isTable := value.(map[string]any)
		existing, hasTable := into[key].(map[string]any)
		if isTable && hasTable {
			mergeInto(existing, table)
			continue
		}
		if isTable {
			fresh := map[string]any{}
			mergeInto(fresh, table)
			into[key] = fresh
			continue
		}
		into[key] = value
	}
}

// RepositoryToken is what a worktree root's template replaces with the repository's
// name.
const RepositoryToken = "{repo}"

// Placement says where one lane works.
type Placement struct {
	Path   string
	Branch string
}

// Place computes a lane's worktree and branch. A root is relative to the repository,
// so `../{repo}-lanes` keeps lanes beside the repository and out of it.
func Place(settings WorktreeSettings, repository, lane, branch string) (Placement, error) {
	if !path.IsAbs(repository) {
		return Placement{}, fmt.Errorf("%w: repository %q is not absolute", ErrBadProfile, repository)
	}
	if settings.Root == "" || lane == "" || strings.ContainsAny(lane, "/\\") || lane == ".." {
		return Placement{}, fmt.Errorf("%w: no place for lane %q under %q", ErrBadProfile, lane, settings.Root)
	}
	root := strings.ReplaceAll(settings.Root, RepositoryToken, path.Base(repository))
	if !path.IsAbs(root) {
		root = path.Join(repository, root)
	}
	if branch == "" {
		branch = settings.BranchPrefix + lane
	}
	return Placement{Path: path.Join(root, lane), Branch: branch}, nil
}
