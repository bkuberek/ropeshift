package cli_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/messages"
)

func memberEntry(from api.TalkFrom) api.TalkEntry {
	return api.TalkEntry{ID: "m", At: 0, Kind: api.TalkKindMember, From: &from, Body: "Sam asks for three", Message: &messages.Message{State: messages.Queued}}
}

// A terminal says who sent a message by the credential's kind, never by its label, which a member chooses.
func TestAStreamNamesTheAssistantByItsCredentialsKind(t *testing.T) {
	assistant := cli.TalkLine(memberEntry(api.TalkFrom{Kind: "delegate", ID: "member-1/dlg_a", Label: "assistant", DelegateKind: "assistant"}))
	if !strings.Contains(assistant, "  assistant for member-1: Sam asks") {
		t.Errorf("the assistant reads %q", assistant)
	}
	for name, from := range map[string]api.TalkFrom{
		"an outside delegate called the assistant": {Kind: "delegate", ID: "member-1/dlg_b", Label: "assistant", DelegateKind: "external"},
		"one whose record is gone":                 {Kind: "delegate", ID: "member-1/dlg_c", Label: "dlg_c"},
	} {
		line := cli.TalkLine(memberEntry(from))
		if strings.Contains(line, "  assistant for member-1:") || !strings.Contains(line, "key "+from.Label+" for member-1: Sam asks") {
			t.Errorf("%s reads %q", name, line)
		}
	}
	if line := cli.TalkLine(memberEntry(api.TalkFrom{Kind: "member", ID: "member-1", Label: "Sam Rivera"})); !strings.Contains(line, "Sam Rivera: Sam asks") {
		t.Errorf("a member reads %q", line)
	}
}

func TestWhatAnAgentOrAMemberCalledThingsCannotMoveATerminal(t *testing.T) {
	if got := cli.Plain("a\x1b[2Jb​c‮d\n"); got != "a?[2Jb?c?d?" {
		t.Errorf("got %q", got)
	}
	line := cli.TalkLine(memberEntry(api.TalkFrom{Kind: "delegate", ID: "member-1/x", Label: "evil\x1b[31m", DelegateKind: "external"}))
	if strings.ContainsRune(line, 0x1b) {
		t.Errorf("an escape reached the terminal in %q", line)
	}
}
