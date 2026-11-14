package authority

import (
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// PolicyAutonomy governs how far a worker goes with no person present (D24).
const PolicyAutonomy PolicyKind = "autonomy"

// Level is how far a worker goes with no person present (FR-ORCH-6).
type Level string

// The levels. A member claims by hand at any of them, because a claim of a member's is the opposite
// of automated use.
const (
	// LevelManual leaves claims to members, and the orchestrator only works the question ladder.
	LevelManual Level = "manual"
	// LevelAttended lets the orchestrator claim inside a run window a member opened.
	LevelAttended Level = "attended"
	// LevelUnattended lets it claim whenever capacity exists.
	LevelUnattended Level = "unattended"
)

// Valid reports a level this build knows.
func (l Level) Valid() bool {
	return l == LevelManual || l == LevelAttended || l == LevelUnattended
}

// The length of a run window, in milliseconds. A window stands for a person being there, so it has
// an end a person chose, and a long one is a choice to make again.
const (
	DefaultWindowMs int64 = 4 * 60 * 60 * 1000
	MaxWindowMs     int64 = 12 * 60 * 60 * 1000
)

// The events of a worker's autonomy.
const (
	EventAutonomySet      = "autonomy.level_set"
	EventRiskAcknowledged = "autonomy.risk_acknowledged"
	EventWindowOpened     = "autonomy.window_opened"
	EventWindowClosed     = "autonomy.window_closed"
	// SubjectAutonomy names the subject of those events: a worker.
	SubjectAutonomy = "worker_autonomy"
)

// The ways a member's act is refused.
var (
	// ErrBadWindow means a run window cannot be opened as asked.
	ErrBadWindow = errors.New("invalid run window")
	// ErrWindowOpen means this worker has a window that stands, which a member closes first.
	ErrWindowOpen = errors.New("a run window is already open")
	// ErrRiskNotAcknowledged means a subscription worker stays below unattended until a member
	// acknowledges the stated risk for it.
	ErrRiskNotAcknowledged = errors.New("unattended on a subscription needs a member to acknowledge the stated risk")
	// ErrStaleStatement means an acknowledgement names text other than the current statement.
	ErrStaleStatement = errors.New("this acknowledges a statement other than the current one")
)

// AutonomyPolicy is the body of an autonomy policy version. Each default is where a sign-in kind
// starts, and the bounds hold a worker at `attended` or below.
type AutonomyPolicy struct {
	Version                   int64 `toml:"version" json:"version"`
	SubscriptionDefault       Level `toml:"subscription_default" json:"subscription_default"`
	APIKeyDefault             Level `toml:"api_key_default" json:"api_key_default"`
	AttendedMaxLanes          int64 `toml:"attended_max_lanes" json:"attended_max_lanes"`
	AttendedMaxRespawnsPerDay int64 `toml:"attended_max_respawns_per_day" json:"attended_max_respawns_per_day"`
}

// AutonomyPolicyFile is the table a member publishes.
type AutonomyPolicyFile struct {
	AutonomyPolicy AutonomyPolicy `toml:"autonomy_policy"`
}

// AutonomyDraft is an autonomy policy as written, and what it says.
type AutonomyDraft struct {
	Body   string
	Policy AutonomyPolicy
}

// BuiltInAutonomyBody is what a workspace runs on until a member publishes one. The bounds are
// proposed values.
const BuiltInAutonomyBody = `[autonomy_policy]
version = 1
subscription_default = "attended"
api_key_default = "unattended"
attended_max_lanes = 4
attended_max_respawns_per_day = 8
`

// DefaultAutonomyPolicy is what BuiltInAutonomyBody says.
func DefaultAutonomyPolicy() AutonomyPolicy {
	return AutonomyPolicy{
		Version: 1, SubscriptionDefault: LevelAttended, APIKeyDefault: LevelUnattended,
		AttendedMaxLanes: 4, AttendedMaxRespawnsPerDay: 8,
	}
}

// Check accepts a policy a workspace can run on. A bound is required, because a zero would read as no
// limit on a sign-in the policy exists to hold back.
func (p AutonomyPolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: version %d", ErrBadLanePolicy, p.Version)
	}
	for _, level := range []Level{p.SubscriptionDefault, p.APIKeyDefault} {
		if !level.Valid() {
			return fmt.Errorf("%w: %q is no autonomy level", ErrBadLanePolicy, level)
		}
	}
	if p.AttendedMaxLanes < 1 || p.AttendedMaxRespawnsPerDay < 1 {
		return fmt.Errorf("%w: the bounds at attended start at 1", ErrBadLanePolicy)
	}
	return nil
}

