package authority_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

var delegate = authority.DelegateActor("m1", "dlg_01")

func TestEveryOperationHasOneClassAndALabel(t *testing.T) {
	seen := map[string]bool{}
	for _, op := range authority.Operations() {
		if seen[op.ID] {
			t.Errorf("%s appears twice", op.ID)
		}
		seen[op.ID] = true
		if op.Label == "" || strings.HasSuffix(op.Label, ".") {
			t.Errorf("%s has label %q, want a short imperative phrase", op.ID, op.Label)
		}
		switch op.Class {
		case authority.ClassOperational, authority.ClassMemberOnly:
			if op.Exemptable || op.NeverReason != "" {
				t.Errorf("%s is %s and still says whether policy exempts it", op.ID, op.Class)
			}
		case authority.ClassConsent:
			if op.Exemptable == (op.NeverReason != "") {
				t.Errorf("%s: a consent operation is exemptable or names why it never is: %+v", op.ID, op)
			}
		default:
			t.Errorf("%s has class %q", op.ID, op.Class)
		}
		if found, ok := authority.OperationOf(op.ID); !ok || found.ID != op.ID {
			t.Errorf("OperationOf(%s) = %+v, %v", op.ID, found, ok)
		}
	}
	if _, ok := authority.OperationOf("work_order.teleport"); ok {
		t.Error("an unknown operation was found")
	}
}

// TestADelegateMayNotMergeAQuestion holds question.merge to a member: it moves one question's lanes onto another in an
// orchestrator's place, so a delegate's call is refused, not carded, and a delegate holds none of what it needs.
func TestADelegateMayNotMergeAQuestion(t *testing.T) {
	op, ok := authority.OperationOf(authority.OpQuestionMerge)
	if !ok || op.Class != authority.ClassMemberOnly {
		t.Fatalf("question.merge reads %+v, want it member-only", op)
	}
	for _, action := range op.Actions {
		if authority.Allowed(delegate, action) {
			t.Errorf("a delegate holds %s, which question.merge needs", action)
		}
	}
}

// TestADelegateMayNotDecideAQuestionNow holds question.decide_now to a member: it takes a question off the ladder into
// the member's own Needs you, so the audit names who chose to decide it now, and a delegate's call is refused.
func TestADelegateMayNotDecideAQuestionNow(t *testing.T) {
	op, ok := authority.OperationOf(authority.OpQuestionDecideNow)
	if !ok || op.ID != "question.decide_now" || op.Class != authority.ClassMemberOnly {
		t.Fatalf("question.decide_now reads %+v, want it member-only", op)
	}
	for _, action := range op.Actions {
		if authority.Allowed(delegate, action) {
			t.Errorf("a delegate holds %s, which question.decide_now needs", action)
		}
	}
}

// TestOnlyAMemberKeepsADecision holds decision.keep to a member: keeping records a decision as the member's own, which is
// a person's word that a one-way choice an orchestrator made stands. A delegate's call is refused, not carded, and
// neither an orchestrator nor a delegate holds the action it needs, while a member does.
func TestOnlyAMemberKeepsADecision(t *testing.T) {
	op, ok := authority.OperationOf(authority.OpDecisionKeep)
	if !ok || op.ID != "decision.keep" || op.Class != authority.ClassMemberOnly {
		t.Fatalf("decision.keep reads %+v, want it member-only", op)
	}
	if len(op.Actions) == 0 {
		t.Fatal("decision.keep names no action its use case checks")
	}
	for _, action := range op.Actions {
		if authority.Allowed(delegate, action) || authority.Allowed(ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "w1"}, action) {
			t.Errorf("a delegate or an orchestrator holds %s, which decision.keep needs", action)
		}
		if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m1"}, action) {
			t.Errorf("a member lacks %s, which decision.keep needs", action)
		}
	}
}

