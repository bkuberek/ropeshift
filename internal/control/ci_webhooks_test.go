package control_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// The signed webhook deliveries of CI events are hints (FR-SPEND-20). Each one is verified against the secret of the
// repository it names before anything of it is trusted, counted when it is refused, dropped when it repeats, and
// turned into a hint for the head it names, which the read then decides.

type hookRepos map[string][]control.CIRegistration

func (r hookRepos) CIRepository(_ context.Context, host, owner, repo string) ([]control.CIRegistration, error) {
	return r[strings.ToLower(host+"/"+owner+"/"+repo)], nil
}

type hookRig struct {
	hooks  *control.CIWebhooks
	board  *control.CIHints
	warned []string
	now    time.Time
}

const hookKey = "the secret of acme widgets"

func newHookRig(t *testing.T, repos hookRepos, env map[string]string) *hookRig {
	t.Helper()
	r := &hookRig{now: time.UnixMilli(1_700_000_000_000)}
	clock := func() time.Time { return r.now }
	r.board = control.NewCIHints(clock)
	r.hooks = control.NewCIWebhooks(repos, r.board, func(name string) (string, bool) { v, ok := env[name]; return v, ok }, clock, func(line string) { r.warned = append(r.warned, line) })
	return r
}

func widgetsRepos() hookRepos {
	return hookRepos{"github.com/acme/widgets": {{Binding: hintedRepo, SecretEnv: "HOOK_WIDGETS"}}}
}