// NewAutonomyVersion stores a draft under a generated ID.
func NewAutonomyVersion(id, authoredBy string, draft AutonomyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyAutonomy, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadAutonomy reads a version's settings, once its body is what its digest covers.
func ReadAutonomy(version PolicyVersion) (AutonomyPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return AutonomyPolicy{}, err
	}
	var policy AutonomyPolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return AutonomyPolicy{}, err
	}
	return policy, nil
}

// WorkerAutonomy is what the ledger holds of one worker beyond the policy: the level a member chose,
// and who acknowledged which statement of the risk.
type WorkerAutonomy struct {
	WorkerID           string `json:"worker_id"`
	Level              Level  `json:"level,omitempty"`
	AcknowledgedBy     string `json:"acknowledged_by,omitempty"`
	AcknowledgedKind   string `json:"acknowledged_kind,omitempty"`
	AcknowledgedDigest string `json:"acknowledged_digest,omitempty"`
	AcknowledgedAt     int64  `json:"acknowledged_at,omitempty"`
}

// Acknowledged says a member stood behind the statement as it reads now. A statement that changed
// asks again, because nobody has read the new one.
func (w WorkerAutonomy) Acknowledged() bool {
	return w.AcknowledgedBy != "" && w.AcknowledgedDigest == RiskDigest()
}

// keyed says a sign-in kind stands on a key the account's owner pays for by use. Any other kind, and
// a kind nobody could read, is held as a subscription, because that errs towards a person being there.
func keyed(kind string) bool { return kind == ledger.SignInAPIKey }

// startsAt is where a sign-in kind starts.
func (p AutonomyPolicy) startsAt(kind string) Level {
	if keyed(kind) {
		return p.APIKeyDefault
	}
	return p.SubscriptionDefault
}

// LevelFor reads a worker's level: what a member chose, or what its sign-in starts at. `unattended`
// on anything but a key stands only on a current acknowledgement, whoever asked for it, so the rule
// holds when a policy names it, and when a worker that held a key comes back on a subscription.
func (p AutonomyPolicy) LevelFor(kind string, setting WorkerAutonomy) Level {
	level := setting.Level
	if level == "" {
		level = p.startsAt(kind)
	}
	if level == LevelUnattended && !keyed(kind) && !setting.Acknowledged() {
		return LevelAttended
	}
	return level
}

// RunWindow is a stretch of time a member opened, inside which an `attended` worker's orchestrator
// claims. It closes on its own, so a person who walks away leaves nothing open.
type RunWindow struct {
	ID       string `json:"id"`
	WorkerID string `json:"worker_id"`
	OpenedBy string `json:"opened_by"`
	// OpenedKind is the kind of principal that opened it, which the store holds to a member.
	OpenedKind string `json:"opened_kind"`
	OpenedAt   int64  `json:"opened_at"`
	ClosesAt   int64  `json:"closes_at"`
	ClosedBy   string `json:"closed_by,omitempty"`
	ClosedAt   int64  `json:"closed_at,omitempty"`
}

// Stands says the window is open at a time.
func (w RunWindow) Stands(now int64) bool {
	return w.ClosedAt == 0 && w.OpenedAt <= now && now < w.ClosesAt
}

// BoundWindow reads a length a member asked for. Nothing asked takes the default, and a length past
// the bound stops there.
func BoundWindow(ms int64) (int64, error) {
	switch {
	case ms < 0:
		return 0, fmt.Errorf("%w: a length of %d", ErrBadWindow, ms)
	case ms == 0:
		return DefaultWindowMs, nil
	}
	return min(ms, MaxWindowMs), nil
}