// TestOnlyAMemberRetriesOrSkipsARepositoryCreation holds the two operations on a failed repository creation to the
// member-only class: a delegate's call is refused, not carded, and neither a delegate nor a lane holds what either needs,
// while a member holds all of it.
func TestOnlyAMemberRetriesOrSkipsARepositoryCreation(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "m1"}
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "l1"}
	for id, want := range map[string][]authority.Action{
		authority.OpPlanRepositoryRetry: {authority.ActionApprovePlan, authority.ActionRegisterManifest, authority.ActionCreateRemotes},
		authority.OpPlanRepositorySkip:  {authority.ActionApprovePlan},
	} {
		op, ok := authority.OperationOf(id)
		if !ok || op.Class != authority.ClassMemberOnly || op.Installation || op.DelegateMayRun() || !slices.Equal(op.Actions, want) {
			t.Errorf("%s reads %+v, want a member-only project operation needing %v", id, op, want)
			continue
		}
		for _, action := range op.Actions {
			if !authority.Allowed(member, action) {
				t.Errorf("a member lacks %s, which %s needs", action, id)
			}
			for _, who := range []ledger.Actor{delegate, lane} {
				if authority.Allowed(who, action) {
					t.Errorf("%s holds %s, which %s needs", who.Kind, action, id)
				}
			}
		}
	}
	if ids := []string{authority.OpPlanRepositoryRetry, authority.OpPlanRepositorySkip}; ids[0] != "plan.repository_retry" || ids[1] != "plan.repository_skip" {
		t.Errorf("the operations are named %v", ids)
	}
}

// TestADelegateHoldsOnlyWhatItsOperationsNeed holds Allowed for a delegate to the class table: a delegate
// may do what an operational or exemptable consent operation of a project needs, and nothing else.
func TestADelegateHoldsOnlyWhatItsOperationsNeed(t *testing.T) {
	needed := map[authority.Action]bool{}
	for _, op := range authority.Operations() {
		if op.Installation || op.Class == authority.ClassMemberOnly || (op.Class == authority.ClassConsent && !op.Exemptable) {
			continue
		}
		for _, action := range op.Actions {
			needed[action] = true
			if !authority.Allowed(delegate, action) {
				t.Errorf("%s needs %s, which a delegate lacks", op.ID, action)
			}
		}
	}
	for _, action := range authority.Actions() {
		if authority.Allowed(delegate, action) && !needed[action] {
			t.Errorf("a delegate may %s, which no operation it may run needs", action)
		}
	}
}

// gateOnly are the never-exempt operations whose use case checks only actions an operation a delegate runs
// needs too, so the class gate alone keeps them to a card, and why.
var gateOnly = map[string]string{
	authority.OpWorkOrderRetryExtra:                 "a retry needs one action, and the use case refuses a delegate an extra attempt",
	authority.OpWorkOrderReworkWithoutFailedVerdict: "a rework needs one action, and the use case refuses a delegate a rework no failed verdict stands behind",
	authority.OpWorkOrderResolve:                    "resolving blocked work needs what replanning it needs",
	authority.OpWorkOrderWrite:                      "writing under a claim checks no action",
	authority.OpWorkOrderRelease:                    "releasing a claim checks no action",
	authority.OpFleetRun:                            "running the fleet needs what pausing it needs",
	authority.OpFleetCaps:                           "setting the fleet's caps needs what pausing it needs",
	authority.OpQuestionRecord:                      "recording a question checks no action",
	authority.OpRepositoryForge:                     "a forge binding is a repository setting",
	authority.OpPaneInput:                           "typing into a pane checks no action",
	authority.OpMessageSend:                         "an outside delegate's message to a lane needs what the assistant's needs",
}

// TestNoGateDefectRunsAMemberOnlyOrNeverExemptOperation holds the second line: every operation a delegate
// may never run without a member needs an action no delegate holds, so a defect in the class gate still
// leaves it to a member, except the few whose use case shares its actions with an operation a delegate
// runs. Each of those shares every action, or it would need one a delegate lacks.
func TestNoGateDefectRunsAMemberOnlyOrNeverExemptOperation(t *testing.T) {
	for _, op := range authority.Operations() {
		if op.Class == authority.ClassOperational || op.Exemptable {
			continue
		}
		lacks := slices.ContainsFunc(op.Actions, func(a authority.Action) bool { return !authority.Allowed(delegate, a) })
		if _, shared := gateOnly[op.ID]; shared == lacks {
			t.Errorf("%s (%s) needs %v, and a delegate lacks one of them: %v; listed as resting on the gate alone: %v", op.ID, op.Class, op.Actions, lacks, shared)
		}
	}
	for id := range gateOnly {
		if op, ok := authority.OperationOf(id); !ok || op.Class != authority.ClassConsent || op.Exemptable {
			t.Errorf("%s rests on the gate alone, and is %+v", id, op)
		}
	}
	for _, action := range []authority.Action{
		authority.ActionConfirmCard, authority.ActionChangeRoster, authority.ActionPublishPolicy, authority.ActionRaiseMergeAuthority,
		authority.ActionReviewPullRequest, authority.ActionManageSessions, authority.ActionSetAutonomy, authority.ActionCreateJoinToken,
		authority.ActionManageProject, authority.ActionDecideOneWay, authority.ActionPassAutonomy, authority.ActionForceArchive,
		authority.ActionApprovePlan, authority.ActionDecideMerge, authority.ActionDecideRecovery, authority.ActionBackupInstallation,
		authority.ActionPublishInstallationPolicy, authority.ActionRaiseSpendLimit,
	} {
		if authority.Allowed(delegate, action) {
			t.Errorf("a delegate may %s", action)
		}
		if !slices.Contains(authority.Actions(), action) {
			t.Errorf("Actions() does not list %s, so the member test never holds it", action)
		}
	}
}

