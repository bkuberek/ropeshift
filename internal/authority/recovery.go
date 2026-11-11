package authority

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// PolicyRecovery bounds what an orchestrator may do to recover stuck work with no member (FR-ORCH-8). It
// bounds orchestrators and not members, and only a member publishes a version of it.
const PolicyRecovery PolicyKind = "recovery"

// ErrBadRecoveryPolicy marks a recovery policy a workspace cannot run on.
var ErrBadRecoveryPolicy = errors.New("invalid recovery policy")

// RecoveryPolicy is the body of a recovery policy version. Each switch allows one act, and
// MaxBreakerResetsPerDay bounds the breaker resets orchestrators make on one work order's lanes in a day.
type RecoveryPolicy struct {
	Version                int64 `toml:"version" json:"version"`
	RestartStalled         bool  `toml:"restart_stalled" json:"restart_stalled"`
	MaxBreakerResetsPerDay int64 `toml:"max_breaker_resets_per_day" json:"max_breaker_resets_per_day"`
	RetryFailed            bool  `toml:"retry_failed" json:"retry_failed"`
	ReplanBlocked          bool  `toml:"replan_blocked" json:"replan_blocked"`
}

// RecoveryPolicyFile is the table a member publishes.
type RecoveryPolicyFile struct {
	RecoveryPolicy RecoveryPolicy `toml:"recovery_policy"`
}

// RecoveryPolicyDraft is a recovery policy as written, and what it says.
type RecoveryPolicyDraft struct {
	Body   string
	Policy RecoveryPolicy
}

// BuiltInRecoveryPolicyBody is what a workspace runs on until a member publishes one: the values the
// design states. Each act it allows is reversible or bounded, and anything past it reaches a member.
const BuiltInRecoveryPolicyBody = `[recovery_policy]
version = 1
restart_stalled = true
max_breaker_resets_per_day = 2
retry_failed = true
replan_blocked = true
`

// DefaultRecoveryPolicy is what BuiltInRecoveryPolicyBody says.
func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{Version: 1, RestartStalled: true, MaxBreakerResetsPerDay: 2, RetryFailed: true, ReplanBlocked: true}
}

// Check accepts a policy a workspace can run on.
func (p RecoveryPolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: version %d", ErrBadRecoveryPolicy, p.Version)
	}
	if p.MaxBreakerResetsPerDay < 0 {
		return fmt.Errorf("%w: max_breaker_resets_per_day %d is below zero", ErrBadRecoveryPolicy, p.MaxBreakerResetsPerDay)
	}
	return nil
}

// NewRecoveryPolicyVersion stores a draft under a generated ID.
func NewRecoveryPolicyVersion(id, authoredBy string, draft RecoveryPolicyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyRecovery, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadRecoveryPolicy reads a version's settings, once its body is what its digest covers.
func ReadRecoveryPolicy(version PolicyVersion) (RecoveryPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return RecoveryPolicy{}, err
	}
	var policy RecoveryPolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return RecoveryPolicy{}, err
	}
	return policy, nil
}

// RecoveryAct names one of an orchestrator's recovery tools, and the member operation that does the same.
// A takeover is never one (FR-WO-4).
type RecoveryAct string

// The recovery acts (ADR-0014).
const (
	RecoverRestart RecoveryAct = "lane_restart"
	RecoverReset   RecoveryAct = "lane_reset"
	RecoverRetry   RecoveryAct = "work_retry"
	RecoverReplan  RecoveryAct = "work_replan"
)

// RecoveryActs lists every act.
func RecoveryActs() []RecoveryAct {
	return []RecoveryAct{RecoverRestart, RecoverReset, RecoverRetry, RecoverReplan}
}

// KnownRecoveryAct says an act is one of the recovery acts.
func KnownRecoveryAct(act RecoveryAct) bool { return slices.Contains(RecoveryActs(), act) }

// LaneAct says an act works on a work order's current lane rather than on the work order alone.
func (a RecoveryAct) LaneAct() bool { return a == RecoverRestart || a == RecoverReset }

// The checks of the recovery policy. A refusal names the one that failed.
const (
	CheckUnknownAct     = "unknown_act"
	CheckRestartStalled = "restart_stalled"
	CheckSameMachine    = "same_machine"
	CheckDayCap         = "daily_generation_cap"
	CheckBreakerResets  = "max_breaker_resets_per_day"
	CheckRetryFailed    = "retry_failed"
	CheckAttempts       = "max_attempts"
	CheckReplanBlocked  = "replan_blocked"
)

// RecoveryFacts is what a check reads of an act's subject, once the subject is in the state the act
// starts from.
type RecoveryFacts struct {
	Act RecoveryAct
	// OnMachine says the lane runs on the calling orchestrator's own machine.
	OnMachine bool
	// DayCapped says the breaker tripped on the lane's daily cap on generations, not on its window.
	DayCapped bool
	// ResetsToday counts the breaker resets orchestrators made on this work order's lanes in the last day.
	ResetsToday int64
	// Attempts and MaxAttempts are the work order's. A work order that names no bound has one attempt.
	Attempts    int64
	MaxAttempts int64
}

// Refusal is the check a policy refused an act on, and the limit it went past, in words a member reads.
type Refusal struct {
	Check string `json:"check"`
	Limit string `json:"limit"`
}

// Judge holds an act to the policy. It says nothing of whether the subject is in the state the act
// starts from, which a caller settles first: a subject another actor moved is a conflict, not a refusal.
func (p RecoveryPolicy) Judge(f RecoveryFacts) (Refusal, bool) {
	refuse := func(check, limit string) (Refusal, bool) { return Refusal{Check: check, Limit: limit}, true }
	switch f.Act {
	case RecoverRestart:
		if !p.RestartStalled {
			return refuse(CheckRestartStalled, "restart_stalled is false")
		}
		if !f.OnMachine {
			return refuse(CheckSameMachine, "the lane runs on another machine than this orchestrator's")
		}
	case RecoverReset:
		if !f.OnMachine {
			return refuse(CheckSameMachine, "the lane runs on another machine than this orchestrator's")
		}
		if f.DayCapped {
			return refuse(CheckDayCap, "the breaker tripped on the lane's daily cap on generations, which only a member's reset overrides")
		}
		if f.ResetsToday >= p.MaxBreakerResetsPerDay {
			return refuse(CheckBreakerResets, fmt.Sprintf("orchestrators reset this work order's breaker %d of max_breaker_resets_per_day %d times in the last day",
				f.ResetsToday, p.MaxBreakerResetsPerDay))
		}
	case RecoverRetry:
		if !p.RetryFailed {
			return refuse(CheckRetryFailed, "retry_failed is false")
		}
		if bound := max(f.MaxAttempts, 1); f.Attempts >= bound {
			return refuse(CheckAttempts, fmt.Sprintf("the work order used %d of max_attempts %d", f.Attempts, bound))
		}
	case RecoverReplan:
		if !p.ReplanBlocked {
			return refuse(CheckReplanBlocked, "replan_blocked is false")
		}
	default:
		return refuse(CheckUnknownAct, fmt.Sprintf("%q is no recovery act", f.Act))
	}
	return Refusal{}, false
}