// AutonomyStanding is where a worker stands now.
type AutonomyStanding struct {
	Level      Level
	WindowOpen bool
}

// MayClaim says this worker's orchestrator may take new work now. A level this build does not know
// claims nothing.
func (s AutonomyStanding) MayClaim() bool {
	switch s.Level {
	case LevelUnattended:
		return true
	case LevelAttended:
		return s.WindowOpen
	}
	return false
}

// MayWork says an orchestrator does more than work the question ladder.
func (s AutonomyStanding) MayWork() bool {
	return s.Level == LevelAttended || s.Level == LevelUnattended
}

// AutonomyView is where a worker stands, as a person reads it. Chosen is what a member asked for, and
// Level is what holds, which differ when the stated risk stands in between.
type AutonomyView struct {
	AsOf              int64      `json:"as_of"`
	WorkerID          string     `json:"worker_id"`
	SignIn            string     `json:"sign_in"`
	Level             Level      `json:"level"`
	Chosen            Level      `json:"chosen_level,omitempty"`
	Acknowledged      bool       `json:"risk_acknowledged"`
	Window            *RunWindow `json:"window,omitempty"`
	MayClaim          bool       `json:"may_claim"`
	MaxLanes          int64      `json:"max_lanes,omitempty"`
	MaxRespawnsPerDay int64      `json:"max_respawns_per_day,omitempty"`
}

// RiskStatement is what a member acknowledges before a subscription worker runs unattended. It states
// the terms and promises nothing: ropeshift cannot make this use acceptable, and an acknowledgement
// moves the risk to a person and removes none of it.
const RiskStatement = `Running this worker unattended means an orchestrator claims work and starts Claude Code sessions on your subscription, with nobody present.

Anthropic's Consumer Terms restrict access "through automated or non-human means" unless Anthropic explicitly permits it, and its advertised usage limits "assume ordinary, individual usage". Whether this is ordinary, individual usage is Anthropic's decision. Ropeshift cannot make that decision, and running the unmodified Claude Code binary does not settle it. This is not legal advice.

Anthropic may rate-limit, restrict, or suspend an account, and it may do so without prior notice. Each session draws on your plan's limits, and these terms can change.

This acknowledgement records that a member read this statement and accepts the risk for this worker. It is not permission from Anthropic, and it removes none of the risk. An API key avoids this question, and running attended reduces it without removing it.

Read the current pages before you acknowledge this:

- https://www.anthropic.com/legal/consumer-terms
- https://code.claude.com/docs/en/legal-and-compliance
- https://support.claude.com/en/articles/15036540
`

// RiskDigest names the statement an acknowledgement covers.
func RiskDigest() string { return PolicyDigest(RiskStatement) }

// AutonomyEvent records one change, with the record a replay reads.
func AutonomyEvent(kind, worker string, record any, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{Type: kind, Actor: actor, Subject: ledger.Subject{Kind: SubjectAutonomy, ID: worker}, Data: data}, nil
}

// ReplayAutonomy rebuilds the records from their events, so a store can be held to what it was told.
// Only a member's event counts: no other principal can write one, so one that says otherwise is no
// record of anything.
func ReplayAutonomy(events []ledger.Event) (map[string]WorkerAutonomy, map[string]RunWindow, error) {
	settings, windows := map[string]WorkerAutonomy{}, map[string]RunWindow{}
	for _, event := range events {
		if event.Subject.Kind != SubjectAutonomy || event.Actor.Kind != ledger.ActorMember {
			continue
		}
		switch event.Type {
		case EventAutonomySet, EventRiskAcknowledged:
			var row WorkerAutonomy
			if err := json.Unmarshal(event.Data, &row); err != nil {
				return nil, nil, err
			}
			settings[row.WorkerID] = row
		case EventWindowOpened, EventWindowClosed:
			var row RunWindow
			if err := json.Unmarshal(event.Data, &row); err != nil {
				return nil, nil, err
			}
			windows[row.ID] = row
		}
	}
	return settings, windows, nil
}