// TestCreatingWorkIsConsentForAnOutsideDelegateAndOperationalForTheAssistant holds the class of every call
// that creates a work order: an orchestrator reads a work order's title, so an outside delegate's call is a
// card a member confirms, unless the delegate policy exempts it, as its fixed list allows. The assistant acts
// in a member's own conversation, and its call runs at once.
func TestCreatingWorkIsConsentForAnOutsideDelegateAndOperationalForTheAssistant(t *testing.T) {
	for _, c := range []struct {
		outside, assistant string
	}{
		{authority.OpWorkOrderCreate, authority.OpWorkOrderCreateByAssistant},
		{authority.OpPlanStartReview, authority.OpPlanStartReviewByAssistant},
	} {
		outside, ok := authority.OperationOf(c.outside)
		if !ok || outside.Class != authority.ClassConsent || !outside.Exemptable || outside.Installation {
			t.Errorf("an outside delegate's %s is %+v, want exemptable consent", c.outside, outside)
		}
		assistant, ok := authority.OperationOf(c.assistant)
		if !ok || assistant.Class != authority.ClassOperational || assistant.Installation {
			t.Errorf("the assistant's %s is %+v, want operational", c.assistant, assistant)
		}
		if assistant.Label != outside.Label || !slices.Equal(assistant.Actions, outside.Actions) {
			t.Errorf("%s and %s run different things: %+v and %+v", c.outside, c.assistant, outside, assistant)
		}
		if err := (authority.DelegatePolicy{Version: 2, Exempt: []string{c.outside}}).Check(); err != nil {
			t.Errorf("a policy cannot exempt %s: %v", c.outside, err)
		}
		if err := (authority.DelegatePolicy{Version: 2, Exempt: []string{c.assistant}}).Check(); !errors.Is(err, authority.ErrInvalid) {
			t.Errorf("a policy exempts the operational %s: %v", c.assistant, err)
		}
	}
}

