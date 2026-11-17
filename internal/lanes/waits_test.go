package lanes

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
)

func waitPolicy(rules WaitRules) DenyPolicy {
	return CompilePolicy(PolicySpec{Worktree: "/work/app-lanes/lane-1", Waits: &rules})
}

// A rule is told apart from a long build by what the command does and never by how long it runs. Each rule has
// its positives, its compound and quoted forms, and the legitimate commands that must pass.
func TestTheWaitRulesClassifyWhatACommandDoes(t *testing.T) {
	tests := []struct {
		name    string
		command string
		rule    string
	}{
		{"a short sleep", "sleep 5", ""},
		{"a sleep one second short of the bound", "sleep 59", ""},
		{"a fractional sleep", "sleep 0.5", ""},
		{"a sleep with a unit", "sleep 5s", ""},
		{"a sleep with no operand", "sleep", ""},
		{"a test run", "go test ./...", ""},
		{"a build", "make build", ""},
		{"a build and a test", "npm run build && npm test", ""},
		{"a bounded test run", "timeout 1800 go test ./...", ""},
		{"git status", "git status", ""},
		{"opening a pull request", "gh pr create --draft --title x --body y", ""},
		{"reading an issue", "gh issue view 12", ""},
		{"reading a pull request's files", "gh api repos/o/r/pulls/12/files", ""},
		{"echoing a sleep", "echo sleep 300", ""},
		{"searching for a sleep", `grep -r "sleep 300" .`, ""},
		{"a commit message naming a watch", `git commit -m "drop sleep 300 and gh pr checks --watch"`, ""},
		{"a file named watch", "cat watch.log", ""},
		{"a test named for a watcher", "go test -run TestWatch ./...", ""},
		{"a script", "./run-tests.sh", ""},
		{"a sleep in another language", `python3 -c "import time; time.sleep(300)"`, ""},
		{"a loop with no sleep", "for i in 1 2 3; do echo $i; done", ""},
		{"a loop that runs a test per file", "for f in a b; do go test ./$f; done", ""},
		{"a read loop", "while read line; do echo $line; done < input.txt", ""},
		{"a short sleep after a loop", "for i in 1; do echo $i; done; sleep 5", ""},
		{"a search for a loop", `rg "while true" src`, ""},

		{"a sleep at the bound", "sleep 60", "W1"},
		{"a sleep of five minutes", "sleep 300", "W1"},
		{"a sleep in minutes", "sleep 1m", "W1"},
		{"a sleep in hours", "sleep 2h", "W1"},
		{"a sleep in days", "sleep 1d", "W1"},
		{"a sleep with no end", "sleep infinity", "W1"},
		{"two operands add up", "sleep 30 30", "W1"},
		{"a sleep after a test", "go test ./... ; sleep 300", "W1"},
		{"a sleep before an echo", "sleep 300 && echo done", "W1"},
		{"a sleep in a group", "(sleep 300)", "W1"},
		{"a sleep under a timeout", "timeout 400 sleep 300", "W1"},
		{"a sleep under an environment", "env FOO=1 sleep 90", "W1"},
		{"a sleep under sudo", "sudo sleep 120", "W1"},
		{"a sleep in a substitution", "echo $(sleep 300)", "W1"},
		{"a sleep in a shell argument", "sh -c 'sleep 300'", "W1"},
		{"a sleep after a build in a shell argument", `bash -c "make build; sleep 120"`, "W1"},
		{"a sleep of a length the hook cannot read", "sleep $DELAY", "W1"},
		{"a quoted sleep command", `"sleep" 300`, "W1"},

		{"a poll of CI in a loop", "while true; do gh pr checks 12; sleep 30; done", "W2"},
		{"a wait for a file", "until [ -f done ]; do sleep 2; done", "W2"},
		{"a counted loop", "for i in 1 2 3; do sleep 1; done", "W2"},
		{"a loop with a short sleep", "while true; do sleep 0.1; done", "W2"},
		{"a retry of a health check", "for i in $(seq 10); do curl -s localhost:8080/health && break; sleep 3; done", "W2"},
		{"a loop over lines", "while :; do\n  sleep 5\ndone", "W2"},
		{"a wait for a port", "while ! curl -sf localhost:9000; do sleep 1; done", "W2"},
		{"a loop in a shell argument", "bash -c 'while true; do sleep 5; done'", "W2"},
		{"a shell in a loop", "while true; do bash -c 'sleep 5'; done", "W2"},
		{"a sleep as the condition", "while sleep 1; do echo x; done", "W2"},
		{"a nested loop", "for a in 1; do for b in 2; do sleep 1; done; done", "W2"},

		{"a watch", "watch -n 5 ls", "W3"},
		{"a watch of a cluster", "watch kubectl get pods", "W3"},
		{"a run watch", "gh run watch", "W3"},
		{"a run watch of one run", "gh run watch 123 --exit-status", "W3"},
		{"a checks watch", "gh pr checks --watch", "W3"},
		{"a checks watch with an interval", "gh pr checks 12 --watch --interval 10", "W3"},
		{"a checks watch by its short flag", "gh pr checks -w", "W3"},
		{"a run watch before an echo", "gh run watch 99 && echo done", "W3"},

		{"a read of checks", "gh pr checks", "W4"},
		{"a read of one pull request's checks", "gh pr checks 12", "W4"},
		{"a view of a pull request", "gh pr view 12", "W4"},
		{"a view of a pull request's rollup", "gh pr view 12 --json statusCheckRollup", "W4"},
		{"a view of a run", "gh run view 123", "W4"},
		{"a list of runs", "gh run list --limit 3", "W4"},
		{"an api read of check runs", "gh api repos/o/r/commits/abc/check-runs", "W4"},
		{"an api read of a check suite", "gh api repos/o/r/check-suites/9", "W4"},
		{"an api read of a run", "gh api /repos/o/r/actions/runs/9", "W4"},
		{"an api read of a status", "gh api repos/o/r/commits/abc/status", "W4"},
		{"an api read with a header", `gh api -H "Accept: application/json" repos/o/r/actions/runs`, "W4"},
		{"a read under a token", "GH_TOKEN=x gh pr view 4", "W4"},
		{"a read after a build", "make build && gh run list", "W4"},
	}
	policy := waitPolicy(DefaultWaitRules())
	if len(tests) < 40 {
		t.Fatalf("the table holds %d commands", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, rule, denied := policy.DecideRule(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: "/work/app-lanes/lane-1"})
			if denied != (tt.rule != "") || rule != tt.rule {
				t.Fatalf("%q: rule %q, denied %v (%s), want %q", tt.command, rule, denied, reason, tt.rule)
			}
			if tt.rule != "" && !strings.Contains(reason, tt.rule+":") {
				t.Errorf("%q: the reason reads %q", tt.command, reason)
			}
		})
	}
}