func sign(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func suite(action, head string) []byte {
	return fmt.Appendf(nil, `{"action":%q,"check_suite":{"head_sha":%q},"repository":{"full_name":"acme/widgets"}}`, action, head)
}

func delivery(event, id string, body []byte) control.CIDelivery {
	return control.CIDelivery{Event: event, ID: id, Signature: sign(hookKey, body), Body: body}
}

func (r *hookRig) hinted() []ledger.CIHint {
	hints, _ := r.board.Since(0)
	return hints
}

func refusal(t *testing.T, err error) *control.CIRefusal {
	t.Helper()
	var refused *control.CIRefusal
	if !errors.As(err, &refused) {
		t.Fatalf("the delivery was not refused: %v", err)
	}
	return refused
}

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestASignedDeliveryHintsTheHeadItNames(t *testing.T) {
	t.Parallel()
	for _, event := range []struct{ name, action string }{
		{"check_suite", "completed"}, {"check_run", "completed"}, {"workflow_run", "requested"}, {"workflow_run", "in_progress"}, {"workflow_run", "completed"},
	} {
		t.Run(event.name+"/"+event.action, func(t *testing.T) {
			r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
			body := fmt.Appendf(nil, `{"action":%q,%q:{"head_sha":%q,"pull_requests":[{"number":7}]},"repository":{"full_name":"acme/widgets"}}`, event.action, event.name, sha)
			outcome, err := r.hooks.Receive(t.Context(), delivery(event.name, "d-1", body))
			if err != nil || outcome != control.CIAccepted {
				t.Fatalf("a signed %s %s came to %q: %v", event.name, event.action, outcome, err)
			}
			if hints := r.hinted(); len(hints) != 1 || hints[0].Binding != hintedRepo.String() || hints[0].Head != sha {
				t.Errorf("the delivery made the hints %+v", hints)
			}
		})
	}
}

func TestADeliveryThatSaysNothingOfCIIsAnsweredAndHintsNothing(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	for i, tt := range []struct {
		event string
		body  []byte
		want  control.CIOutcome
	}{
		{"check_suite", suite("requested", sha), control.CIIgnored},
		{"check_run", []byte(`{"action":"created","check_run":{"head_sha":"` + sha + `"},"repository":{"full_name":"acme/widgets"}}`), control.CIIgnored},
		{"push", []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/widgets"}}`), control.CIIgnored},
		{"ping", []byte(`{"zen":"Keep it logically awesome.","repository":{"full_name":"acme/widgets"}}`), control.CIPong},
	} {
		outcome, err := r.hooks.Receive(t.Context(), delivery(tt.event, fmt.Sprint("d-", i), tt.body))
		if err != nil || outcome != tt.want {
			t.Errorf("%s came to %q, %v, want %q", tt.event, outcome, err, tt.want)
		}
	}
	if hints := r.hinted(); len(hints) != 0 {
		t.Errorf("deliveries that say nothing of CI made hints: %+v", hints)
	}
}

// A refusal is counted by its reason and is never a hint, and the reason is what an operator reads while the caller is
// told the same of each.
func TestABadOrMissingSignatureIsRefusedAndCounted(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	body := suite("completed", sha)
	for name, tt := range map[string]struct {
		signature, reason string
	}{
		"missing":           {"", control.RefusedMissingSignature},
		"another key":       {sign("not the secret", body), control.RefusedBadSignature},
		"no prefix":         {strings.TrimPrefix(sign(hookKey, body), "sha256="), control.RefusedBadSignature},
		"not hex":           {"sha256=zzzz", control.RefusedBadSignature},
		"another body":      {sign(hookKey, []byte("{}")), control.RefusedBadSignature},
		"the legacy digest": {"sha1=" + strings.Repeat("a", 40), control.RefusedBadSignature},
	} {
		d := control.CIDelivery{Event: "check_suite", ID: "d-" + name, Signature: tt.signature, Body: body}
		if _, err := r.hooks.Receive(t.Context(), d); refusal(t, err).Reason != tt.reason {
			t.Errorf("a %s signature was refused as %v, want %s", name, err, tt.reason)
		}
	}
	if hints := r.hinted(); len(hints) != 0 {
		t.Errorf("a refused delivery made hints: %+v", hints)
	}
	counts := r.hooks.Counts()
	if counts[control.RefusedMissingSignature] != 1 || counts[control.RefusedBadSignature] != 5 {
		t.Errorf("the refusals were counted as %v", counts)
	}
	heard := map[string]bool{}
	for _, reason := range []string{control.RefusedMissingSignature, control.RefusedBadSignature} {
		for _, line := range r.warned {
			heard[reason] = heard[reason] || strings.Contains(line, reason)
		}
	}
	if len(r.warned) != 2 || !heard[control.RefusedMissingSignature] || !heard[control.RefusedBadSignature] {
		t.Errorf("an operator heard %q, want one warning of the first refusal of each kind", r.warned)
	}
	for _, line := range r.warned {
		if strings.Contains(line, hookKey) || strings.Contains(line, "HOOK_WIDGETS") {
			t.Errorf("a warning names the secret or its variable: %q", line)
		}
	}
}

// A repository ropeshift does not hold, one that names no variable, and one whose variable is not set are refused
// the same way as a bad signature, so a caller learns nothing of which repositories there are.
func TestADeliveryForARepositoryThatCannotBeVerifiedIsRefused(t *testing.T) {
	t.Parallel()
	body := suite("completed", sha)
	tests := map[string]struct {
		repos  hookRepos
		env    map[string]string
		reason string
	}{
		"unknown repository":      {hookRepos{}, map[string]string{"HOOK_WIDGETS": hookKey}, control.RefusedUnknownRepository},
		"no variable named":       {hookRepos{"github.com/acme/widgets": {{Binding: hintedRepo}}}, map[string]string{"HOOK_WIDGETS": hookKey}, control.RefusedNoSecret},
		"variable not set":        {widgetsRepos(), map[string]string{}, control.RefusedNoSecret},
		"variable set to nothing": {widgetsRepos(), map[string]string{"HOOK_WIDGETS": ""}, control.RefusedNoSecret},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := newHookRig(t, tt.repos, tt.env)
			_, err := r.hooks.Receive(t.Context(), delivery("check_suite", "d-1", body))
			if refused := refusal(t, err); refused.Reason != tt.reason || refused.Malformed {
				t.Errorf("refused as %+v, want %s", refused, tt.reason)
			}
			if len(r.hinted()) != 0 || r.hooks.Counts()[tt.reason] != 1 {
				t.Errorf("hints %+v, counts %v", r.hinted(), r.hooks.Counts())
			}
		})
	}
}

