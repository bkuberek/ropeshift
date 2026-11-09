package hook

import (
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
// the hook printed.
func pastTheDeadline(t *testing.T, input string, decides bool) string {
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
	failClosed(cfg, &out)
	if decides {
		run(cfg, event, taken, &out)
	}
	cfg.out.settle(cfg)
	return out.String()
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
// a deferral is the one answer the agent CLI reads.
func TestADeadlineAfterTheVerdictAddsNoAnswer(t *testing.T) {
	for name, call := range map[string]struct{ input, decision, reason string }{
		"pass":  {input: `{"tool_name":"Bash","tool_input":{"command":"git status"}}`},
		"deny":  {input: `{"tool_name":"Bash","tool_input":{"command":"git push --force origin main"}}`, decision: "deny", reason: "a force push is denied"},
		"defer": {input: askUserInput, decision: "defer"},
	} {
		t.Run(name, func(t *testing.T) {
			out := pastTheDeadline(t, call.input, true)
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

// A policy hook fails closed: a deadline that comes before its verdict denies the call.
func TestADeadlineBeforeTheVerdictDenies(t *testing.T) {
	out := pastTheDeadline(t, `{"tool_name":"Bash","tool_input":{"command":"git status"}}`, false)
	if got, reason := decision(t, out); got != "deny" || reason != lateVerdict {
		t.Errorf("a hook with no verdict at its deadline answered %q", out)
	}
}
