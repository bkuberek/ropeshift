package evidence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	aSession = "0f8fad5b-d9cb-469f-a165-70867728950e"
	aHome    = "/home/somebody"
	anEmail  = "somebody@example.org"
)

func read(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	if strings.Count(string(raw), "\n") != 1 || !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("not one line: %q", raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

func input(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// What a session did is the point of the log, and what names a session or an account is not.
func TestHookLineKeepsWhatASessionDidAndNothingThatNamesIt(t *testing.T) {
	line := HookLine(input(t, map[string]any{
		"session_id": aSession, "transcript_path": aHome + "/transcript.jsonl", "cwd": aHome + "/work", "prompt_id": aSession,
		"agent_id": "agent", "scratchpad_dir": aHome + "/pad", "tool_use_id": "toolu_1",
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "permission_mode": "default",
		"tool_input": map[string]any{"command": "git commit -m one", "description": "commit for " + anEmail},
	}), Tags{Lane: "lane-1", Generation: "2", Home: aHome}, time.UnixMilli(1_800_000_000_000))
	got := read(t, line)
	if got["hook_event_name"] != "PreToolUse" || got["tool_name"] != "Bash" || got["permission_mode"] != "default" {
		t.Errorf("what it did went missing: %s", line)
	}
	if got["lane"] != "lane-1" || got["generation"] != "2" || got["at_ms"] != float64(1_800_000_000_000) {
		t.Errorf("where it stood went missing: %s", line)
	}
	call, _ := got["tool_input"].(map[string]any)
	if call["command"] != "git commit -m one" {
		t.Errorf("the call's arguments went missing: %s", line)
	}
	for _, name := range []string{"session_id", "transcript_path", "cwd", "prompt_id", "agent_id", "scratchpad_dir", "tool_use_id"} {
		if _, kept := got[name]; kept {
			t.Errorf("%s stayed: %s", name, line)
		}
	}
	for _, private := range []string{aSession, aHome, anEmail} {
		if strings.Contains(string(line), private) {
			t.Errorf("%s stayed: %s", private, line)
		}
	}
}

// A field this log has never heard of stays out, so a CLI that adds one adds nothing here.
func TestHookLineKeepsOnlyWhatItNames(t *testing.T) {
	line := HookLine(input(t, map[string]any{"hook_event_name": "Stop", "account_uuid": "x", "last_assistant_message": "done"}), Tags{}, time.UnixMilli(1))
	got := read(t, line)
	if got["last_assistant_message"] != "done" {
		t.Errorf("a turn's text went missing: %s", line)
	}
	if _, kept := got["account_uuid"]; kept {
		t.Errorf("an unknown field stayed: %s", line)
	}
}

// A session ID can sit inside what a tool answered, so the line is read as text too.
func TestHookLineScrubsInsideValues(t *testing.T) {
	line := HookLine(input(t, map[string]any{
		"hook_event_name": "PostToolUse", "tool_response": map[string]any{"text": "session " + aSession + " of " + anEmail + " in " + aHome + "/x"},
	}), Tags{Home: aHome}, time.UnixMilli(1))
	for _, private := range []string{aSession, anEmail, aHome} {
		if strings.Contains(string(line), private) {
			t.Errorf("%s stayed: %s", private, line)
		}
	}
	if !strings.Contains(string(line), SessionMark) {
		t.Errorf("no mark where the session stood: %s", line)
	}
}

// Input that does not parse says so, and none of it reaches the log.
func TestHookLineOnInputItCannotRead(t *testing.T) {
	line := HookLine([]byte("not json "+aSession), Tags{Lane: "lane-1"}, time.UnixMilli(1))
	got := read(t, line)
	if got["malformed"] != true {
		t.Errorf("malformed input reads %s", line)
	}
	if strings.Contains(string(line), aSession) {
		t.Errorf("the raw input stayed: %s", line)
	}
}

func TestScrubLeavesOtherTextAlone(t *testing.T) {
	text := "lane-1 tripped at generation 3: retries log at warn"
	if got := Scrub(text, aHome); got != text {
		t.Errorf("Scrub(%q) = %q", text, got)
	}
}
