package hook

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
)

func bashInput(t *testing.T, cwd, command string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"tool_name": "Bash", "tool_use_id": "toolu_1", "cwd": cwd, "tool_input": map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// FR-SPEND-2: the hook reads the wait rules from the generation's policy file and denies a match, with no
// worker to ask. The call is never run, and the reason names the rule and the way on.
func TestTheHookDeniesAWaitWithTheWorkerStopped(t *testing.T) {
	rules := lanes.DefaultWaitRules()
	policy := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{Worktree: "/work/lane-1", Waits: &rules}))
	cfg := config{event: eventPreToolUse, policy: policy, socket: filepath.Join(t.TempDir(), "gone.sock")}
	for command, rule := range map[string]string{
		"sleep 300": "W1", "while true; do gh pr checks 12; sleep 30; done": "W2",
		"gh run watch": "W3", "gh pr checks --watch": "W3", "gh pr view 12": "W4",
	} {
		start := time.Now()
		verdict, answer := decide(cfg, bashInput(t, "/work/lane-1", command))
		if verdict != callDenied || !strings.Contains(string(answer), `"permissionDecision":"deny"`) || !strings.Contains(string(answer), rule+":") {
			t.Errorf("%q: %v %s", command, verdict, answer)
		}
		if took := time.Since(start); took > 50*time.Millisecond {
			t.Errorf("%q: the hook took %s", command, took)
		}
	}
	for _, command := range []string{"sleep 5", "go test ./...", "make build"} {
		if verdict, answer := decide(cfg, bashInput(t, "/work/lane-1", command)); verdict == callDenied {
			t.Errorf("%q was denied: %s", command, answer)
		}
	}
}

// The worker hears which rule denied a call, and never the command: the event carries the rule's ID.
func TestAWaitDenialReachesTheWorkerAsTheRuleAndNotTheCommand(t *testing.T) {
	rules := lanes.DefaultWaitRules()
	policy := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{Worktree: "/work/lane-1", Waits: &rules}))
	event, _ := forwarded(t, config{event: eventPreToolUse, policy: policy}, string(bashInput(t, "/work/lane-1", "gh run watch sentinel-xyz")))
	if event.Detail != lanes.DetailDenied || event.WaitRule != "W3" {
		t.Errorf("the worker heard %+v", event)
	}
	raw, err := json.Marshal(event)
	if err != nil || strings.Contains(string(raw), "sentinel-xyz") {
		t.Errorf("the event holds the command: %s, %v", raw, err)
	}
	other, _ := forwarded(t, config{event: eventPreToolUse, policy: policy}, string(bashInput(t, "/work/lane-1", "git push --force origin lane/one")))
	if other.Detail != lanes.DetailDenied || other.WaitRule != "" {
		t.Errorf("a denial for another reason reads as a wait: %+v", other)
	}
}
