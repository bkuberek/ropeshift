package lanes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/workorders"
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
	// Gates are permission rules for actions a person must approve. The driver writes each as an
	// `ask` rule in every mode, because `PermissionRequest` fires only for a call that would prompt,
	// and an ask rule outranks the allow rules a lane that writes holds (FR-LANE-19). A gate only
	// adds requests. The layers' lists join as a union, so a layer adds gates and no layer can take
	// one away (MergeLayers).
	Gates []string `toml:"gates" json:"gates"`
	// Classes maps each capability class of the agent roster to a model of this profile's driver
	// (ADR-0025). Only the built-in and the user's layers set it, because a repository's or a lane's
	// layer, which a plan or an agent can edit, must not make a cheap agent expensive.
	Classes map[string]string `toml:"classes" json:"classes,omitempty"`
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
	// Root is where a lane's worktrees go. The built-in layer sets none, and the profile source gives a
	// profile with none the installation's own root, which lies outside every clone (FR-PROF-3).
	Root string `toml:"root" json:"root"`
	// RootSource names the layer that set Root, for a refusal that has to say where to change it. No
	// layer sets it, and it is no part of a lane's digest.
	RootSource   string `toml:"-" json:"-"`
	BranchPrefix string `toml:"branch_prefix" json:"branch_prefix"`
	// PushOnStop sends a lane's branch and its checkpoint refs to the repository's remote when the
	// lane stops or its worktree is removed. A push sends work off the machine, so it is off until
	// the user's own layer turns it on, and no other layer can change it.
	PushOnStop bool `toml:"push_on_stop" json:"push_on_stop"`
}

// ResourceSettings sizes what each lane gets, and says how often the worker measures its machine and where
// a measure warns (FR-OBS-8, FR-UI-10). A measuring setting left unset takes its documented default and
// stays out of a lane's digest.
type ResourceSettings struct {
	PortsPerLane         int64    `toml:"ports_per_lane" json:"ports_per_lane"`
	ComposeProjectPrefix string   `toml:"compose_project_prefix" json:"compose_project_prefix"`
	ReportInterval       string   `toml:"report_interval" json:"report_interval,omitempty"`
	DiskFreeWarn         string   `toml:"disk_free_warn" json:"disk_free_warn,omitempty"`
	MemoryAvailableWarn  *float64 `toml:"memory_available_warn" json:"memory_available_warn,omitempty"`
	MemoryPressureWarn   *float64 `toml:"memory_pressure_warn" json:"memory_pressure_warn,omitempty"`
	CPULoadWarn          *float64 `toml:"cpu_load_warn" json:"cpu_load_warn,omitempty"`
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
	// Review bounds the reviews of one work order. It stays out of the digest while a profile leaves it
	// unset, so no lane's recorded profile moves.
	Review ReviewSettings `toml:"review" json:"review,omitzero"`
}

// ReviewSettings is the `[review]` table of a profile.
type ReviewSettings struct {
	// MaxReviewRounds bounds how many times the review agent judges one work order (FR-REVIEW-8). At the
	// bound, work its review still fails waits for a person. Unset reads as the default of three.
	MaxReviewRounds int64 `toml:"max_review_rounds" json:"max_review_rounds,omitempty"`
}

