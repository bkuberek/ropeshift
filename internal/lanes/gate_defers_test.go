package lanes

import "testing"

func TestDefersReadsAGateWildcardAsTheAskRuleDoes(t *testing.T) {
	cases := []struct {
		gate, command string
		defers        bool
	}{
		{"Bash(touch gated*)", "touch gated-call-ran", true},
		{"Bash(touch gated*)", "touch gated", true},
		{"Bash(touch gated*)", "touch other", false},
		{"Bash(git tag *)", "git tag v1", true},
		{"Bash(git tag *)", "git tagger", false},
		{"Bash(git tag:*)", "git tag v1", true},
		{"Bash(git tag:*)", "git tagger", false},
		{"Bash(git tag v1)", "git tag v1", true},
		{"Bash(git tag v1)", "git tag v1 extra", false},
	}
	for _, tc := range cases {
		policy := CompilePolicy(PolicySpec{
			Worktree: "/work/app-lanes/lane-1", Home: "/home/person", DefaultBranches: []string{"main"},
			Writable: []string{"/run/t/lane-1"}, Gates: []GateRule{ReadGate(tc.gate)},
		})
		if got := policy.Defers(shellCall(tc.command)); got != tc.defers {
			t.Errorf("gate %q, command %q: Defers = %v, want %v", tc.gate, tc.command, got, tc.defers)
		}
	}
}
