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
	OnOneWayDecision  string `toml:"on_one_way_decision" json:"on_one_way_decision"`
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
on_one_way_decision = "notify"
`

// DefaultLanePolicy is what BuiltInLanePolicyBody says.
func DefaultLanePolicy() LanePolicy {
	return LanePolicy{Version: 1, OnContractVersion: LaneNotify, OnOneWayDecision: LaneNotify}
}

// Check accepts a policy a workspace can run on.
func (p LanePolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: version %d", ErrBadLanePolicy, p.Version)
	}
	for _, value := range []string{p.OnContractVersion, p.OnOneWayDecision} {
		if value != LaneNotify && value != LanePark {
			return fmt.Errorf("%w: %q is neither %s nor %s", ErrBadLanePolicy, value, LaneNotify, LanePark)
		}
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