// A denial names the rule and the way to proceed, and never the command (FR-OBS-5).
func TestAWaitDenialNamesTheRuleAndNeverTheCommand(t *testing.T) {
	policy := waitPolicy(DefaultWaitRules())
	want := map[string]string{"W1": "lane_wait", "W2": "lane_report", "W3": "lane_report", "W4": "lane_report"}
	for command, rule := range map[string]string{
		"sleep 300 --sentinel-xyz": "W1", "while true; do sleep 1; done # sentinel-xyz": "W2",
		"gh run watch sentinel-xyz": "W3", "gh pr view sentinel-xyz": "W4",
	} {
		reason, got, denied := policy.DecideRule(ToolCall{Tool: ToolBash, Command: command})
		if !denied || got != rule {
			t.Fatalf("%q: rule %q, denied %v", command, got, denied)
		}
		if strings.Contains(reason, "sentinel-xyz") {
			t.Errorf("the reason for %s holds the command: %q", rule, reason)
		}
		if !strings.HasPrefix(reason, "ropeshift policy: "+rule+":") || !strings.Contains(reason, want[rule]) {
			t.Errorf("the reason for %s reads %q", rule, reason)
		}
	}
}

// A command that matches more than one rule is denied under the first of them that is on, so turning a
// rule off never hides a deeper one.
func TestACommandThatMatchesTwoRulesIsDeniedUnderTheFirstThatIsOn(t *testing.T) {
	const command = "while true; do gh pr checks 12; sleep 30; done"
	call := ToolCall{Tool: ToolBash, Command: command}
	rules := DefaultWaitRules()
	if _, rule, _ := waitPolicy(rules).DecideRule(call); rule != "W2" {
		t.Errorf("rule %q", rule)
	}
	rules.Off = []string{"W2"}
	if _, rule, _ := waitPolicy(rules).DecideRule(call); rule != "W4" {
		t.Errorf("with W2 off, rule %q", rule)
	}
	rules.Off = []string{"W2", "W4"}
	if _, _, denied := waitPolicy(rules).DecideRule(call); denied {
		t.Error("with W2 and W4 off, the loop was denied")
	}
}

// A project's policy moves what each rule matches and turns a rule off, and nothing else does.
func TestAPolicyVersionMovesTheWaitRules(t *testing.T) {
	rules := WaitRules{
		Version: WaitRulesVersion, MaxToolSleepMs: (10 * time.Minute).Milliseconds(), Off: []string{"W4"},
		Patterns: map[string][]string{"W3": {"kubectl rollout status"}},
	}
	policy := waitPolicy(rules)
	for command, want := range map[string]string{
		"sleep 300":                              "",
		"sleep 600":                              "W1",
		"gh pr view 12":                          "",
		"gh pr checks 12":                        "",
		"kubectl rollout status deploy/x":        "W3",
		"sudo kubectl rollout status deploy/x":   "W3",
		"kubectl get pods":                       "",
		"kubectl rollout statusx":                "",
		"make build && kubectl rollout status x": "W3",
	} {
		if _, rule, _ := policy.DecideRule(ToolCall{Tool: ToolBash, Command: command}); rule != want {
			t.Errorf("%q: rule %q, want %q", command, rule, want)
		}
	}
}

