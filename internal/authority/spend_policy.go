package authority

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// PolicySpend holds the values that stop agent sessions from spending on waste (ADR-0037). Every value is a
// named key with a default, and only a member publishes a version, so no plan, repository file, or agent can
// move one. With no version published, the built-in one is in force.
const PolicySpend PolicyKind = "spend"

// ErrBadSpendPolicy marks a spend policy a project cannot run on.
var ErrBadSpendPolicy = errors.New("invalid spend policy")

// The wait rules a spend policy names. A rule is a class of command that waits by construction.
var waitRuleIDs = []string{"W1", "W2", "W3", "W4"}

// The ranges of the wait keys. A live wait or a sleep in a tool call that no bound held would be the waste the
// policy exists to stop, and a pattern list that no bound held would be a hook that reads without end.
const (
	minLiveWait         = time.Minute
	maxLiveWait         = time.Hour
	minToolSleep        = time.Second
	maxToolSleep        = time.Hour
	maxPatternsPerRule  = 64
	maxPatternBytes     = 200
	defaultMaxLiveWait  = "5m"
	defaultMaxToolSleep = "60s"
)

// SpendPolicy is the body of a spend policy version, as far as this build reads it. Durations stay text, so a
// domain value carries no parsing rule, and a key a later build adds is refused here as an unknown setting.
type SpendPolicy struct {
	Version int64 `toml:"version" json:"version"`
	// MaxLiveWait is how long a lane may wait live. A longer `lane_wait` parks the lane.
	MaxLiveWait string `toml:"max_live_wait" json:"max_live_wait"`
	// MaxToolSleep is the sleep inside one command that wait rule W1 denies, and any longer one.
	MaxToolSleep string `toml:"max_tool_sleep" json:"max_tool_sleep"`
	// WaitRulesOff names the rules a member turned off, by ID.
	WaitRulesOff []string `toml:"wait_rules_off" json:"wait_rules_off"`
	// WaitRulePatterns are command prefixes a rule also matches, by rule ID.
	WaitRulePatterns map[string][]string `toml:"wait_rule_patterns" json:"wait_rule_patterns"`
}

// SpendPolicyFile is the table a member publishes.
type SpendPolicyFile struct {
	SpendPolicy SpendPolicy `toml:"spend_policy"`
}

// SpendPolicyDraft is a spend policy as written, and what it says.
type SpendPolicyDraft struct {
	Body   string
	Policy SpendPolicy
}

// BuiltInSpendPolicyBody is what a project runs on until a member publishes a version.
const BuiltInSpendPolicyBody = `[spend_policy]
version = 1
max_live_wait = "5m"
max_tool_sleep = "60s"
wait_rules_off = []
wait_rule_patterns = { W1 = [], W2 = [], W3 = [], W4 = [] }
`

// DefaultSpendPolicy is what BuiltInSpendPolicyBody says.
func DefaultSpendPolicy() SpendPolicy {
	return SpendPolicy{
		Version: 1, MaxLiveWait: defaultMaxLiveWait, MaxToolSleep: defaultMaxToolSleep, WaitRulesOff: []string{},
		WaitRulePatterns: map[string][]string{"W1": {}, "W2": {}, "W3": {}, "W4": {}},
	}
}

// LiveWait is the longest a lane waits live. A version that passed Check always parses.
func (p SpendPolicy) LiveWait() time.Duration {
	d, _ := time.ParseDuration(p.MaxLiveWait)
	return d
}

// ToolSleep is the sleep a command may hold before wait rule W1 denies it.
func (p SpendPolicy) ToolSleep() time.Duration {
	d, _ := time.ParseDuration(p.MaxToolSleep)
	return d
}

func spendRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadSpendPolicy}, args...)...)
}

func checkSpendDuration(key, text string, low, high time.Duration) error {
	d, err := time.ParseDuration(text)
	if err != nil {
		return spendRefusal("%s %q is not a duration, such as \"5m\"", key, text)
	}
	if d < low || d > high {
		return spendRefusal("%s is %s, and it runs from %s to %s", key, text, low, high)
	}
	return nil
}

// Check accepts a policy a project can run on. A value outside its key's range is refused, and the refusal
// names the key and the range.
func (p SpendPolicy) Check() error {
	if p.Version < 1 {
		return spendRefusal("version %d", p.Version)
	}
	if err := checkSpendDuration("max_live_wait", p.MaxLiveWait, minLiveWait, maxLiveWait); err != nil {
		return err
	}
	if err := checkSpendDuration("max_tool_sleep", p.MaxToolSleep, minToolSleep, maxToolSleep); err != nil {
		return err
	}
	for _, rule := range p.WaitRulesOff {
		if !slices.Contains(waitRuleIDs, rule) {
			return spendRefusal("wait_rules_off names %q, and the rules are %s", rule, strings.Join(waitRuleIDs, ", "))
		}
	}
	for rule, patterns := range p.WaitRulePatterns {
		if !slices.Contains(waitRuleIDs, rule) {
			return spendRefusal("wait_rule_patterns names %q, and the rules are %s", rule, strings.Join(waitRuleIDs, ", "))
		}
		if len(patterns) > maxPatternsPerRule {
			return spendRefusal("wait_rule_patterns holds %d patterns for %s, and a rule holds at most %d", len(patterns), rule, maxPatternsPerRule)
		}
		for _, pattern := range patterns {
			if strings.TrimSpace(pattern) == "" || len(pattern) > maxPatternBytes {
				return spendRefusal("wait_rule_patterns holds a pattern for %s that is empty or past %d bytes", rule, maxPatternBytes)
			}
		}
	}
	return nil
}

// NewSpendPolicyVersion stores a draft under a generated ID.
func NewSpendPolicyVersion(id, authoredBy string, draft SpendPolicyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicySpend, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadSpendPolicy reads a version's settings, once its body is what its digest covers.
func ReadSpendPolicy(version PolicyVersion) (SpendPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return SpendPolicy{}, err
	}
	var policy SpendPolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return SpendPolicy{}, err
	}
	return policy, nil
}