// TestADelegatePolicyMayExemptOnlyTheReversibleList holds the delegate policy to its fixed list of reversible
// acts (ADR-0024): accepting work whose every criterion passed with every required check green, sending work
// to rework after a failed verdict, creating work, and starting a contract review. Every other consent
// operation asks, and a version that lists one is refused, naming it.
func TestADelegatePolicyMayExemptOnlyTheReversibleList(t *testing.T) {
	want := []string{authority.OpPlanStartReview, authority.OpWorkOrderAccept, authority.OpWorkOrderCreate, authority.OpWorkOrderRework}
	var got []string
	for _, op := range authority.Operations() {
		if op.Exemptable {
			got = append(got, op.ID)
		}
		if op.Class != authority.ClassConsent || op.Exemptable {
			continue
		}
		err := (authority.DelegatePolicy{Version: 2, Exempt: []string{op.ID}}).Check()
		if !errors.Is(err, authority.ErrInvalid) || !strings.Contains(err.Error(), op.ID) || op.NeverReason == "" {
			t.Errorf("a policy that lists %s: %v", op.ID, err)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the policy may exempt %v, want %v", got, want)
	}
	// An accept or a rework short of its condition is an operation of its own, which stays a card.
	accept, _ := authority.OperationOf(authority.OpWorkOrderAccept)
	if !slices.Equal(accept.Actions, []authority.Action{authority.ActionAccept}) {
		t.Errorf("an exempt accept needs %v, want only what verified work needs", accept.Actions)
	}
	for _, id := range []string{authority.OpWorkOrderAcceptUnverified, authority.OpWorkOrderReworkWithoutFailedVerdict} {
		if op, ok := authority.OperationOf(id); !ok || op.Class != authority.ClassConsent || op.Exemptable {
			t.Errorf("%s is %+v, want consent no policy exempts", id, op)
		}
	}
}

// TestPassingAReviewIsAMembersWord holds every review that makes work claimable to a member (ADR-0024): a
// delegate's call to pass one, planning work's included and the assistant's included, is a card no policy
// exempts, and no delegate holds what passing one needs, so its acceptance of a contract review passes none
// of the work it read. Starting a contract review is a separate act a delegate may run.
func TestPassingAReviewIsAMembersWord(t *testing.T) {
	for _, id := range []string{authority.OpWorkOrderReview, authority.OpWorkOrderReviewPlan} {
		if op, ok := authority.OperationOf(id); !ok || op.Class != authority.ClassConsent || op.Exemptable {
			t.Errorf("%s is %+v, want consent no policy exempts", id, op)
		}
	}
	if authority.Allowed(delegate, authority.ActionPassReview) {
		t.Error("a delegate holds what passing a review needs")
	}
	if !authority.Allowed(delegate, authority.ActionStartReview) || !slices.Contains(authority.Actions(), authority.ActionStartReview) {
		t.Error("a delegate lacks what starting a contract review needs")
	}
}

// TestADelegatesDecisionIsAMembersWord holds every decision a delegate records to a member's consent
// (ADR-0024): one within the decision policy is a card no policy exempts, for an outside delegate and the
// assistant alike, and no delegate holds what recording a decision needs. An orchestrator still decides within
// policy, through its own acts, which no class reads.
func TestADelegatesDecisionIsAMembersWord(t *testing.T) {
	for _, id := range []string{authority.OpDecisionAssume, authority.OpDecisionRecord} {
		if op, ok := authority.OperationOf(id); !ok || op.Class != authority.ClassConsent || op.Exemptable {
			t.Errorf("%s is %+v, want consent no policy exempts", id, op)
		}
	}
	if authority.Allowed(delegate, authority.ActionRecordDecision) {
		t.Error("a delegate holds what recording a decision needs")
	}
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "o1"}, authority.ActionRecordDecision) {
		t.Error("an orchestrator lacks what deciding within policy needs")
	}
}

func TestStrictestClassWins(t *testing.T) {
	ops := func(ids ...string) []authority.Operation {
		var out []authority.Operation
		for _, id := range ids {
			op, ok := authority.OperationOf(id)
			if !ok {
				t.Fatalf("no operation %s", id)
			}
			out = append(out, op)
		}
		return out
	}
	cases := []struct {
		ids  []string
		want authority.OperationClass
	}{
		{[]string{"repository.set"}, authority.ClassOperational},
		{[]string{"repository.set", "repository.forge"}, authority.ClassConsent},
		{[]string{"repository.forge", "repository.raise_merge_authority", "repository.set"}, authority.ClassConsent},
		{[]string{"approval.decide", "card.confirm"}, authority.ClassMemberOnly},
	}
	for _, c := range cases {
		if got := authority.Strictest(ops(c.ids...)); got != c.want {
			t.Errorf("%v reads as %s, want %s", c.ids, got, c.want)
		}
	}
}

func TestADelegateActsForItsMember(t *testing.T) {
	if delegate.Kind != ledger.ActorDelegate || delegate.ID != "m1/dlg_01" {
		t.Fatalf("actor %+v", delegate)
	}
	member, credential, ok := authority.ActingFor(delegate)
	if !ok || member != "m1" || credential != "dlg_01" {
		t.Fatalf("acting for %q %q %v", member, credential, ok)
	}
	if _, _, ok := authority.ActingFor(ledger.Actor{Kind: ledger.ActorMember, ID: "m1/dlg_01"}); ok {
		t.Error("a member reads as acting for someone")
	}
	if authority.PartyOf(delegate) != authority.PartyDelegate || !authority.PartyOf(delegate).Agent() {
		t.Errorf("a delegate is party %q", authority.PartyOf(delegate))
	}
	if authority.Principal(delegate) != "delegate:m1/dlg_01" {
		t.Errorf("principal %q", authority.Principal(delegate))
	}
}

