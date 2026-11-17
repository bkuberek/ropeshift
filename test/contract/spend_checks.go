package contract

import (
	"errors"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// SpendChecks are the spend policy a project's lanes wait under (ADR-0037): a versioned record only a member
// publishes, refused outside its ranges, and carried to a worker with every lane's lease.
var SpendChecks = []Check{
	{"SP1", "the spend policy is published like any other, only by a member, and a value outside its range is refused", checkSpendPolicyPublished},
	{"SP2", "a lane's lease carries the spend policy in force, and a new version reaches the next read", checkLeaseCarriesTheSpendPolicy},
	{"SP3", "the installation's spend policy is published only by a member, refused outside its ranges, and read by every project", checkInstallationSpendPolicy},
	{"SP4", "a lane records the built-in policy versions when nothing was published", checkLaneRecordsTheBuiltInPolicies},
	{"SP5", "a lane records the policy versions in force when it started, and a later version does not change them", checkLaneRecordsThePoliciesItStartedUnder},
}

func (r *Rig) lanePolicies(store ports.Store, lane string) (ledger.LanePolicies, error) {
	var got ledger.LanePolicies
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		got, err = tx.Lanes().Policies(lane)
		return err
	})
	return got, err
}

func checkLaneRecordsTheBuiltInPolicies(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	got, err := r.lanePolicies(s.store, s.lane)
	if err != nil {
		return err
	}
	if got != (ledger.LanePolicies{Spend: 1, InstallationSpend: 1}) {
		return Violatedf("a lane started on the built-in policies records %+v", got)
	}
	if _, err := r.lanePolicies(s.store, "no-such-lane"); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a lane nobody started reads %v", err)
	}
	return nil
}

func checkLaneRecordsThePoliciesItStartedUnder(r *Rig) error {
	spend := control.NewSpend(r.Store())
	policy := authority.DefaultSpendPolicy()
	policy.Version, policy.HardMultiplier = 2, 3
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	installation := authority.DefaultInstallationSpendPolicy()
	installation.Version, installation.OrchestratorDailyWarn = 2, 40
	if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, authority.InstallationSpendPolicyDraft{Body: installation.Body(), Policy: installation}, theMember); err != nil {
		return err
	}
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	policy.Version, policy.HardMultiplier = 3, 4
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	got, err := r.lanePolicies(s.store, s.lane)
	if err != nil {
		return err
	}
	if got != (ledger.LanePolicies{Spend: 2, InstallationSpend: 2}) {
		return Violatedf("a lane started under spend version 2 and installation version 2 records %+v after version 3 was published", got)
	}
	return nil
}

func checkInstallationSpendPolicy(r *Rig) error {
	spend := control.NewSpend(r.Store())
	versions, err := spend.InstallationSpendPolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 1 || versions[0].Kind != authority.PolicyInstallationSpend || versions[0].Body != authority.BuiltInInstallationSpendPolicyBody {
		return Violatedf("an installation starts on %+v", versions)
	}
	policy := authority.DefaultInstallationSpendPolicy()
	policy.Version, policy.OrchestratorDailyWarn = 2, 40
	draft := authority.InstallationSpendPolicyDraft{Body: policy.Body(), Policy: policy}
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "o"}, {Kind: ledger.ActorLane, ID: "l"}, {Kind: ledger.ActorWorker, ID: "w"},
		{Kind: ledger.ActorDelegate, ID: "member-1/assistant"},
	} {
		if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, draft, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s moved the installation's spend policy: %v", actor.Kind, err)
		}
	}
	if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return err
	}
	if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return Violatedf("a retry failed: %v", err)
	}
	outside := authority.DefaultInstallationSpendPolicy()
	outside.Version, outside.OrchestratorClass = 3, "huge"
	if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, authority.InstallationSpendPolicyDraft{Body: outside.Body(), Policy: outside}, theMember); !errors.Is(err, authority.ErrBadSpendPolicy) {
		return Violatedf("a class that does not exist read %v", err)
	}
	stale := authority.InstallationSpendPolicyDraft{Body: policy.Body() + "# again\n", Policy: policy}
	if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, stale, theMember); !errors.Is(err, authority.ErrBadSpendPolicy) {
		return Violatedf("a version that is not newer read %v", err)
	}
	versions, err = spend.InstallationSpendPolicyVersions(r.Ctx, r.Scope)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	in, err := spend.InstallationSpendPolicyInForce(r.Ctx, r.Scope)
	if err != nil || in.OrchestratorDailyWarn != 40 || in.Version != 2 {
		return Violatedf("the policy in force reads %+v: %v", in, err)
	}
	return nil
}

