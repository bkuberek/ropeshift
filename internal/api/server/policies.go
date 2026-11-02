package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/BurntSushi/toml"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

// decisionPolicyTable is the table a decision policy's body holds.
const decisionPolicyTable = "decision_policy"

// lanePolicyTable is the table a lane policy's body holds.
const lanePolicyTable = "lane_policy"

// policyKeys are the settings a body must state. A key left out is refused, because its zero
// value would close or open a gate that nobody chose.
var policyKeys = []string{"version", "reversible_default", "one_way_categories", "max_assumptions_per_work_order"}

// lanePolicyKeys are the settings a lane policy must state.
var lanePolicyKeys = []string{"version", "on_contract_version", "on_one_way_decision"}

// readPolicy reads a body as its author wrote it. The API is where TOML ends: the control
// plane holds no parser, and it keeps the body with what this returns.
func readPolicy(body string) (questions.PolicyDraft, error) {
	var file questions.PolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return questions.PolicyDraft{}, fmt.Errorf("%w: %v", questions.ErrInvalid, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return questions.PolicyDraft{}, fmt.Errorf("%w: unknown setting %s", questions.ErrInvalid, unknown[0])
	}
	for _, key := range policyKeys {
		if !meta.IsDefined(decisionPolicyTable, key) {
			return questions.PolicyDraft{}, fmt.Errorf("%w: a decision policy states %s", questions.ErrInvalid, key)
		}
	}
	return questions.PolicyDraft{Body: body, Policy: file.DecisionPolicy}, nil
}

// readLanePolicy reads a body whose table is `lane_policy`, on the same terms.
func readLanePolicy(body string) (authority.LanePolicyDraft, error) {
	var file authority.LanePolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.LanePolicyDraft{}, fmt.Errorf("%w: %v", authority.ErrBadLanePolicy, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.LanePolicyDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrBadLanePolicy, unknown[0])
	}
	for _, key := range lanePolicyKeys {
		if !meta.IsDefined(lanePolicyTable, key) {
			return authority.LanePolicyDraft{}, fmt.Errorf("%w: a lane policy states %s", authority.ErrBadLanePolicy, key)
		}
	}
	return authority.LanePolicyDraft{Body: body, Policy: file.LanePolicy}, nil
}

// holdsLanePolicy says a body is a lane policy, which the table it names decides.
func holdsLanePolicy(body string) bool {
	var tables map[string]any
	if _, err := toml.Decode(body, &tables); err != nil {
		return false
	}
	_, found := tables[lanePolicyTable]
	return found
}

func (h *handler) publishPolicy(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.PublishPolicy
	if !decode(w, r, &in) {
		return
	}
	var record authority.PolicyVersion
	if holdsLanePolicy(in.Body) {
		draft, err := readLanePolicy(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.questions.PublishLanePolicy(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
	draft, err := readPolicy(in.Body)
	if err != nil {
		fail(w, err)
		return
	}
	record, err = h.questions.PublishPolicy(r.Context(), h.Scope, draft, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

// policyVersions reads the versions of the kind a request names, and the decision policy when it
// names none.
func (h *handler) policyVersions(r *http.Request) ([]authority.PolicyVersion, error) {
	if r.URL.Query().Get("kind") == string(authority.PolicyLane) {
		return h.questions.LanePolicyVersions(r.Context(), h.Scope)
	}
	return h.questions.PolicyVersions(r.Context(), h.Scope)
}

func (h *handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	versions, err := h.policyVersions(r)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.PolicyList{Versions: versions})
}

func (h *handler) showPolicy(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil {
		invalid(w, "version %q is not a number", r.PathValue("version"))
		return
	}
	versions, err := h.policyVersions(r)
	if err != nil {
		fail(w, err)
		return
	}
	for _, row := range versions {
		if row.Version == version {
			reply(w, http.StatusOK, row)
			return
		}
	}
	fail(w, fmt.Errorf("policy version %d: %w", version, ports.ErrNotFound))
}

func (h *handler) overturnDecision(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.OverturnDecision
	if !decode(w, r, &in) {
		return
	}
	result, err := h.questions.Overturn(r.Context(), h.Scope, r.PathValue("id"), questions.Overturn{
		DecisionID: in.ID, Statement: in.Statement, Rationale: in.Rationale, Citations: in.Citations,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, result)
}
