package claudecode

import (
	"encoding/json"
	"os"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// FR-LANE-19: the policy a generation's hook reads holds every push and gate in both run modes and every permission
// mode, and the settings keep the sandbox from approving a command ahead of the hook's ask.
func TestTheGeneratedPolicyHoldsPushesAndGatesInEveryMode(t *testing.T) {
	for _, run := range []string{ports.RunModeHeadless, ports.RunModeInteractive} {
		for _, mode := range []string{"plan", "dontAsk", "default", "acceptEdits", "auto"} {
			t.Run(run+"/"+mode, func(t *testing.T) {
				spec := policySpec(t)
				spec.RunMode, spec.PermissionMode, spec.Gates = run, mode, []string{"Bash(git tag *)"}
				if err := writeGenerated(spec); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(policyPath(spec))
				if err != nil {
					t.Fatal(err)
				}
				var policy lanes.DenyPolicy
				if err := json.Unmarshal(raw, &policy); err != nil {
					t.Fatal(err)
				}
				for command, want := range map[string]bool{
					"git push origin lane/x": true, "git -C . push origin lane/x": true, "env git push": true, "/usr/bin/git push": true,
					"sh -c 'git push'": true, "git -C . tag v1": true, "git tag v1": true, "$GIT push": true,
					"go test ./...": false, "go build ./...": false, "git add -A && git commit -m work": false,
				} {
					call := lanes.ToolCall{Tool: lanes.ToolBash, Command: command, Cwd: spec.Dir}
					if reason, denied := policy.Decide(call); denied {
						t.Errorf("%q denied: %s", command, reason)
					}
					if _, asks := policy.Asks(call); asks != want {
						t.Errorf("%q asks %v, want %v", command, asks, want)
					}
				}
				tag := lanes.ToolCall{Tool: lanes.ToolBash, Command: "git tag v1"}
				if got, want := policy.Defers(tag), run == ports.RunModeHeadless; got != want {
					t.Errorf("a gate defers %v, want %v", got, want)
				}
				settings, err := json.Marshal(generateSettings(spec))
				if err != nil {
					t.Fatal(err)
				}
				var written struct {
					Sandbox struct {
						AutoAllow *bool `json:"autoAllowBashIfSandboxed"`
					} `json:"sandbox"`
				}
				if err := json.Unmarshal(settings, &written); err != nil {
					t.Fatal(err)
				}
				if written.Sandbox.AutoAllow == nil || *written.Sandbox.AutoAllow {
					t.Error("the sandbox approves commands ahead of the hook's ask")
				}
			})
		}
	}
}