// A call that is no shell command reads no wait rule, and a denial for another reason names none.
func TestADenialForAnotherReasonNamesNoWaitRule(t *testing.T) {
	policy := waitPolicy(DefaultWaitRules())
	if _, rule, denied := policy.DecideRule(ToolCall{Tool: ToolBash, Command: "git push --force origin lane/one", Cwd: "/work/app-lanes/lane-1"}); !denied || rule != "" {
		t.Errorf("a force push: rule %q, denied %v", rule, denied)
	}
	if _, _, denied := policy.DecideRule(ToolCall{Tool: "Read", Path: "/work/app-lanes/lane-1/sleep"}); denied {
		t.Error("a file read was denied")
	}
	if _, _, denied := (testPolicy()).DecideRule(ToolCall{Tool: ToolBash, Command: "sleep 300"}); denied {
		t.Error("a policy with no wait rules denied a sleep")
	}
}

// The hook reads the compiled file, so the rules and the settings under them survive being written.
func TestTheWaitRulesSurviveTheCompiledPolicyFile(t *testing.T) {
	rules := WaitRules{Version: WaitRulesVersion, MaxToolSleepMs: 90_000, Off: []string{"W2"}, Patterns: map[string][]string{"W1": {"nap"}}}
	raw, err := json.Marshal(waitPolicy(rules))
	if err != nil {
		t.Fatal(err)
	}
	var read DenyPolicy
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if read.Waits == nil || read.Waits.MaxToolSleepMs != 90_000 || !slices.Equal(read.Waits.Off, []string{"W2"}) || read.Waits.Version != WaitRulesVersion {
		t.Errorf("the policy file reads %+v", read.Waits)
	}
	if _, rule, _ := read.DecideRule(ToolCall{Tool: ToolBash, Command: "nap 5"}); rule != "W1" {
		t.Errorf("the pattern read back matches rule %q", rule)
	}
}

// The defaults are the design's values, and a lease that carries no spend policy gets them.
func TestTheWaitDefaultsAreTheDesignsValues(t *testing.T) {
	rules := DefaultWaitRules()
	if rules.Version != WaitRulesVersion || rules.MaxToolSleepMs != 60_000 || len(rules.Off) != 0 {
		t.Errorf("defaults %+v", rules)
	}
	if got := WaitRulesFrom(nil); got.MaxToolSleepMs != rules.MaxToolSleepMs || len(got.Off) != 0 || len(got.Patterns) != 0 {
		t.Errorf("no spend policy reads %+v", got)
	}
	if got := MaxLiveWait(nil); got != 5*time.Minute {
		t.Errorf("no spend policy gives %s", got)
	}
	spend := &ledger.DesiredSpend{
		Version: 3, MaxLiveWaitMs: (10 * time.Minute).Milliseconds(), MaxToolSleepMs: 30_000, WaitRulesOff: []string{"W4"},
		WaitRulePatterns: map[string][]string{"W3": {"kubectl rollout status"}},
	}
	got := WaitRulesFrom(spend)
	if got.MaxToolSleepMs != 30_000 || !slices.Equal(got.Off, []string{"W4"}) || got.Patterns["W3"][0] != "kubectl rollout status" {
		t.Errorf("a spend policy reads %+v", got)
	}
	if MaxLiveWait(spend) != 10*time.Minute {
		t.Errorf("a spend policy gives %s", MaxLiveWait(spend))
	}
}

// A wait at or under the bound stays live, and anything longer parks.
func TestAWaitPastTheBoundParks(t *testing.T) {
	bound := 5 * time.Minute
	for minutes, want := range map[int64]bool{1: false, 5: false, 6: true, 30: true} {
		if got := ParksOnWait(minutes, bound); got != want {
			t.Errorf("%d minutes: parks %v, want %v", minutes, got, want)
		}
	}
	if ParksOnWait(9, 10*time.Minute) || !ParksOnWait(11, 10*time.Minute) {
		t.Error("the bound does not follow the policy")
	}
}

// The wake of a long wait is a timer and a person, whichever comes first.
func TestAWaitsWakeIsATimerOrAPerson(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	expr, err := parking.Parse(WaitWake(at), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if expr.Holds(parking.Facts{}, at-1) {
		t.Error("the wake holds before its time")
	}
	if !expr.Holds(parking.Facts{}, at) {
		t.Error("the wake does not hold at its time")
	}
	if !expr.Holds(parking.Facts{{Kind: parking.HumanResume, Key: "lane-1"}: true}, at-1000) {
		t.Error("a person's resume does not end the wait")
	}
	if next, ok := expr.NextTime(at - 5); !ok || next != at {
		t.Errorf("the next evaluation is %d, %v", next, ok)
	}
}