func TestADirectMemberIsAMemberAndNeverADelegateActingForOne(t *testing.T) {
	for _, tc := range []struct {
		name   string
		actor  ledger.Actor
		member string
		ok     bool
	}{
		{"a member", ledger.Actor{Kind: ledger.ActorMember, ID: "m1"}, "m1", true},
		{"a delegate acting for a member", delegate, "", false},
		{"a member with no ID", ledger.Actor{Kind: ledger.ActorMember}, "", false},
		{"a worker", ledger.Actor{Kind: ledger.ActorWorker, ID: "m1"}, "", false},
		{"no actor", ledger.Actor{}, "", false},
	} {
		member, ok := authority.DirectMember(tc.actor)
		if ok != tc.ok || (ok && member != tc.member) {
			t.Errorf("%s reads as member %q, %v; want %q, %v", tc.name, member, ok, tc.member, tc.ok)
		}
	}
	if member, ok := authority.MemberOf(delegate); !ok || member != "m1" {
		t.Errorf("MemberOf a delegate is %q, %v; the two readings differ on a delegate", member, ok)
	}
}

func TestADelegateCredentialHasThreeStates(t *testing.T) {
	credential := authority.DelegateCredential{ID: "dlg_01", Member: "m1", CreatedAt: 100, ExpiresAt: 200}
	if state := credential.State(150); state != authority.DelegateActive {
		t.Errorf("before expiry %s", state)
	}
	if state := credential.State(200); state != authority.DelegateExpired {
		t.Errorf("at expiry %s", state)
	}
	credential.RevokedAt = 120
	if state := credential.State(150); state != authority.DelegateRevoked {
		t.Errorf("revoked %s", state)
	}
	if state := credential.State(250); state != authority.DelegateRevoked {
		t.Errorf("revoked and past expiry %s", state)
	}
}

func TestANewDelegateIsCheckedBeforeItIsKept(t *testing.T) {
	ok := authority.DelegateRequest{Member: "m1", Label: "planning assistant", ExpiresInMs: authority.MinDelegateTTLMs}
	if err := ok.Check(); err != nil {
		t.Fatalf("a good request: %v", err)
	}
	bad := map[string]authority.DelegateRequest{
		"a member ID with a slash": {Member: "m/1", Label: "x", ExpiresInMs: authority.MinDelegateTTLMs},
		"no label":                 {Member: "m1", ExpiresInMs: authority.MinDelegateTTLMs},
		"a long label":             {Member: "m1", Label: strings.Repeat("é", 65), ExpiresInMs: authority.MinDelegateTTLMs},
		"under a minute":           {Member: "m1", Label: "x", ExpiresInMs: authority.MinDelegateTTLMs - 1},
		"over thirty days":         {Member: "m1", Label: "x", ExpiresInMs: authority.MaxDelegateTTLMs + 1},
		"another kind":             {Member: "m1", Label: "x", ExpiresInMs: authority.MinDelegateTTLMs, Kind: "assistant"},
	}
	for name, request := range bad {
		if err := request.Check(); !errors.Is(err, authority.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	long := authority.DelegateRequest{Member: "m1", Label: strings.Repeat("é", 64), ExpiresInMs: authority.MaxDelegateTTLMs}
	if err := long.Check(); err != nil {
		t.Errorf("64 characters and thirty days: %v", err)
	}
}

func TestADelegatePolicyExemptsOnlyExemptableConsent(t *testing.T) {
	policy := authority.DelegatePolicy{Version: 2, Exempt: []string{"work_order.accept", "work_order.rework"}}
	if err := policy.Check(); err != nil {
		t.Fatalf("a good policy: %v", err)
	}
	refused := authority.DelegatePolicy{Version: 2, Exempt: []string{
		"work_order.teleport", "work_order.claim", "delegate.create", "roster.change", "work_order.cancel", "work_order.accept",
	}}
	err := refused.Check()
	if !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	for _, id := range []string{"work_order.teleport", "work_order.claim", "delegate.create", "roster.change", "work_order.cancel"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("the refusal does not name %s: %v", id, err)
		}
	}
	if strings.Contains(err.Error(), "work_order.accept") {
		t.Errorf("the refusal names an ID it accepts: %v", err)
	}
	if err := (authority.DelegatePolicy{Version: 0}).Check(); !errors.Is(err, authority.ErrInvalid) {
		t.Errorf("version 0: %v", err)
	}
}

// TestADelegatePolicyLetsADelegateRaiseTheModelAndEffortUnlessItSaysNot reads raise_model_and_effort as a
// version states it, and as true when a version leaves it out, the built-in one and every one published
// before the setting included (FR-AUTH-16).
func TestADelegatePolicyLetsADelegateRaiseTheModelAndEffortUnlessItSaysNot(t *testing.T) {
	if !authority.BuiltInDelegatePolicy().RaisesModelAndEffort() {
		t.Error("the built-in delegate policy keeps a delegate from raising the model and effort")
	}
	no, yes := false, true
	for _, c := range []struct {
		name   string
		policy authority.DelegatePolicy
		raises bool
	}{
		{"a version that leaves it out", authority.DelegatePolicy{Version: 2}, true},
		{"a version that allows it", authority.DelegatePolicy{Version: 2, RaiseModelAndEffort: &yes}, true},
		{"a version that refuses it", authority.DelegatePolicy{Version: 2, RaiseModelAndEffort: &no}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			version, err := authority.NewDelegatePolicyVersion("p1", "m1", authority.DelegatePolicyDraft{Body: "[delegate_policy]\n", Policy: c.policy}, 1)
			if err != nil {
				t.Fatal(err)
			}
			read, err := authority.ReadDelegatePolicy(version)
			if err != nil {
				t.Fatal(err)
			}
			if read.RaisesModelAndEffort() != c.raises {
				t.Errorf("the version %s reads %v", version.Settings, read.RaisesModelAndEffort())
			}
		})
	}
	// A version that leaves it out keeps the settings a version kept before the setting existed.
	version, err := authority.NewDelegatePolicyVersion("p1", "m1", authority.DelegatePolicyDraft{Body: "[delegate_policy]\n", Policy: authority.DelegatePolicy{Version: 2}}, 1)
	if err != nil || version.Settings != `{"version":2,"exempt":[]}` {
		t.Errorf("settings %s: %v", version.Settings, err)
	}
}

