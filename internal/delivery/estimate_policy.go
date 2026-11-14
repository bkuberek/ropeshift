package delivery

import (
	"encoding/json"

	"ropeshift.com/ropeshift/internal/authority"
)

// PolicyEstimate is the kind of the estimate policy, a project's own, which only a member publishes and no
// calibration process writes (ADR-0026).
const PolicyEstimate authority.PolicyKind = "estimate"

// EstimatePolicyFile is the table a member publishes.
type EstimatePolicyFile struct {
	EstimatePolicy EstimatePolicy `toml:"estimate_policy"`
}

// EstimatePolicyDraft is an estimate policy as written, and what it says.
type EstimatePolicyDraft struct {
	Body   string
	Policy EstimatePolicy
}

// BuiltInEstimatePolicyBody is what a project runs on until a member publishes one: the values ADR-0026 states,
// with the project's delivery history kept to itself.
const BuiltInEstimatePolicyBody = `[estimate_policy]
version = 1
min_samples = 10
recent_samples = 50
forecast_trials = 2000
share_history = false
`

// NewEstimatePolicyVersion stores a draft under a generated ID.
func NewEstimatePolicyVersion(id, authoredBy string, draft EstimatePolicyDraft, createdAt int64) (authority.PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return authority.PolicyVersion{
		ID: id, Kind: PolicyEstimate, Version: draft.Policy.Version, Digest: authority.PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadEstimatePolicy reads a version's settings, once its body is what its digest covers.
func ReadEstimatePolicy(version authority.PolicyVersion) (EstimatePolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return EstimatePolicy{}, err
	}
	var policy EstimatePolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return EstimatePolicy{}, err
	}
	return policy, nil
}
