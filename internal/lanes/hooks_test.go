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

// TestAStopCountsTheBackgroundWorkStillRunning reads the `background_tasks` a Stop hook gets, which tell a
// session that is done from one waiting for background work to wake it
// (https://code.claude.com/docs/en/hooks#stop-input). Only the count leaves the session: a task's description
// and command are what it was asked to do.
func TestAStopCountsTheBackgroundWorkStillRunning(t *testing.T) {
	const secret = "research the identity providers"
	input := []byte(`{"session_id":"s-1","hook_event_name":"Stop","last_assistant_message":"I started four agents.",
"background_tasks":[{"id":"a","type":"subagent","status":"running","description":"` + secret + `","agent_type":"general-purpose"},
{"id":"b","type":"shell","status":"running","description":"build","command":"make ` + secret + `"}],"session_crons":[]}`)
	event := lanes.NewHookEvent(lanes.EventStop, input)
	if event.Background != 2 {
		t.Errorf("background %d, want the 2 tasks in flight", event.Background)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "I started") {
		t.Errorf("a task's text left the session: %s", raw)
	}
	for name, input := range map[string]string{
		"an empty list": `{"session_id":"s-1","background_tasks":[]}`,
		"no list":       `{"session_id":"s-1"}`,
	} {
		if event := lanes.NewHookEvent(lanes.EventStop, []byte(input)); event.Background != 0 {
			t.Errorf("%s: background %d", name, event.Background)
		}
	}
	if event := lanes.NewHookEvent(lanes.EventPreToolUse, input); event.Background != 0 {
		t.Errorf("a tool call counts background work: %d", event.Background)
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
