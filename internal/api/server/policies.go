package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
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

// recoveryPolicyTable is the table a recovery policy's body holds.
const recoveryPolicyTable = "recovery_policy"

// recoveryPolicyKeys are the settings a recovery policy must state. A switch left out would allow or
// refuse an act nobody chose.
var recoveryPolicyKeys = []string{"version", "restart_stalled", "max_breaker_resets_per_day", "retry_failed", "replan_blocked", "max_attempts"}

// readRecoveryPolicy reads a body whose table is `recovery_policy`, on the same terms as a lane policy.
func readRecoveryPolicy(body string) (authority.RecoveryPolicyDraft, error) {
	var file authority.RecoveryPolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.RecoveryPolicyDraft{}, fmt.Errorf("%w: %v", authority.ErrBadRecoveryPolicy, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.RecoveryPolicyDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrBadRecoveryPolicy, unknown[0])
	}
	for _, key := range recoveryPolicyKeys {
		if !meta.IsDefined(recoveryPolicyTable, key) {
			return authority.RecoveryPolicyDraft{}, fmt.Errorf("%w: a recovery policy states %s", authority.ErrBadRecoveryPolicy, key)
		}
	}
	return authority.RecoveryPolicyDraft{Body: body, Policy: file.RecoveryPolicy}, nil
}

// holdsLanePolicy says a body is a lane policy, which the table it names decides.
func holdsLanePolicy(body string) bool { return holdsTable(body, lanePolicyTable) }

// delegationTable names the table of a delegation policy.
const delegationTable = "delegation_policy"

// readDelegation reads a delegation policy as a member wrote it. A setting this build does not know
// stops it, because a class nobody reads would look declared.
func readDelegation(body string) (authority.DelegationDraft, error) {
	var file authority.DelegationPolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.DelegationDraft{}, fmt.Errorf("%w: %v", authority.ErrBadLanePolicy, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.DelegationDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrBadLanePolicy, unknown[0])
	}
	if !meta.IsDefined(delegationTable, "version") {
		return authority.DelegationDraft{}, fmt.Errorf("%w: a delegation policy states its version", authority.ErrBadLanePolicy)
	}
	return authority.DelegationDraft{Body: body, Policy: file.DelegationPolicy}, nil
}

// assistantTable names the table of the assistant policy.
const assistantTable = "assistant"

// readAssistantPolicy reads an assistant policy as a member wrote it. Both of its settings are stated,
// because an idle stop left out would read as no stop at all.
func readAssistantPolicy(body string) (authority.AssistantDraft, error) {
	var file authority.AssistantPolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.AssistantDraft{}, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.AssistantDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrInvalid, unknown[0])
	}
	for _, key := range []string{"version", "idle_stop"} {
		if !meta.IsDefined(assistantTable, key) {
			return authority.AssistantDraft{}, fmt.Errorf("%w: an assistant policy states %s", authority.ErrInvalid, key)
		}
	}
	return authority.AssistantDraft{Body: body, Policy: file.Assistant}, nil
}

func holdsTable(body, table string) bool {
	var tables map[string]any
	if _, err := toml.Decode(body, &tables); err != nil {
		return false
	}
	_, found := tables[table]
	return found
}

func (h *handler) publishPolicy(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.PublishPolicy
	if !decode(w, r, &in) {
		return
	}
	var record authority.PolicyVersion
	if holdsTable(in.Body, delegatePolicyTable) {
		draft, err := readDelegatePolicy(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.approvals.PublishDelegatePolicy(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
	if holdsTable(in.Body, notifyTable) {
		draft, err := readNotify(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.notices.PublishNotifyPolicy(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
	if holdsTable(in.Body, autonomyTable) {
		draft, err := readAutonomy(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.autonomy.PublishAutonomyPolicy(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
	if holdsTable(in.Body, assistantTable) {
		draft, err := readAssistantPolicy(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.conversations.PublishAssistantPolicy(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
	if holdsTable(in.Body, delegationTable) {
		draft, err := readDelegation(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.approvals.PublishDelegation(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
	if holdsTable(in.Body, recoveryPolicyTable) {
		draft, err := readRecoveryPolicy(in.Body)
		if err != nil {
			fail(w, err)
			return
		}
		if record, err = h.recoveries.PublishRecoveryPolicy(r.Context(), h.Scope, draft, actor); err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, record)
		return
	}
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
	if r.URL.Query().Get("kind") == string(authority.PolicyDelegate) {
		return h.approvals.DelegatePolicyVersions(r.Context(), h.Scope)
	}
	if r.URL.Query().Get("kind") == string(authority.PolicyNotify) {
		return h.notices.NotifyVersions(r.Context(), h.Scope)
	}
	if r.URL.Query().Get("kind") == string(authority.PolicyAutonomy) {
		return h.autonomy.AutonomyVersions(r.Context(), h.Scope)
	}
	if r.URL.Query().Get("kind") == string(authority.PolicyDelegation) {
		return h.approvals.DelegationVersions(r.Context(), h.Scope)
	}
	if r.URL.Query().Get("kind") == string(authority.PolicyAssistant) {
		return h.conversations.AssistantVersions(r.Context(), h.Scope)
	}
	if r.URL.Query().Get("kind") == string(authority.PolicyLane) {
		return h.questions.LanePolicyVersions(r.Context(), h.Scope)
	}
	if r.URL.Query().Get("kind") == string(authority.PolicyRecovery) {
		return h.recoveries.RecoveryPolicyVersions(r.Context(), h.Scope)
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
