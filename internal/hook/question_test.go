package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
)

const askUserInput = `{"session_id":"s1","tool_name":"AskUserQuestion","tool_use_id":"toolu_1","tool_input":{"questions":[
  {"question":"Which retry budget?","options":[{"label":"Three"},{"label":"Five"}]}]}}`

const questionToken = "a-lanes-token"

// questionHook runs one PreToolUse hook on a compiled policy, with the worker down, and
// returns what it printed and what it spooled.
func questionHook(t *testing.T, policy lanes.DenyPolicy, input string) (string, []lanes.HookEvent) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(dir, "spool")
	env := map[string]string{api.EnvPolicy: path, api.EnvSpool: spool, api.EnvLaneToken: questionToken, api.EnvLaneID: "lane-1", api.EnvGeneration: "1"}
	var out bytes.Buffer
	if code := Main([]string{eventPreToolUse}, strings.NewReader(input), &out, func(name string) string { return env[name] }); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var events []lanes.HookEvent
	spooled, err := os.ReadFile(spool)
	if err != nil {
		t.Fatal(err)
	}
	digestOf := func(string) (string, error) { return lanes.TokenDigest(questionToken), nil }
	for line := range bytes.SplitSeq(bytes.TrimSpace(spooled), []byte("\n")) {
		event, err := lanes.OpenHook(line, digestOf)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return out.String(), events
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("hook output\n got %s\nwant %s", got, want)
	}
}

// TestAHeadlessLaneDefersTheQuestionTool is the conversion: the turn ends, and the event
// carries the question to the worker, which records it.
func TestAHeadlessLaneDefersTheQuestionTool(t *testing.T) {
	out, events := questionHook(t, lanes.DenyPolicy{QuestionTool: lanes.QuestionToolDefer}, askUserInput)
	golden(t, "question-headless.golden", out)
	if len(events) != 1 || len(events[0].Asked) != 1 {
		t.Fatalf("events %+v", events)
	}
	asked := events[0].Asked[0]
	if asked.Text != "Which retry budget?" || strings.Join(asked.Options, ",") != "Three,Five" {
		t.Errorf("asked %+v", asked)
	}
}

// TestAnInteractiveLaneIsToldToAsk names the tool in the denial, so the agent asks through
// the ladder and the terminal stays quiet.
func TestAnInteractiveLaneIsToldToAsk(t *testing.T) {
	out, events := questionHook(t, lanes.DenyPolicy{QuestionTool: lanes.QuestionToolDeny}, askUserInput)
	golden(t, "question-interactive.golden", out)
	if len(events) != 1 || len(events[0].Asked) != 0 {
		t.Errorf("a denied question reached the worker: %+v", events)
	}
}

func TestOnlyTheQuestionToolIsConverted(t *testing.T) {
	input := `{"session_id":"s1","tool_name":"Bash","tool_use_id":"t","tool_input":{"command":"go test","questions":[{"question":"no"}]}}`
	out, events := questionHook(t, lanes.DenyPolicy{QuestionTool: lanes.QuestionToolDefer}, input)
	if out != "" || len(events) != 1 || len(events[0].Asked) != 0 {
		t.Errorf("out %q, events %+v", out, events)
	}
}

// TestASessionWithNoQuestionRuleIsLeftAlone keeps a policy compiled before this rule, or by
// another driver, as it was.
func TestASessionWithNoQuestionRuleIsLeftAlone(t *testing.T) {
	out, events := questionHook(t, lanes.DenyPolicy{}, askUserInput)
	if out != "" || len(events) != 1 || len(events[0].Asked) != 0 {
		t.Errorf("out %q, events %+v", out, events)
	}
}
