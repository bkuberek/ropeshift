package lanes_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// TestAHookTakesOnlyTheMessageFromAPrompt is the admission signal and the privacy rule together: a
// prompt's first line names a message, and no word of the prompt leaves the session.
func TestAHookTakesOnlyTheMessageFromAPrompt(t *testing.T) {
	const secret = "a line of the body nobody should read"
	line := "[ropeshift message 01JCB3 kind=answer age=0m02s tier=fresh from=orchestrator reply_to=01JC9Z]\n" + secret
	input, err := json.Marshal(map[string]string{"session_id": "s-1", "prompt": line})
	if err != nil {
		t.Fatal(err)
	}
	event := lanes.NewHookEvent("UserPromptSubmit", input)
	if event.Message != "01JCB3" {
		t.Errorf("message %q", event.Message)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "kind=answer") {
		t.Errorf("a prompt left the session: %s", raw)
	}
}

func TestAPromptWithNoMessageNamesNone(t *testing.T) {
	input, err := json.Marshal(map[string]string{"session_id": "s-1", "prompt": "Go on with the retry."})
	if err != nil {
		t.Fatal(err)
	}
	if event := lanes.NewHookEvent("UserPromptSubmit", input); event.Message != "" {
		t.Errorf("message %q", event.Message)
	}
}

// TestProfileWindowsReadPerKind leaves a kind on the default when its window cannot be read,
// because a bad value must not lose a message.
func TestProfileWindowsReadPerKind(t *testing.T) {
	profile := lanes.Profile{Messages: map[string]lanes.MessageWindow{
		"instruction": {StaleAfter: "1m", ExpireAfter: "3m"},
		"notice":      {StaleAfter: "soon", ExpireAfter: "3m"},
		"answer":      {StaleAfter: "5m", ExpireAfter: "3m"},
	}}
	windows := profile.MessageWindows()
	if len(windows) != 1 {
		t.Fatalf("windows %+v", windows)
	}
	if got := windows["instruction"]; got.ExpireAfter.Minutes() != 3 || got.StaleAfter.Minutes() != 1 {
		t.Errorf("window %+v", got)
	}
}
