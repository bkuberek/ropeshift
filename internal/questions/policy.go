package questions

import (
	"encoding/json"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/authority"
)

// ReversibleAssume lets an orchestrator record an assumption for a reversible choice. It is
// the only value the design defines for reversible_default.
const ReversibleAssume = "assume"

// builtInMaxAssumptions is the sample policy's limit.
const builtInMaxAssumptions = 5

// BuiltInPolicyBody is the first version of a workspace's decision policy.
const BuiltInPolicyBody = `[decision_policy]
version = 1
reversible_default = "assume"
one_way_categories = ["security", "compliance", "data"]
max_assumptions_per_work_order = 5
`

// Policy is a workspace's decision policy: what an orchestrator may decide and assume.
type Policy struct {
	Version                    int64    `json:"version" toml:"version"`
	ReversibleDefault          string   `json:"reversible_default" toml:"reversible_default"`
	OneWayCategories           []string `json:"one_way_categories" toml:"one_way_categories"`
	MaxAssumptionsPerWorkOrder int64    `json:"max_assumptions_per_work_order" toml:"max_assumptions_per_work_order"`
}

// PolicyFile is the TOML table a member writes.
type PolicyFile struct {
	DecisionPolicy Policy `toml:"decision_policy"`
}

// PolicyDraft is a decision policy a member publishes: the table as written, and what it
// says. The API reads the one from the other.
type PolicyDraft struct {
	Body   string
	Policy Policy
}

// DefaultPolicy is what BuiltInPolicyBody says.
func DefaultPolicy() Policy {
	return Policy{
		Version: 1, ReversibleDefault: ReversibleAssume,
		OneWayCategories:           []string{CategorySecurity, CategoryCompliance, CategoryData},
		MaxAssumptionsPerWorkOrder: builtInMaxAssumptions,
	}
}

// Check accepts a policy a workspace can run on. An unknown category is refused, because a
// misspelled one would silently leave a gate open.
func (p Policy) Check(added []string) error {
	if p.Version < 1 {
		return fmt.Errorf("%w: a decision policy names its version", ErrInvalid)
	}
	if p.ReversibleDefault != ReversibleAssume {
		return fmt.Errorf("%w: reversible_default %q is not %q", ErrInvalid, p.ReversibleDefault, ReversibleAssume)
	}
	if p.MaxAssumptionsPerWorkOrder < 0 {
		return fmt.Errorf("%w: max_assumptions_per_work_order is below zero", ErrInvalid)
	}
	return CheckCategories(p.OneWayCategories, added)
}

// NewPolicyVersion is a checked draft as a record. Version 1 is the built-in one, so a
// member's first version is 2 or later.
func NewPolicyVersion(id, authoredBy string, draft PolicyDraft, createdAt int64) (authority.PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return authority.PolicyVersion{
		ID: id, Kind: authority.PolicyDecision, Version: draft.Policy.Version, Digest: authority.PolicyDigest(draft.Body),
		Body: draft.Body, Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadPolicy reads a version's settings, once its body is what its digest covers.
func ReadPolicy(version authority.PolicyVersion) (Policy, error) {
	if err := version.CheckDigest(); err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return Policy{}, fmt.Errorf("decision policy version %d: %w", version.Version, err)
	}
	return policy, nil
}

// Open reports categories an orchestrator may decide in. One category the policy forces to
// one-way is enough to refuse.
func (p Policy) Open(categories []string) bool {
	for _, category := range categories {
		if slices.Contains(p.OneWayCategories, category) {
			return false
		}
	}
	return true
}

// MayDecide reports whether an orchestrator may decide or assume a question.
func (p Policy) MayDecide(q Question) bool { return p.Open(q.Categories) }