func TestADelegatePolicyNeverExemptsInAOneWayCategory(t *testing.T) {
	oneWay := authority.Operation{ID: "example.one_way", Label: "Decide", Class: authority.ClassConsent, Exemptable: true, OneWay: true}
	policy := authority.DelegatePolicy{Version: 2, Exempt: []string{oneWay.ID, "work_order.accept"}}
	accept, _ := authority.OperationOf("work_order.accept")
	rework, _ := authority.OperationOf("work_order.rework")
	operational, _ := authority.OperationOf("work_order.claim")
	if !policy.Exempts([]authority.Operation{oneWay}, false) {
		t.Error("an exempt operation outside a one-way category is not exempt")
	}
	if policy.Exempts([]authority.Operation{oneWay}, true) {
		t.Error("an operation in a one-way category is exempt")
	}
	if policy.Exempts([]authority.Operation{accept, rework}, false) {
		t.Error("a call with one consent operation the policy does not list is exempt")
	}
	if !policy.Exempts([]authority.Operation{accept, operational}, false) {
		t.Error("an exempt consent operation beside an operational one is not exempt")
	}
	// A version published while the list was longer still exempts nothing outside it.
	for _, id := range []string{"work_order.cancel", "question.answer", "question.record", "work_order.retry_extra"} {
		op, _ := authority.OperationOf(id)
		if (authority.DelegatePolicy{Version: 2, Exempt: []string{id}}).Exempts([]authority.Operation{op}, false) {
			t.Errorf("a stored version that lists %s exempts it", id)
		}
	}
	if builtIn := authority.BuiltInDelegatePolicy(); builtIn.Version != 1 || len(builtIn.Exempt) != 0 || builtIn.Check() != nil {
		t.Errorf("the built-in policy reads %+v", builtIn)
	}
	if !strings.Contains(authority.BuiltInDelegatePolicyBody, "exempt = []") {
		t.Errorf("the built-in body %q exempts something", authority.BuiltInDelegatePolicyBody)
	}
}

