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

// TestNoGateDefectRunsAMemberOnlyOrNeverExemptOperation holds the second line: every operation a delegate
// may never run without a member needs an action no delegate holds, so a defect in the class gate still
// leaves it to a member.
func TestNoGateDefectRunsAMemberOnlyOrNeverExemptOperation(t *testing.T) {
	for _, op := range authority.Operations() {
		if op.Class == authority.ClassOperational || op.Exemptable {
			continue
		}
		if !slices.ContainsFunc(op.Actions, func(a authority.Action) bool { return !authority.Allowed(delegate, a) }) {
			t.Errorf("%s (%s) needs no action a delegate lacks: %v", op.ID, op.Class, op.Actions)
		}
	}
	for _, action := range []authority.Action{
		authority.ActionConfirmCard, authority.ActionChangeRoster, authority.ActionPublishPolicy, authority.ActionRaiseMergeAuthority,
		authority.ActionReviewPullRequest, authority.ActionManageSessions, authority.ActionSetAutonomy, authority.ActionCreateJoinToken,
		authority.ActionManageProject, authority.ActionDecideOneWay, authority.ActionPassAutonomy, authority.ActionForceArchive,
		authority.ActionApprovePlan, authority.ActionDecideMerge, authority.ActionDecideRecovery, authority.ActionBackupInstallation,
	} {
		if authority.Allowed(delegate, action) {
			t.Errorf("a delegate may %s", action)
		}
		if !slices.Contains(authority.Actions(), action) {
			t.Errorf("Actions() does not list %s, so the member test never holds it", action)
		}
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
	policy := authority.DelegatePolicy{Version: 2, Exempt: []string{"work_order.accept", "work_order.cancel"}}
	if err := policy.Check(); err != nil {
		t.Fatalf("a good policy: %v", err)
	}
	refused := authority.DelegatePolicy{Version: 2, Exempt: []string{
		"work_order.teleport", "work_order.create", "delegate.create", "roster.change", "work_order.accept",
	}}
	err := refused.Check()
	if !errors.Is(err, authority.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	for _, id := range []string{"work_order.teleport", "work_order.create", "delegate.create", "roster.change"} {
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

func TestADelegatePolicyNeverExemptsInAOneWayCategory(t *testing.T) {
	policy := authority.DelegatePolicy{Version: 2, Exempt: []string{"question.answer", "work_order.accept"}}
	answer, _ := authority.OperationOf("question.answer")
	accept, _ := authority.OperationOf("work_order.accept")
	cancel, _ := authority.OperationOf("work_order.cancel")
	operational, _ := authority.OperationOf("work_order.create")
	if !policy.Exempts([]authority.Operation{answer}, false) {
		t.Error("an exempt answer outside a one-way category is not exempt")
	}
	if policy.Exempts([]authority.Operation{answer}, true) {
		t.Error("an answer in a one-way category is exempt")
	}
	if policy.Exempts([]authority.Operation{accept, cancel}, false) {
		t.Error("a call with one consent operation the policy does not list is exempt")
	}
	if !policy.Exempts([]authority.Operation{accept, operational}, false) {
		t.Error("an exempt consent operation beside an operational one is not exempt")
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