// Rounds is the bound in force: the one a profile set, or else the default.
func (s ReviewSettings) Rounds() int64 {
	if s.MaxReviewRounds < 1 {
		return workorders.DefaultMaxReviewRounds
	}
	return s.MaxReviewRounds
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

// unionSettings are the settings, each as its path of keys, whose lists merge as the union of every
// layer's list instead of the last layer's. A gate asks a person before an action runs, so a layer that
// sets agent.gates adds to the gates of the layers below it and cannot drop one.
var unionSettings = [][]string{{"agent", "gates"}}

// MergeLayers folds layers in order, later over earlier. A table merges key by key,
// so a layer can change one threshold and keep its neighbors, and any other value
// replaces what was there, except a list of unionSettings, which keeps every layer's
// entries in the order the layers name them, each once.
//
// A key is one setting under the Unicode case folding the profile's decoder reads it with, so two
// keys of one table that differ only in case would resolve to whichever the decoder met first.
// MergeLayers refuses a layer that holds two, and layers that spell one key two ways, and its
// error names both spellings.
func MergeLayers(layers ...map[string]any) (map[string]any, error) {
	merged := map[string]any{}
	for _, layer := range layers {
		if err := mergeInto(merged, layer, nil); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

// CheckSpellings refuses a layer that holds two keys of one table that differ only in case, with a
// *SpellingError, in a table inside a list as in any other. It reads any decoded TOML, so a policy's body is
// held to it as a profile's layer is.
func CheckSpellings(layer map[string]any) error {
	_, err := MergeLayers(layer)
	return err
}

// SpellingError is two keys of one table that differ only in case, which a decoder that reads keys under
// Unicode case folding takes for one setting, whichever it meets first. It is a bad profile.
type SpellingError struct {
	First, Second string
	// Table is the path of the table that holds both, and empty for the top level.
	Table []string
}

// Error names both spellings as a bad profile.
func (e *SpellingError) Error() string {
	return fmt.Sprintf("%v: %s, and a profile spells each key one way", ErrBadProfile, e.Clash())
}

// Unwrap makes the clash a bad profile.
func (e *SpellingError) Unwrap() error { return ErrBadProfile }

// Clash names the two spellings and the table that holds them.
func (e *SpellingError) Clash() string {
	return fmt.Sprintf("%q and %q spell one key%s", e.First, e.Second, inTable(e.Table))
}

// mergeInto folds from into into, which sits at path. The keys of into hold no two spellings of one key,
// because every earlier merge checked them, so it only has to compare the keys of from with them, and
// it walks those in order so that the same layers always give the same error.
func mergeInto(into, from map[string]any, path []string) error {
	spellings := make(map[string]string, len(into)+len(from))
	for key := range into {
		spellings[foldKey(key)] = key
	}
	for _, key := range slices.Sorted(maps.Keys(from)) {
		folded := foldKey(key)
		if other, taken := spellings[folded]; taken && other != key {
			return &SpellingError{First: other, Second: key, Table: slices.Clone(path)}
		}
		spellings[folded] = key
		value, here := from[key], append(slices.Clone(path), key)
		if err := checkListed(value, here); err != nil {
			return err
		}
		if isUnion(here) {
			existing, has := into[key]
			into[key] = unionOf(existing, has, value)
			continue
		}
		table, isTable := value.(map[string]any)
		existing, hasTable := into[key].(map[string]any)
		if isTable && hasTable {
			if err := mergeInto(existing, table, here); err != nil {
				return err
			}
			continue
		}
		if isTable {
			fresh := map[string]any{}
			if err := mergeInto(fresh, table, here); err != nil {
				return err
			}
			into[key] = fresh
			continue
		}
		into[key] = value
	}
	return nil
}

// checkListed holds every table inside a list to the rule a table of a layer is held to, whether the decoder
// made the list of maps, as it does for an array of tables, or of any, as it does for an inline list, and
// however deep the lists nest. A list is replaced as a whole when layers merge, never folded element by
// element, so each table is checked alone, and a clash names it by its place in the list, such as
// `classes[1]`. A value that is no list holds none.
func checkListed(value any, path []string) error {
	switch list := value.(type) {
	case []map[string]any:
		for i, table := range list {
			if err := mergeInto(map[string]any{}, table, elementPath(path, i)); err != nil {
				return err
			}
		}
	case []any:
		for i, element := range list {
			here := elementPath(path, i)
			if table, isTable := element.(map[string]any); isTable {
				if err := mergeInto(map[string]any{}, table, here); err != nil {
					return err
				}
				continue
			}
			if err := checkListed(element, here); err != nil {
				return err
			}
		}
	}
	return nil
}

// elementPath is the path of the element of the list at path, with its place in the list on its last part.
func elementPath(path []string, index int) []string {
	here := slices.Clone(path)
	here[len(here)-1] += fmt.Sprintf("[%d]", index)
	return here
}

// isUnion says path is a setting of unionSettings, in any spelling the decoder reads as it.
func isUnion(path []string) bool {
	for _, setting := range unionSettings {
		if slices.EqualFunc(path, setting, strings.EqualFold) {
			return true
		}
	}
	return false
}

// unionOf adds a layer's list to the union so far. A value that is no list stays in the profile, and the
// decoder refuses it, so a layer that sets the list to something else, or follows one that did, cannot
// leave out the entries of the layers below it.
func unionOf(existing any, has bool, value any) any {
	added, isList := value.([]any)
	if !has {
		if !isList {
			return value
		}
		return union(nil, added)
	}
	current, wasList := existing.([]any)
	switch {
	case !wasList:
		return existing
	case !isList:
		return value
	}
	return union(current, added)
}

// union is the entries of both lists, in order, with each name once. An entry that is no name is kept as it
// is, for the decoder to refuse.
func union(first, second []any) []any {
	out := make([]any, 0, len(first)+len(second))
	seen := map[string]bool{}
	for _, entry := range slices.Concat(first, second) {
		if name, isName := entry.(string); isName {
			if seen[name] {
				continue
			}
			seen[name] = true
		}
		out = append(out, entry)
	}
	return out
}

// foldKey names a key so that two keys share a name exactly when strings.EqualFold calls them equal: it
// replaces each rune with the least one of its simple case folding orbit.
func foldKey(key string) string {
	var folded strings.Builder
	for _, r := range key {
		least := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			least = min(least, next)
		}
		folded.WriteRune(least)
	}
	return folded.String()
}

// inTable says where a refusal happened: the table at path, or nothing at the top of a layer.
func inTable(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return " of [" + strings.Join(path, ".") + "]"
}

// The tokens a worktree root's template replaces: the repository's clone folder's base name, and the ID of
// the project the lane belongs to.
const (
	RepositoryToken = "{repo}"
	ProjectToken    = "{project}"
)

// The layers a worktree root can come from, as a refusal names them.
const (
	RootFromDefault    = "the built-in root"
	RootFromUser       = "the user's profile layer"
	RootFromRepository = "the repository's profile layer"
)

// DefaultProject stands for the project of a lane whose lease names none, which is the installation's only
// project.
const DefaultProject = "default"

// Placement says where one lane works.
type Placement struct {
	Path   string
	Branch string
}

// Place computes a lane's worktree and branch. A root is relative to the repository when it is not
// absolute, so `../{repo}-lanes` keeps lanes beside the repository and out of it. The project is the ID of
// the lane's project, and an empty one is DefaultProject: the ID, because a rename changes the name and the
// root is computed again for every generation.
func Place(settings WorktreeSettings, repository, project, lane, branch string) (Placement, error) {
	if !path.IsAbs(repository) {
		return Placement{}, fmt.Errorf("%w: repository %q is not absolute", ErrBadProfile, repository)
	}
	if project == "" {
		project = DefaultProject
	}
	if strings.ContainsAny(project, "/\\") || project == ".." || project == "." {
		return Placement{}, fmt.Errorf("%w: no place for project %q", ErrBadProfile, project)
	}
	if settings.Root == "" || lane == "" || strings.ContainsAny(lane, "/\\") || lane == ".." {
		return Placement{}, fmt.Errorf("%w: no place for lane %q under %q", ErrBadProfile, lane, settings.Root)
	}
	root := strings.NewReplacer(RepositoryToken, path.Base(repository), ProjectToken, project).Replace(settings.Root)
	if !path.IsAbs(root) {
		root = path.Join(repository, root)
	}
	if branch == "" {
		branch = settings.BranchPrefix + lane
	}
	return Placement{Path: path.Join(root, lane), Branch: branch}, nil
}
