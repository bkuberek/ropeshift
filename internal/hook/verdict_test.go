package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// pastTheDeadline runs one PreToolUse hook on a policy that denies a force push and defers the
// question tool, and then lets its deadline come, as a loaded machine does while the hook still hands
// on its event. A hook that does not decide meets its deadline with no verdict. It returns everything
// the hook printed and every event it spooled.
func pastTheDeadline(t *testing.T, input string, decides bool) (string, []lanes.HookEvent) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	raw, err := json.Marshal(lanes.DenyPolicy{
		Deny:         []lanes.DenyRule{{Tool: lanes.ToolBash, CommandPrefix: "git push --force", Reason: "a force push is denied"}},
		QuestionTool: lanes.QuestionToolDefer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{
		event: eventPreToolUse, policy: path, spool: filepath.Join(dir, "spool.jsonl"), token: "a-token",
		lane: "lane-1", workOrder: "wo", generation: 1, out: new(outbox),
	}
	var out lockedBuffer
	event, taken := take(cfg, strings.NewReader(input))
	untilDecided(cfg, event, &out)
	if decides {
		run(cfg, event, taken, &out)
	}
	cfg.out.settle(cfg)
	spooled, err := os.ReadFile(cfg.spool)
	if err != nil {
		t.Fatal(err)
	}
	var events []lanes.HookEvent
	digestOf := func(string) (string, error) { return lanes.TokenDigest(cfg.token), nil }
	for line := range bytes.SplitSeq(bytes.TrimSpace(spooled), []byte("\n")) {
		event, err := lanes.OpenHook(line, digestOf)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return out.String(), events
}

// decision reads a PreToolUse answer, which must be one JSON object.
func decision(t *testing.T, out string) (string, string) {
	t.Helper()
	var answer struct {
		Specific struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatalf("the hook printed %q, which is not one JSON object: %v", out, err)
	}
	return answer.Specific.Decision, answer.Specific.Reason
}

// Once a policy hook has its verdict, its deadline owes nothing: a pass stays a pass, and a denial or
// a deferral is the one answer the agent CLI reads. The event reaches the spool once, as the verdict
// left it.
func TestADeadlineAfterTheVerdictAddsNoAnswer(t *testing.T) {
	for name, call := range map[string]struct {
		input, decision, reason, detail string
		asked                           int
	}{
		"pass":  {input: `{"tool_name":"Bash","tool_input":{"command":"git status"}}`},
		"deny":  {input: `{"tool_name":"Bash","tool_input":{"command":"git push --force origin main"}}`, decision: "deny", reason: "a force push is denied", detail: lanes.DetailDenied},
		"defer": {input: askUserInput, decision: "defer", asked: 1},
	} {
		t.Run(name, func(t *testing.T) {
			out, spooled := pastTheDeadline(t, call.input, true)
			if len(spooled) != 1 || spooled[0].Detail != call.detail || len(spooled[0].Asked) != call.asked {
				t.Errorf("the spool holds %+v", spooled)
			}
			if call.decision == "" {
				if out != "" {
					t.Errorf("a call the policy passed was answered %q", out)
				}
				return
			}
			if got, reason := decision(t, out); got != call.decision || reason != call.reason {
				t.Errorf("the hook answered %q, and the policy reached %s", out, call.decision)
			}
		})
	}
}

// A policy hook fails closed: a deadline that comes before its verdict denies the call, and the event
// reaches the spool once, as a refused call's.
func TestADeadlineBeforeTheVerdictDenies(t *testing.T) {
	out, spooled := pastTheDeadline(t, `{"tool_name":"Bash","tool_input":{"command":"git status"}}`, false)
	if got, reason := decision(t, out); got != "deny" || reason != lateVerdict {
		t.Errorf("a hook with no verdict at its deadline answered %q", out)
	}
	if len(spooled) != 1 || spooled[0].Detail != lanes.DetailDenied {
		t.Errorf("the spool holds %+v", spooled)
	}
}
