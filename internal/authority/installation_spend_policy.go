package authority

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// PolicyInstallationSpend holds the spend values that belong to the installation and no project: the class the
// orchestrator runs on and its daily warning. The orchestrator serves every project on its machine, so the
// installation keeps these, as it keeps the autonomy policy, and an admin changes them (ADR-0037).
const PolicyInstallationSpend PolicyKind = "installation_spend"

// InstallationSpendPolicy is the body of an installation spend policy version.
type InstallationSpendPolicy struct {
	Version int64 `toml:"version" json:"version"`
	// OrchestratorClass is the capability class the orchestrator runs on.
	OrchestratorClass string `toml:"orchestrator_class" json:"orchestrator_class"`
	// OrchestratorDailyWarn is the orchestrator's soft daily limit per machine, in US dollars at list price. 0
	// means no warning.
	OrchestratorDailyWarn float64 `toml:"orchestrator_daily_warn" json:"orchestrator_daily_warn"`
}

// InstallationSpendPolicyFile is the table an admin publishes.
type InstallationSpendPolicyFile struct {
	InstallationSpendPolicy InstallationSpendPolicy `toml:"installation_spend_policy"`
}

// InstallationSpendPolicyDraft is an installation spend policy as written, and what it says.
type InstallationSpendPolicyDraft struct {
	Body   string
	Policy InstallationSpendPolicy
}

// InstallationSpendPolicyKeys lists the keys in the order a surface shows them.
var InstallationSpendPolicyKeys = []string{"orchestrator_class", "orchestrator_daily_warn"}

// InstallationSpendPolicyFileKeys are the keys a body must state.
var InstallationSpendPolicyFileKeys = []string{"version", "orchestrator_class", "orchestrator_daily_warn"}

// BuiltInInstallationSpendPolicyBody is what an installation runs on until an admin publishes a version.
var BuiltInInstallationSpendPolicyBody = DefaultInstallationSpendPolicy().Body()

// DefaultInstallationSpendPolicy is the version the design states.
func DefaultInstallationSpendPolicy() InstallationSpendPolicy {
	return InstallationSpendPolicy{Version: 1, OrchestratorClass: defaultClassName}
}

// Check accepts a policy an installation can run on, and refuses a value outside its key's range, naming the key.
func (p InstallationSpendPolicy) Check() error {
	if p.Version < 1 {
		return spendRefusal("version %d", p.Version)
	}
	if err := checkSpendChoice("orchestrator_class", p.OrchestratorClass, SessionClasses); err != nil {
		return err
	}
	return checkSpendFloat("orchestrator_daily_warn", p.OrchestratorDailyWarn, 0, maxAmount)
}

// Set returns the policy with one key changed to a value, given as text, and the other as it was. The result must
// check. A key nobody offers, a project's included, is refused. The version is the caller's to move.
func (p InstallationSpendPolicy) Set(key, value string) (InstallationSpendPolicy, error) {
	out := p
	value = strings.TrimSpace(value)
	switch key {
	case "orchestrator_class":
		out.OrchestratorClass = value
	case "orchestrator_daily_warn":
		n, err := parseSpendFloat(key, value)
		if err != nil {
			return p, err
		}
		out.OrchestratorDailyWarn = n
	default:
		return p, spendRefusal("%s is no installation spend key", key)
	}
	if err := out.Check(); err != nil {
		return p, err
	}
	return out, nil
}

// Text is the value of a key as Set reads it, and empty for a key nobody offers.
func (p InstallationSpendPolicy) Text(key string) string {
	switch key {
	case "orchestrator_class":
		return p.OrchestratorClass
	case "orchestrator_daily_warn":
		return strconv.FormatFloat(p.OrchestratorDailyWarn, 'g', -1, 64)
	}
	return ""
}

// ChangesTo names the keys a later version changed, with each value before and after, in the order the keys are listed.
func (p InstallationSpendPolicy) ChangesTo(next InstallationSpendPolicy) []ledger.SpendKeyChange {
	return keyChanges(InstallationSpendPolicyKeys, p.Text, next.Text)
}

// Body is the policy as an admin would write it, stating every key.
func (p InstallationSpendPolicy) Body() string {
	return fmt.Sprintf("[installation_spend_policy]\nversion = %d\norchestrator_class = %q\norchestrator_daily_warn = %s\n",
		p.Version, p.OrchestratorClass, tomlFloat(p.OrchestratorDailyWarn))
}

// NewInstallationSpendPolicyVersion stores a draft under a generated ID.
func NewInstallationSpendPolicyVersion(id, authoredBy string, draft InstallationSpendPolicyDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyInstallationSpend, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadInstallationSpendPolicy reads a version's settings, once its body is what its digest covers, and refuses a
// version whose settings fail Check, as ReadSpendPolicy does.
func ReadInstallationSpendPolicy(version PolicyVersion) (InstallationSpendPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return InstallationSpendPolicy{}, err
	}
	policy := DefaultInstallationSpendPolicy()
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return InstallationSpendPolicy{}, err
	}
	if err := policy.Check(); err != nil {
		return InstallationSpendPolicy{}, fmt.Errorf("installation spend policy version %d is refused: %w", version.Version, err)
	}
	return policy, nil
}