func checkSpendPolicyPublished(r *Rig) error {
	spend := control.NewSpend(r.Store())
	versions, err := spend.SpendPolicyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 1 || versions[0].Kind != authority.PolicySpend || versions[0].Body != authority.BuiltInSpendPolicyBody {
		return Violatedf("a workspace starts on %+v", versions)
	}
	policy := authority.DefaultSpendPolicy()
	policy.Version, policy.MaxLiveWait = 2, "10m"
	draft := authority.SpendPolicyDraft{Body: "[spend_policy]\nversion = 2\nmax_live_wait = \"10m\"\n", Policy: policy}
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorOrchestrator, ID: "o"}, {Kind: ledger.ActorLane, ID: "l"}, {Kind: ledger.ActorWorker, ID: "w"}} {
		if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, draft, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s moved the spend policy: %v", actor.Kind, err)
		}
	}
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return err
	}
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return Violatedf("a retry failed: %v", err)
	}
	stale := authority.SpendPolicyDraft{Body: "[spend_policy]\nversion = 2\n# again\n", Policy: policy}
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, stale, theMember); !errors.Is(err, authority.ErrBadSpendPolicy) {
		return Violatedf("a version that is not newer read %v", err)
	}
	outside := authority.DefaultSpendPolicy()
	outside.Version, outside.MaxLiveWait = 3, "30s"
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: "[spend_policy]\nversion = 3\n", Policy: outside}, theMember); !errors.Is(err, authority.ErrBadSpendPolicy) {
		return Violatedf("a live wait under a minute read %v", err)
	}
	versions, err = spend.SpendPolicyVersions(r.Ctx, r.Scope)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	return nil
}

func checkLeaseCarriesTheSpendPolicy(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	read := func() (*ledger.DesiredSpend, error) {
		state, err := s.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: s.auth})
		if err != nil {
			return nil, err
		}
		if len(state.Leases) != 1 || state.Leases[0].Lane != s.lane {
			return nil, Violatedf("desired state %+v", state.Leases)
		}
		return state.Leases[0].Spend, nil
	}
	got, err := read()
	if err != nil {
		return err
	}
	if got == nil || got.Version != 1 || got.MaxLiveWaitMs != (5*time.Minute).Milliseconds() || got.MaxToolSleepMs != 60_000 || len(got.WaitRulesOff) != 0 {
		return Violatedf("a lease on the built-in policy carries %+v", got)
	}
	policy := authority.DefaultSpendPolicy()
	policy.Version, policy.MaxLiveWait, policy.MaxToolSleep = 2, "10m", "90s"
	policy.WaitRulesOff = []string{"W4"}
	policy.WaitRulePatterns = map[string][]string{"W3": {"kubectl rollout status"}}
	draft := authority.SpendPolicyDraft{Body: "[spend_policy]\nversion = 2\n", Policy: policy}
	if _, err := control.NewSpend(s.store).PublishSpendPolicy(r.Ctx, r.Scope, draft, theMember); err != nil {
		return err
	}
	if got, err = read(); err != nil {
		return err
	}
	if got == nil || got.Version != 2 || got.MaxLiveWaitMs != (10*time.Minute).Milliseconds() || got.MaxToolSleepMs != 90_000 ||
		len(got.WaitRulesOff) != 1 || got.WaitRulesOff[0] != "W4" || len(got.WaitRulePatterns["W3"]) != 1 {
		return Violatedf("a lease on version 2 carries %+v", got)
	}
	return nil
}
