package authority_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
)

// A member's conversations are theirs alone: no delegate reaches one of their operations, so the
// assistant cannot start, rename, or delete a conversation, and no card stands in for one.
func TestEveryConversationOperationIsAMembersAlone(t *testing.T) {
	for _, id := range []string{
		authority.OpConversationCreate, authority.OpConversationOpen, authority.OpConversationStop, authority.OpConversationRename,
		authority.OpConversationDelete, authority.OpConversationCompact, authority.OpConversationClear, authority.OpConversationMessage,
	} {
		op, ok := authority.OperationOf(id)
		if !ok {
			t.Fatalf("no operation %s", id)
		}
		if op.Class != authority.ClassMemberOnly || op.Installation {
			t.Errorf("%s is %s, installation %v", id, op.Class, op.Installation)
		}
		for _, action := range op.Actions {
			if authority.Allowed(delegate, action) {
				t.Errorf("%s needs %s, which a delegate holds", id, action)
			}
		}
	}
	if authority.Allowed(delegate, authority.ActionConverse) {
		t.Error("a delegate may converse as its member")
	}
}

func TestTheAssistantPolicyStopsAnIdleSessionWithinItsBounds(t *testing.T) {
	builtIn := authority.BuiltInAssistantPolicy()
	if err := builtIn.Check(); err != nil {
		t.Fatal(err)
	}
	if got := builtIn.IdleStopAfter(); got != 15*time.Minute {
		t.Errorf("the built-in idle stop is %s", got)
	}
	for _, idle := range []string{"59s", "24h1m", "soon", "", "-5m"} {
		policy := authority.AssistantPolicy{Version: 2, IdleStop: idle}
		if err := policy.Check(); !errors.Is(err, authority.ErrInvalid) {
			t.Errorf("idle_stop %q: %v", idle, err)
		}
	}
	for _, idle := range []string{"1m", "24h", "90m"} {
		policy := authority.AssistantPolicy{Version: 2, IdleStop: idle}
		if err := policy.Check(); err != nil {
			t.Errorf("idle_stop %q: %v", idle, err)
		}
	}
	if err := (authority.AssistantPolicy{Version: 0, IdleStop: "15m"}).Check(); !errors.Is(err, authority.ErrInvalid) {
		t.Errorf("version 0: %v", err)
	}
	version, err := authority.NewAssistantVersion("p1", "m1", authority.AssistantDraft{Body: authority.BuiltInAssistantBody, Policy: builtIn}, 5)
	if err != nil {
		t.Fatal(err)
	}
	read, err := authority.ReadAssistant(version)
	if err != nil || read.IdleStopAfter() != 15*time.Minute || version.Kind != authority.PolicyAssistant {
		t.Errorf("read %+v, %v", read, err)
	}
}

// An assistant credential is made by the control plane for one generation of one conversation's
// session; a member never asks for one, and the external kind is the only one a member makes.
func TestAMemberMakesOnlyExternalCredentials(t *testing.T) {
	request := authority.DelegateRequest{Member: "m1", Label: "x", Kind: authority.DelegateAssistant, ExpiresInMs: authority.MinDelegateTTLMs}
	if err := request.Check(); !errors.Is(err, authority.ErrInvalid) {
		t.Errorf("a member asked for an assistant credential: %v", err)
	}
	credential := authority.DelegateCredential{ID: "dlg_1", Member: "m1", Kind: authority.DelegateAssistant, Conversation: "c1", Generation: 3, CreatedAt: 1}
	if credential.State(1<<62) != authority.DelegateActive {
		t.Errorf("an assistant credential with no expiry reads %s", credential.State(1<<62))
	}
	if !credential.Assistant() {
		t.Error("an assistant credential reads as external")
	}
}

// A session that has received no message since it started stops after the warm idle stop, two minutes by
// default, never later than the idle stop, and a published one holds to the same bounds.
func TestTheAssistantPolicyStopsAnUnusedSessionSooner(t *testing.T) {
	builtIn := authority.BuiltInAssistantPolicy()
	if got := builtIn.WarmIdleStopAfter(); got != 2*time.Minute {
		t.Errorf("the built-in warm idle stop is %s", got)
	}
	if !strings.Contains(authority.BuiltInAssistantBody, `warm_idle_stop = "2m"`) {
		t.Errorf("the built-in body says %q", authority.BuiltInAssistantBody)
	}
	if got := (authority.AssistantPolicy{Version: 2, IdleStop: "30m"}).WarmIdleStopAfter(); got != 2*time.Minute {
		t.Errorf("a policy that names no warm idle stop stops an unused session after %s", got)
	}
	if got := (authority.AssistantPolicy{Version: 2, IdleStop: "1m", WarmIdleStop: "5m"}).WarmIdleStopAfter(); got != time.Minute {
		t.Errorf("a warm idle stop past the idle stop reads %s", got)
	}
	for _, warm := range []string{"59s", "24h1m", "soon", "-1m"} {
		if err := (authority.AssistantPolicy{Version: 2, IdleStop: "15m", WarmIdleStop: warm}).Check(); !errors.Is(err, authority.ErrInvalid) {
			t.Errorf("warm_idle_stop %q: %v", warm, err)
		}
	}
	for _, warm := range []string{"", "1m", "10m"} {
		if err := (authority.AssistantPolicy{Version: 2, IdleStop: "15m", WarmIdleStop: warm}).Check(); err != nil {
			t.Errorf("warm_idle_stop %q: %v", warm, err)
		}
	}
}

func TestTheAssistantsLabelIsReservedAgainstOutsideCredentials(t *testing.T) {
	ok := authority.DelegateRequest{Member: "m", Project: "p", Label: "assistant bot", ExpiresInMs: authority.MinDelegateTTLMs}
	if err := ok.Check(); err != nil {
		t.Fatalf("a label that holds the word is refused: %v", err)
	}
	for _, label := range []string{"assistant", "Assistant", "ASSISTANT", " assistant ", "Assistant "} {
		request := ok
		request.Label = label
		err := request.Check()
		if !errors.Is(err, authority.ErrInvalid) || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("label %q: got %v, want a refusal that says it is reserved", label, err)
		}
	}
}

// A label that hides a character, or looks like the assistant's, is no label for an outside credential: a
// control, a zero-width character, a byte order mark, and a bidirectional control are refused whole, and a
// compatibility form reads as the plain letters it stands for.
func TestALabelCannotHideCharactersOrPassForTheAssistants(t *testing.T) {
	ok := authority.DelegateRequest{Member: "m", Project: "p", Label: "planning assistant", ExpiresInMs: authority.MinDelegateTTLMs}
	for name, label := range map[string]string{
		"zero-width space":       "assistant\u200b",
		"zero-width joiner":      "assis\u200dtant",
		"byte order mark":        "\ufeffassistant",
		"right-to-left mark":     "assistant\u200f",
		"right-to-left override": "\u202eassistant",
		"isolate":                "\u2066assistant\u2069",
		"escape":                 "bot\x1b[2J",
		"newline":                "two\nlines",
		"soft hyphen":            "assis\u00adtant",
	} {
		request := ok
		request.Label = label
		if err := request.Check(); !errors.Is(err, authority.ErrInvalid) {
			t.Errorf("%s: got %v, want a refusal", name, err)
		}
	}
	request := ok
	request.Label = "Planungs-Assistent für Ärzte"
	if err := request.Check(); err != nil {
		t.Errorf("a plain label in another language is refused: %v", err)
	}
}