// A delivery for one repository, signed with another repository's secret, is a bad signature.
func TestEachRepositoryIsVerifiedByItsOwnSecret(t *testing.T) {
	t.Parallel()
	other := ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "gadgets"}
	r := newHookRig(t, hookRepos{
		"github.com/acme/widgets": {{Binding: hintedRepo, SecretEnv: "HOOK_WIDGETS"}},
		"github.com/acme/gadgets": {{Binding: other, SecretEnv: "HOOK_GADGETS"}},
	}, map[string]string{"HOOK_WIDGETS": hookKey, "HOOK_GADGETS": "the secret of acme gadgets"})
	body := suite("completed", sha)
	if _, err := r.hooks.Receive(t.Context(), control.CIDelivery{Event: "check_suite", ID: "d-1", Signature: sign("the secret of acme gadgets", body), Body: body}); refusal(t, err).Reason != control.RefusedBadSignature {
		t.Errorf("a delivery for widgets signed with gadgets' secret: %v", err)
	}
}

// A repository two projects hold is hinted in each project whose own secret verifies the delivery, and in no other.
func TestARepositoryTwoProjectsHoldIsHintedWhereItsSecretVerifies(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, hookRepos{"github.com/acme/widgets": {
		{Binding: hintedRepo, SecretEnv: "HOOK_A"}, {Binding: hintedRepo, SecretEnv: "HOOK_B"}, {Binding: ledger.ForgeBinding{Host: "github.com", Owner: "Acme", Repo: "Widgets"}, SecretEnv: "HOOK_A"},
	}}, map[string]string{"HOOK_A": hookKey, "HOOK_B": "another"})
	if outcome, err := r.hooks.Receive(t.Context(), delivery("check_suite", "d-1", suite("completed", sha))); err != nil || outcome != control.CIAccepted {
		t.Fatalf("came to %q: %v", outcome, err)
	}
	hints := r.hinted()
	if len(hints) != 2 || hints[0].Binding != "github.com/acme/widgets" || hints[1].Binding != "github.com/Acme/Widgets" {
		t.Errorf("the hints are %+v, want one for each binding the secret verifies, and none repeated", hints)
	}
}

// A delivery ID is dropped the second time, only once the signature has held: a forged delivery cannot spend the ID
// of the real one.
func TestAReplayedDeliveryIsDroppedAndAForgeryCannotSpendItsID(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	body := suite("completed", sha)
	forged := control.CIDelivery{Event: "check_suite", ID: "d-1", Signature: sign("not the secret", body), Body: body}
	if _, err := r.hooks.Receive(t.Context(), forged); refusal(t, err).Reason != control.RefusedBadSignature {
		t.Fatalf("a forgery came to %v", err)
	}
	real := delivery("check_suite", "d-1", body)
	if outcome, err := r.hooks.Receive(t.Context(), real); err != nil || outcome != control.CIAccepted {
		t.Fatalf("the real delivery after a forgery with its ID came to %q: %v", outcome, err)
	}
	if outcome, err := r.hooks.Receive(t.Context(), real); err != nil || outcome != control.CIDuplicate {
		t.Errorf("the same delivery again came to %q: %v", outcome, err)
	}
	if hints := r.hinted(); len(hints) != 1 {
		t.Errorf("a replay made hints: %+v", hints)
	}
	if got := r.hooks.Counts()[control.DroppedReplay]; got != 1 {
		t.Errorf("the replay was counted %d times", got)
	}
	r.now = r.now.Add(2 * time.Hour)
	if outcome, err := r.hooks.Receive(t.Context(), real); err != nil || outcome != control.CIAccepted {
		t.Errorf("a delivery an age later came to %q: %v, and a redelivery that late is read again", outcome, err)
	}
}