func TestOnlyAMemberDecidesACard(t *testing.T) {
	card := authority.Approval{
		ID: "c1", Kind: authority.KindConfirm, Subject: "repository:r1", SubjectDigest: "sha256:aa", Summary: "Raise merge authority: web",
		RequestedBy: authority.Principal(delegate), State: authority.ApprovalPending, Detail: `{"operation":"repository.raise_merge_authority"}`,
	}
	if err := card.Validate(); err != nil {
		t.Fatalf("a card with no lane: %v", err)
	}
	if !card.MembersOnly() {
		t.Error("a card is not a member's alone")
	}
	other := authority.DelegateActor("m1", "dlg_02")
	for _, actor := range []ledger.Actor{other, delegate, {Kind: ledger.ActorOrchestrator, ID: "w1"}} {
		if _, err := card.Decide(authority.Decision{ID: "c1", Digest: "sha256:aa", Allow: true}, actor, 1, 10); !errors.Is(err, authority.ErrDeciderKind) {
			t.Errorf("%s decided a card: %v", actor.Kind, err)
		}
	}
	decided, err := card.Decide(authority.Decision{ID: "c1", Digest: "sha256:aa", Allow: true}, ledger.Actor{Kind: ledger.ActorMember, ID: "m1"}, 1, 10)
	if err != nil || decided.State != authority.ApprovalApproved {
		t.Fatalf("a member's confirmation: %+v %v", decided, err)
	}
	withLane := card
	withLane.Lane = "l1"
	if err := withLane.Validate(); !errors.Is(err, authority.ErrBadApproval) {
		t.Errorf("a card on a lane: %v", err)
	}
	noDetail := card
	noDetail.Detail = ""
	if err := noDetail.Validate(); !errors.Is(err, authority.ErrBadApproval) {
		t.Errorf("a card with no operation: %v", err)
	}
}

