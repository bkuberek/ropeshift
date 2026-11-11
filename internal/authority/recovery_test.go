package authority_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
)

// The built-in recovery policy is the one the design states: an orchestrator restarts a stalled lane,
// retries failed work, and replans blocked work, and resets a breaker at most twice a day.
func TestTheBuiltInRecoveryPolicyIsTheOneTheDesignStates(t *testing.T) {
	want := authority.RecoveryPolicy{Version: 1, RestartStalled: true, MaxBreakerResetsPerDay: 2, RetryFailed: true, ReplanBlocked: true}
	if got := authority.DefaultRecoveryPolicy(); got != want {
		t.Errorf("the built-in policy reads %+v", got)
	}
	if err := want.Check(); err != nil {
		t.Errorf("the built-in policy does not check: %v", err)
	}
	for _, line := range []string{"[recovery_policy]", "version = 1", "restart_stalled = true", "max_breaker_resets_per_day = 2", "retry_failed = true", "replan_blocked = true"} {
		if !strings.Contains(authority.BuiltInRecoveryPolicyBody, line+"\n") {
			t.Errorf("the built-in body does not say %q", line)
		}
	}
}

func TestARecoveryPolicyNamesAVersionAndNoNegativeBound(t *testing.T) {
	for _, bad := range []authority.RecoveryPolicy{{Version: 0}, {Version: 2, MaxBreakerResetsPerDay: -1}} {
		if err := bad.Check(); !errors.Is(err, authority.ErrBadRecoveryPolicy) {
			t.Errorf("%+v checks: %v", bad, err)
		}
	}
}

// A version keeps its body, and a body that no longer matches its digest reads as nothing.
func TestARecoveryPolicyVersionReadsBackAndRefusesAnotherBody(t *testing.T) {
	draft := authority.RecoveryPolicyDraft{Body: authority.BuiltInRecoveryPolicyBody, Policy: authority.DefaultRecoveryPolicy()}
	version, err := authority.NewRecoveryPolicyVersion("p1", "member-1", draft, 5)
	if err != nil {
		t.Fatal(err)
	}
	if version.Kind != authority.PolicyRecovery || version.Version != 1 {
		t.Errorf("version %+v", version)
	}
	got, err := authority.ReadRecoveryPolicy(version)
	if err != nil || got != draft.Policy {
		t.Errorf("read %+v, %v", got, err)
	}
	version.Body += "# changed\n"
	if _, err := authority.ReadRecoveryPolicy(version); !errors.Is(err, authority.ErrPolicyDigest) {
		t.Errorf("a changed body reads: %v", err)
	}
}

// Each act checks only what bounds it, and a refusal names the check that failed and the limit.
func TestEachRecoveryActIsHeldToItsOwnChecks(t *testing.T) {
	builtIn := authority.DefaultRecoveryPolicy()
	off := authority.RecoveryPolicy{Version: 2, MaxBreakerResetsPerDay: 0}
	cases := []struct {
		name   string
		policy authority.RecoveryPolicy
		facts  authority.RecoveryFacts
		check  string
		limit  string
	}{
		{"a stalled lane on this machine restarts", builtIn, authority.RecoveryFacts{Act: authority.RecoverRestart, OnMachine: true}, "", ""},
		{"a restart the policy turns off", off, authority.RecoveryFacts{Act: authority.RecoverRestart, OnMachine: true}, authority.CheckRestartStalled, "restart_stalled is false"},
		{"a restart of another machine's lane", builtIn, authority.RecoveryFacts{Act: authority.RecoverRestart}, authority.CheckSameMachine, "another machine"},
		{"a reset within the day's bound", builtIn, authority.RecoveryFacts{Act: authority.RecoverReset, OnMachine: true, ResetsToday: 1}, "", ""},
		{"a reset of another machine's lane", builtIn, authority.RecoveryFacts{Act: authority.RecoverReset}, authority.CheckSameMachine, "another machine"},
		{"a reset of a lane its day stopped", builtIn, authority.RecoveryFacts{Act: authority.RecoverReset, OnMachine: true, DayCapped: true}, authority.CheckDayCap, "daily cap"},
		{"a reset past the day's bound", builtIn, authority.RecoveryFacts{Act: authority.RecoverReset, OnMachine: true, ResetsToday: 2}, authority.CheckBreakerResets, "2 of max_breaker_resets_per_day 2"},
		{"a reset a zero bound refuses", off, authority.RecoveryFacts{Act: authority.RecoverReset, OnMachine: true}, authority.CheckBreakerResets, "0 of max_breaker_resets_per_day 0"},
		{"a retry with attempts left", builtIn, authority.RecoveryFacts{Act: authority.RecoverRetry, Attempts: 1, MaxAttempts: 3}, "", ""},
		{"a retry the policy turns off", off, authority.RecoveryFacts{Act: authority.RecoverRetry, Attempts: 1, MaxAttempts: 3}, authority.CheckRetryFailed, "retry_failed is false"},
		{"a retry with no attempts left", builtIn, authority.RecoveryFacts{Act: authority.RecoverRetry, Attempts: 3, MaxAttempts: 3}, authority.CheckAttempts, "3 of max_attempts 3"},
		{"a retry of work that names no bound", builtIn, authority.RecoveryFacts{Act: authority.RecoverRetry, Attempts: 1}, authority.CheckAttempts, "1 of max_attempts 1"},
		{"a replan", builtIn, authority.RecoveryFacts{Act: authority.RecoverReplan}, "", ""},
		{"a replan the policy turns off", off, authority.RecoveryFacts{Act: authority.RecoverReplan}, authority.CheckReplanBlocked, "replan_blocked is false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refusal, refused := tc.policy.Judge(tc.facts)
			if refused != (tc.check != "") || refusal.Check != tc.check {
				t.Fatalf("judged %+v, refused %v, want check %q", refusal, refused, tc.check)
			}
			if !strings.Contains(refusal.Limit, tc.limit) {
				t.Errorf("the limit reads %q, want it to say %q", refusal.Limit, tc.limit)
			}
		})
	}
}

// An act this build does not know is refused, because a policy that names no check for it bounds nothing.
func TestAnUnknownRecoveryActIsRefused(t *testing.T) {
	if _, refused := authority.DefaultRecoveryPolicy().Judge(authority.RecoveryFacts{Act: "lane_takeover", OnMachine: true}); !refused {
		t.Error("an unknown act passed the policy")
	}
	if authority.KnownRecoveryAct("lane_takeover") {
		t.Error("a takeover reads as a recovery act")
	}
	for _, act := range authority.RecoveryActs() {
		if !authority.KnownRecoveryAct(act) {
			t.Errorf("%s does not read as a recovery act", act)
		}
	}
}