// The memory of delivery IDs is bounded, and the oldest ID goes first.
func TestTheMemoryOfDeliveryIDsIsBounded(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	body := suite("completed", sha)
	for i := range control.CIDeliveriesHeld + 10 {
		if _, err := r.hooks.Receive(t.Context(), delivery("check_suite", fmt.Sprint("d-", i), body)); err != nil {
			t.Fatal(err)
		}
	}
	if outcome, _ := r.hooks.Receive(t.Context(), delivery("check_suite", "d-0", body)); outcome != control.CIAccepted {
		t.Errorf("the oldest ID is still remembered after %d more: %q", control.CIDeliveriesHeld+9, outcome)
	}
	if outcome, _ := r.hooks.Receive(t.Context(), delivery("check_suite", fmt.Sprint("d-", control.CIDeliveriesHeld+9), body)); outcome != control.CIDuplicate {
		t.Errorf("the newest ID is forgotten: %q", outcome)
	}
}

// A delivery that holds right and is no delivery of GitHub's is malformed, which a caller is told, because its
// signature held.
func TestADeliverySignedRightButMalformedIsRefusedAsMalformed(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	body := suite("completed", sha)
	for name, d := range map[string]control.CIDelivery{
		"no delivery ID": delivery("check_suite", "", body),
		"no event":       delivery("", "d-1", body),
		"not JSON":       {Event: "check_suite", ID: "d-2", Signature: sign(hookKey, []byte("nope")), Body: []byte("nope")},
	} {
		// A body that is not JSON names no repository, so its signature cannot be checked: unknown, not malformed.
		_, err := r.hooks.Receive(t.Context(), d)
		refused := refusal(t, err)
		if name == "not JSON" {
			if refused.Reason != control.RefusedUnknownRepository {
				t.Errorf("%s was refused as %+v", name, refused)
			}
			continue
		}
		if !refused.Malformed {
			t.Errorf("%s was refused as %+v, want malformed", name, refused)
		}
	}
}

// A head that is no commit ID is not put in a hint: the hint then names every pull request of the repository.
func TestAHeadThatIsNoCommitIDMakesAHintForTheWholeRepository(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	for i, head := range []string{"", "../../etc", "not a sha"} {
		if _, err := r.hooks.Receive(t.Context(), delivery("check_suite", fmt.Sprint("d-", i), suite("completed", head))); err != nil {
			t.Fatal(err)
		}
	}
	for _, hint := range r.hinted() {
		if hint.Head != "" {
			t.Errorf("the hint names the head %q", hint.Head)
		}
	}
}

// A GitHub Enterprise server names its host, and the repository is the one on that host.
func TestAnEnterpriseDeliveryNamesItsHost(t *testing.T) {
	t.Parallel()
	enterprise := ledger.ForgeBinding{Host: "git.example.com", Owner: "acme", Repo: "widgets"}
	r := newHookRig(t, hookRepos{"git.example.com/acme/widgets": {{Binding: enterprise, SecretEnv: "HOOK_WIDGETS"}}}, map[string]string{"HOOK_WIDGETS": hookKey})
	d := delivery("check_suite", "d-1", suite("completed", sha))
	if _, err := r.hooks.Receive(t.Context(), d); refusal(t, err).Reason != control.RefusedUnknownRepository {
		t.Errorf("a delivery with no host for a repository on another host: %v", err)
	}
	d.ID, d.EnterpriseHost = "d-2", "git.example.com"
	if outcome, err := r.hooks.Receive(t.Context(), d); err != nil || outcome != control.CIAccepted {
		t.Errorf("a delivery from the enterprise host came to %q: %v", outcome, err)
	}
}

// A warning is for the first refusal of a kind and each hundredth after it, so a flood of guesses fills no log.
func TestAFloodOfRefusalsWarnsOnlyNowAndThen(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, widgetsRepos(), map[string]string{"HOOK_WIDGETS": hookKey})
	body := suite("completed", sha)
	for i := range 250 {
		_, _ = r.hooks.Receive(t.Context(), control.CIDelivery{Event: "check_suite", ID: fmt.Sprint("d-", i), Signature: sign("guess", body), Body: body})
	}
	if len(r.warned) != 3 || r.hooks.Counts()[control.RefusedBadSignature] != 250 {
		t.Errorf("250 refusals warned %d times and were counted %d", len(r.warned), r.hooks.Counts()[control.RefusedBadSignature])
	}
}
