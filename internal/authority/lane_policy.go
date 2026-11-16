package authority

import (
	"encoding/json"
	"errors"
	"fmt"
)

// PolicyLane governs what a change under a running lane does to it (D24).
const PolicyLane PolicyKind = "lane"

// What a change under a running lane does. A lane is told either way, and the next generation
// reads the fact either way.
const (
	LaneNotify = "notify"
	LanePark   = "park"
)

// ErrBadLanePolicy marks a lane policy a workspace cannot run on.
var ErrBadLanePolicy = errors.New("invalid lane policy")

// LanePolicy is the body of a lane policy version (FR-PLAN-8).
type LanePolicy struct {
	Version           int64  `toml:"version" json:"version"`
	OnContractVersion string `toml:"on_contract_version" json:"on_contract_version"`
	// OnBindingDecision is what a decision that binds a lane's work does to the lane (ADR-0030). Unset
	// tells it, as the built-in policy does.
	OnBindingDecision string `toml:"on_binding_decision" json:"on_binding_decision,omitempty"`
	// OnOneWayDecision, when a policy sets it, overrides OnBindingDecision for a one-way decision, so a
	// member can park on what cannot be undone and tell on the rest. Unset follows OnBindingDecision.
	OnOneWayDecision string `toml:"on_one_way_decision" json:"on_one_way_decision,omitempty"`
}

// BindingResponse is what a decision that binds a lane's work does to the lane: notify or park.
func (p LanePolicy) BindingResponse(oneWay bool) string {
	if oneWay && p.OnOneWayDecision != "" {
		return p.OnOneWayDecision
	}
	if p.OnBindingDecision != "" {
		return p.OnBindingDecision
	}
	return LaneNotify
}

// LanePolicyFile is the table a member publishes.
type LanePolicyFile struct {
	LanePolicy LanePolicy `toml:"lane_policy"`
}

// LanePolicyDraft is a lane policy as written, and what it says.
type LanePolicyDraft struct {
	Body   string
	Policy LanePolicy
}

// BuiltInLanePolicyBody is what a workspace runs on until a member publishes one. It tells a lane
// and leaves the choice to a person, because parking a lane is the costlier mistake.
const BuiltInLanePolicyBody = `[lane_policy]
version = 1
on_contract_version = "notify"
on_binding_decision = "notify"
`

// DefaultLanePolicy is what BuiltInLanePolicyBody says.
func DefaultLanePolicy() LanePolicy {
	return LanePolicy{Version: 1, OnContractVersion: LaneNotify, OnBindingDecision: LaneNotify}
}

// Check accepts a policy a workspace can run on. A policy states what a contract change does to a lane. The
// keys for a decision are optional, so a policy published before them keeps its meaning.
func (p LanePolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: version %d", ErrBadLanePolicy, p.Version)
	}
	if err := checkLaneResponse(p.OnContractVersion); err != nil {
		return err
	}
	for _, value := range []string{p.OnBindingDecision, p.OnOneWayDecision} {
		if value == "" {
			continue
		}
		if err := checkLaneResponse(value); err != nil {
			return err
		}
	}
	return nil
}

func checkLaneResponse(value string) error {
	if value != LaneNotify && value != LanePark {
		return fmt.Errorf("%w: %q is neither %s nor %s", ErrBadLanePolicy, value, LaneNotify, LanePark)
	}
	return nil
}

// NewLanePolicyVersion stores a draft under a generated ID.
func NewLanePolicyVersion(id, authoredBy string, draft LanePolicyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyLane, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadLanePolicy reads a version's settings, once its body is what its digest covers.
func ReadLanePolicy(version PolicyVersion) (LanePolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return LanePolicy{}, err
	}
	var policy LanePolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return LanePolicy{}, err
	}
	return policy, nil
}