func TestADelegateDecidesOnlyWhatTheDelegationPolicyCovers(t *testing.T) {
	call, err := authority.ReadToolCall("Bash", []byte(`{"command":"go test ./..."}`))
	if err != nil {
		t.Fatal(err)
	}
	request := authority.Approval{
		ID: "a1", Lane: "l1", WorkOrder: "w1", Kind: authority.KindToolCall, Subject: "Bash", SubjectDigest: call.Digest,
		Input: call.Input, Command: call.Command, RequestedBy: "worker:w1", State: authority.ApprovalPending,
	}
	covering, err := authority.NewDelegationVersion("p2", "m1", authority.DelegationDraft{
		Body: "covering", Policy: authority.DelegationPolicy{Version: 2, Classes: []authority.DelegationClass{{Tool: "Bash", CommandPrefix: "go test"}}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := authority.NewDelegationVersion("p1", "m1", authority.DelegationDraft{Body: "empty", Policy: authority.DelegationPolicy{Version: 1}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.CheckDelegated(request, covering, delegate); err != nil {
		t.Errorf("a covered request: %v", err)
	}
	if err := authority.CheckDelegated(request, empty, delegate); !errors.Is(err, authority.ErrNotDelegated) {
		t.Errorf("an uncovered request: %v", err)
	}
	decided, err := request.Decide(authority.Decision{ID: "a1", Digest: call.Digest, Allow: true}, delegate, 2, 10)
	if err != nil || decided.DecidedKind != string(ledger.ActorDelegate) || decided.DecidedBy != "delegate:m1/dlg_01" {
		t.Errorf("a delegate's decision %+v %v", decided, err)
	}
}

func TestACardsBindingMovesWithItsSubject(t *testing.T) {
	if authority.RecordDigest("repository", "r1", 4) == authority.RecordDigest("repository", "r1", 5) {
		t.Error("a newer event about the record leaves the binding where it was")
	}
	if authority.RecordDigest("repository", "r1", 4) == authority.RecordDigest("work_order", "r1", 4) {
		t.Error("two kinds of record bind alike")
	}
	request := authority.Approval{ID: "a1", SubjectDigest: "sha256:aa", State: authority.ApprovalPending}
	decided := request
	decided.State = authority.ApprovalDenied
	if authority.RequestDigest(request) == authority.RequestDigest(decided) {
		t.Error("a decided request binds as an open one")
	}
	if authority.PolicyBinding(authority.PolicyDecision, "p1") == authority.PolicyBinding(authority.PolicyDecision, "p2") {
		t.Error("a newer version in force leaves the binding where it was")
	}
	if authority.ArgumentsDigest(`{"a":1}`) == authority.ArgumentsDigest(`{"a":2}`) {
		t.Error("other arguments bind alike")
	}
	if _, err := authority.CardDetailOf(authority.Approval{ID: "a1", Kind: authority.KindToolCall}); !errors.Is(err, authority.ErrBadApproval) {
		t.Errorf("a tool call read as a card: %v", err)
	}
}

// TestADelegatePastTheGateStillDecidesNoRequestOutsideItsReach holds what the use cases enforce for
// approval.decide, whose tool-call form needs only an action a delegate holds: a request of a kind only a
// member decides refuses a delegate by its kind, and any other request refuses one the delegation policy
// does not cover, so a defect in the class gate still decides nothing.
func TestADelegatePastTheGateStillDecidesNoRequestOutsideItsReach(t *testing.T) {
	if !authority.Allowed(delegate, authority.ActionDecideApproval) {
		t.Fatal("a delegate lacks the action a covered request needs, so nothing below proves a guard")
	}
	empty, err := authority.NewDelegationVersion("p1", "m1", authority.DelegationDraft{Body: "empty", Policy: authority.DelegationPolicy{Version: 1}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	call, err := authority.ReadToolCall("Bash", []byte(`{"command":"git tag v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{authority.KindToolCall, authority.KindArtifact, authority.KindContract} {
		request := authority.Approval{
			ID: "a1", Lane: "l1", WorkOrder: "w1", Kind: kind, Subject: "Bash", SubjectDigest: call.Digest, Input: call.Input,
			Command: call.Command, RequestedBy: "worker:w1", State: authority.ApprovalPending,
		}
		if err := authority.CheckDelegated(request, empty, delegate); !errors.Is(err, authority.ErrNotDelegated) {
			t.Errorf("a delegate decides a %s request no class covers: %v", kind, err)
		}
	}
	for _, kind := range []string{authority.KindPlan, authority.KindRecovery, authority.KindMerge, authority.KindConfirm} {
		request := authority.Approval{ID: "a2", Kind: kind, Subject: "s", SubjectDigest: "sha256:aa", RequestedBy: "system:x", State: authority.ApprovalPending}
		if _, err := request.Decide(authority.Decision{ID: "a2", Digest: "sha256:aa", Allow: true}, delegate, 1, 10); !errors.Is(err, authority.ErrDeciderKind) {
			t.Errorf("a delegate decides a %s request: %v", kind, err)
		}
	}
}

// Running the practices check changes no state a decision relies on, so it is an operational operation: a delegate's
// call to it runs at once, and no action stands in front of it.
func TestRunningThePracticesCheckIsOperational(t *testing.T) {
	op, ok := authority.OperationOf(authority.OpPracticesCheck)
	if !ok {
		t.Fatal("the class table lacks practices.check")
	}
	if op.ID != "practices.check" || op.Class != authority.ClassOperational || op.Exemptable || len(op.Actions) != 0 {
		t.Errorf("practices.check reads %+v, want operational with no action", op)
	}
}

// An acknowledgement changes what members see as open, so acknowledging a finding and withdrawing the acknowledgement
// are consent operations that no delegate policy exempts, each behind its own action: a delegate's call is a card a
// member confirms, and no lane, orchestrator, worker, or tick holds either action.
func TestAcknowledgingAPracticesFindingIsConsentThatNoPolicyExempts(t *testing.T) {
	for _, tc := range []struct {
		id, label string
		action    authority.Action
	}{
		{authority.OpPracticesAcknowledge, "Acknowledge a practices finding", authority.ActionAcknowledgePractices},
		{authority.OpPracticesWithdraw, "Withdraw a finding's acknowledgement", authority.ActionWithdrawPractices},
	} {
		op, ok := authority.OperationOf(tc.id)
		if !ok {
			t.Fatalf("the class table lacks %s", tc.id)
		}
		if op.Label != tc.label || op.Class != authority.ClassConsent || op.Exemptable || op.NeverReason == "" || !slices.Equal(op.Actions, []authority.Action{tc.action}) {
			t.Errorf("%s reads %+v, want consent that no policy exempts, behind %s", tc.id, op, tc.action)
		}
		if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, tc.action) {
			t.Errorf("a member may not %s", tc.action)
		}
		for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
			if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, tc.action) {
				t.Errorf("a %s may %s", kind, tc.action)
			}
		}
	}
}
