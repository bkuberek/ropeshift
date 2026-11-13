package authority

import (
	"encoding/json"
	"fmt"
	"time"
)

// PolicyAssistant governs the assistant's sessions: when an idle one stops, and when one that has had no
// message since it started stops (ADR-0024). It is the
// installation's, as autonomy is, because a member's assistant sessions run on workers every project shares.
const PolicyAssistant PolicyKind = "assistant"

// The bounds of an idle stop, which hold a warm idle stop too.
const (
	MinAssistantIdleStop = time.Minute
	MaxAssistantIdleStop = 24 * time.Hour
)

// DefaultAssistantWarmIdleStop is how long a session that has had no message since it started runs, where
// a policy names no warm idle stop.
const DefaultAssistantWarmIdleStop = 2 * time.Minute

// AssistantPolicy is the body of an assistant policy version. IdleStop is a duration, such as "15m", after
// which an idle session stops. WarmIdleStop is the shorter one for a session started ahead of a message, on
// a member's first keystroke, that has had none since it started; once a message reaches it, IdleStop
// holds.
type AssistantPolicy struct {
	Version      int64  `toml:"version" json:"version"`
	IdleStop     string `toml:"idle_stop" json:"idle_stop"`
	WarmIdleStop string `toml:"warm_idle_stop" json:"warm_idle_stop,omitempty"`
}

// AssistantPolicyFile is the table a member publishes.
type AssistantPolicyFile struct {
	Assistant AssistantPolicy `toml:"assistant"`
}

// AssistantDraft is an assistant policy as written, and what it says.
type AssistantDraft struct {
	Body   string
	Policy AssistantPolicy
}

// BuiltInAssistantBody is what an installation runs on until a member publishes one.
const BuiltInAssistantBody = `[assistant]
version = 1
idle_stop = "15m"
warm_idle_stop = "2m"
`

// BuiltInAssistantPolicy is what BuiltInAssistantBody says.
func BuiltInAssistantPolicy() AssistantPolicy {
	return AssistantPolicy{Version: 1, IdleStop: "15m", WarmIdleStop: "2m"}
}

// Check accepts a policy whose idle stop is a duration from one minute to a day, and whose warm idle stop,
// when it names one, is too.
func (p AssistantPolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: assistant policy version %d", ErrInvalid, p.Version)
	}
	idle, err := time.ParseDuration(p.IdleStop)
	if err != nil || idle < MinAssistantIdleStop || idle > MaxAssistantIdleStop {
		return fmt.Errorf("%w: idle_stop is a duration from %s to %s, such as \"15m\"", ErrInvalid, "1m", "24h")
	}
	if p.WarmIdleStop == "" {
		return nil
	}
	warm, err := time.ParseDuration(p.WarmIdleStop)
	if err != nil || warm < MinAssistantIdleStop || warm > MaxAssistantIdleStop {
		return fmt.Errorf("%w: warm_idle_stop is a duration from %s to %s, such as \"2m\"", ErrInvalid, "1m", "24h")
	}
	return nil
}

// WarmIdleStopAfter is how long a session that has had no message since it started runs before it stops,
// and never longer than an idle one. A policy that names none, or none that parses, reads as two minutes.
func (p AssistantPolicy) WarmIdleStopAfter() time.Duration {
	warm, err := time.ParseDuration(p.WarmIdleStop)
	if err != nil || warm < MinAssistantIdleStop || warm > MaxAssistantIdleStop {
		warm = DefaultAssistantWarmIdleStop
	}
	return min(warm, p.IdleStopAfter())
}

// IdleStopAfter is how long an idle session runs before it stops. A policy that does not parse reads as
// the built-in one, which only a policy written before its check could hold.
func (p AssistantPolicy) IdleStopAfter() time.Duration {
	idle, err := time.ParseDuration(p.IdleStop)
	if err != nil || idle < MinAssistantIdleStop || idle > MaxAssistantIdleStop {
		return 15 * time.Minute
	}
	return idle
}

// NewAssistantVersion stores a draft under a generated ID.
func NewAssistantVersion(id, authoredBy string, draft AssistantDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyAssistant, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadAssistant reads a version's settings, once its body is what its digest covers.
func ReadAssistant(version PolicyVersion) (AssistantPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return AssistantPolicy{}, err
	}
	var policy AssistantPolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return AssistantPolicy{}, err
	}
	return policy, nil
}
