package hook

import (
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// hookDecision is what a PreToolUse answer decides, and empty for a pass.
func hookDecision(t *testing.T, answer []byte) string {
	t.Helper()
	if len(answer) == 0 {
		return ""
	}
	var out struct {
		Specific struct {
			Decision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(answer, &out); err != nil {
		t.Fatal(err)
	}
	return out.Specific.Decision
}

// FR-LANE-19: the hook asks for a push and for a gated command, and denies what the lane may never do, in both run
// modes and every permission mode, however the command is written. An ordinary command is left to the mode.
func TestPushesAndGatesHoldInEveryMode(t *testing.T) {
	tests := []struct{ tool, command, want string }{
		{"Bash", "git push origin lane/x", "ask"},
		{"Bash", "git -C . push origin lane/x", "ask"},
		{"Bash", "env git push", "ask"},
		{"Bash", "/usr/bin/git push", "ask"},
		{"Bash", "sh -c 'git push'", "ask"},
		{"Bash", "git -C . tag v1", "ask"},
		{"Bash", "git tag v1", "ask"},
		{"Bash", "$GIT push", "ask"},
		{"Bash", "echo git push | sh", "ask"},
		{"Monitor", "git -C . push origin lane/x", "ask"},
		{"Bash", "git -C . push --force origin lane/x", "deny"},
		{"Bash", "timeout 60 git push origin HEAD:main", "deny"},
		{"Bash", "go test ./...", ""},
		{"Bash", "go build ./... && npm run build", ""},
		{"Bash", "git add -A && git commit -m 'fix the push'", ""},
		{"Bash", "git status --short", ""},
	}
	for _, askOnly := range []bool{false, true} {
		run := "headless"
		if askOnly {
			run = "interactive"
		}
		path := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{
			Worktree: "/work/lane-1", DefaultBranches: []string{"main"},
			Gates: []lanes.GateRule{lanes.ReadGate("Bash(git tag *)")}, GatesAskOnly: askOnly,
		}))
		for _, mode := range []string{"plan", "dontAsk", "default", "acceptEdits", "auto"} {
			for _, tt := range tests {
				t.Run(run+"/"+mode+"/"+tt.tool+"/"+tt.command, func(t *testing.T) {
					input, err := json.Marshal(map[string]any{
						"tool_name": tt.tool, "tool_use_id": "toolu_1", "cwd": "/work/lane-1", "permission_mode": mode,
						"tool_input": map[string]string{"command": tt.command},
					})
					if err != nil {
						t.Fatal(err)
					}
					if _, answer := decide(config{event: eventPreToolUse, policy: path}, input); hookDecision(t, answer) != tt.want {
						t.Errorf("answered %q, want %q", answer, tt.want)
					}
				})
			}
		}
	}
}

// A gated call defers only where a deferral stands for a request and an ignored one still meets the ask rule. Every
// other answer of the worker leaves the call to ask.
func TestAGatedCallThatDoesNotDeferAsks(t *testing.T) {
	policy := lanes.DenyPolicy{Gates: []lanes.GateRule{lanes.ReadGate("Bash(git tag *)")}}
	pane := policy
	pane.GatesAskOnly = true
	answered := func(decision string) lanes.Reply {
		return lanes.Reply{OK: true, Approval: &lanes.ApprovalReply{Decision: decision, Reason: "ropeshift policy: no"}}
	}
	tests := []struct {
		name    string
		policy  lanes.DenyPolicy
		command string
		reply   lanes.Reply
		want    string
		looks   int
	}{
		{"a request stands", policy, "git tag v1", answered(lanes.ApprovalWaiting), "defer", 1},
		{"the worker refuses", policy, "git tag v1", answered(lanes.ApprovalDeny), "deny", 1},
		{"a member approved", policy, "git tag v1", answered(lanes.ApprovalGoesOn), "ask", 1},
		{"the worker cannot say", policy, "git tag v1", lanes.Reply{Code: lanes.CodeUnavailable}, "ask", 1},
		{"another form of the gate", policy, "git -C . tag v1", answered(lanes.ApprovalWaiting), "ask", 0},
		{"a pane", pane, "git tag v1", answered(lanes.ApprovalWaiting), "ask", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socket, worker := aScriptedWorker(t, tt.reply)
			cfg := config{event: eventPreToolUse, socket: socket, token: "a-token", policy: policyFile(t, tt.policy), raised: 100 * hardDeadline}
			_, answer := decide(cfg, bashInput(t, "/work/lane-1", tt.command))
			if hookDecision(t, answer) != tt.want || len(worker.taken()) != tt.looks {
				t.Errorf("answered %q after %d looks, want %q after %d", answer, len(worker.taken()), tt.want, tt.looks)
			}
		})
	}
}
