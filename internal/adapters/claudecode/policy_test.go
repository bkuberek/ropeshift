package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

func policySpec(t *testing.T) ports.SessionSpec {
	t.Helper()
	root := t.TempDir()
	worktree := filepath.Join(root, "repository-lanes", "lane-1")
	for _, dir := range []string{worktree, filepath.Join(root, "repository-lanes", "lane-2"), filepath.Join(root, "repository")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return ports.SessionSpec{
		Lane: "lane-1", Generation: 1, Dir: worktree, ConfigDir: filepath.Join(root, "generated"), Binary: "/bin/ropeshift",
		PermissionMode: "default", DefaultBranch: "trunk", Env: []string{"HOME=/home/person"},
	}
}

func TestEveryDenialIsADenyRuleToo(t *testing.T) {
	rules := generateSettings(policySpec(t)).Permissions.Deny
	for _, rule := range []string{
		"Bash(git push --force*)", "Bash(git push -f*)", "Bash(git push * main)", "Bash(git push * *:main)",
		"Bash(git push * master)", "Bash(git push * trunk)", "Bash(git push * *:trunk)",
	} {
		if !slices.Contains(rules, rule) {
			t.Errorf("no rule %s in %v", rule, rules[:8])
		}
	}
}

// A deny rule written through a link also applies at the link's real location, so a rule
// on a sibling that leads back to the worktree denies every write in it. That is how `/tmp`
// reads on macOS, where it is a link to `/private/tmp`.
func TestEditRulesNameNoLinkToTheWorktree(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(root, "private", "work", "lane-1")
	beside := filepath.Join(root, "private", "work", "lane-2")
	for _, dir := range []string{filepath.Join(worktree, "src"), beside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"above": filepath.Join(root, "private"), "same": worktree, "inside": filepath.Join(worktree, "src"), "beside": beside,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	rules := editRules(worktree)
	for _, name := range []string{"above", "same", "inside"} {
		for _, rule := range rules {
			if strings.Contains(rule, "/"+name+")") || strings.Contains(rule, "/"+name+"/") {
				t.Errorf("a rule names the worktree by another path: %s", rule)
			}
		}
	}
	if !slices.Contains(rules, "Edit(/"+filepath.Join(root, "beside")+"/**)") {
		t.Errorf("a link that leads elsewhere lost its rule: %v", rules[:4])
	}
}

func TestEditRulesHoldEverythingBesideTheWorktree(t *testing.T) {
	spec := policySpec(t)
	real, err := filepath.EvalSymlinks(spec.Dir)
	if err != nil {
		t.Fatal(err)
	}
	rules := editRules(spec.Dir)
	lanesDir := filepath.Dir(real)
	if !slices.Contains(rules, "Edit(/"+filepath.Join(lanesDir, "lane-2")+"/**)") {
		t.Errorf("another lane is not denied: %v", rules[:4])
	}
	if !slices.Contains(rules, "Edit(/"+filepath.Join(filepath.Dir(lanesDir), "repository")+"/**)") {
		t.Errorf("the repository is not denied")
	}
	for _, rule := range rules {
		if strings.HasPrefix(rule, "Edit(/"+real) {
			t.Errorf("a rule covers the lane's own worktree: %s", rule)
		}
	}
}

func TestGatesBecomeAskRulesOnlyWhereACallWouldNotPrompt(t *testing.T) {
	tests := []struct {
		mode string
		want []string
	}{
		{"default", nil},
		{"acceptEdits", nil},
		{"auto", []string{"Bash(git tag *)"}},
		{"bypassPermissions", []string{"Bash(git tag *)"}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			spec := policySpec(t)
			spec.PermissionMode, spec.Gates = tt.mode, []string{"Bash(git tag *)"}
			if got := generateSettings(spec).Permissions.Ask; !slices.Equal(got, tt.want) {
				t.Errorf("ask %v", got)
			}
		})
	}
}

func TestPreToolUseFailsClosed(t *testing.T) {
	group := generateSettings(policySpec(t)).Hooks["PreToolUse"]
	if len(group) != 1 || group[0].Hooks[0].OnFailure != "block" {
		t.Errorf("hooks %+v", group)
	}
}

func TestWrittenPolicyGuardsTheWorktreeAndTheBranches(t *testing.T) {
	spec := policySpec(t)
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
	if !slices.Contains(policy.DefaultBranches, "trunk") || !slices.Contains(policy.Roots, spec.Dir) {
		t.Errorf("policy %+v", policy)
	}
	if _, denied := policy.Decide(lanes.ToolCall{Tool: "Write", Path: "/etc/hosts", Cwd: spec.Dir}); !denied {
		t.Error("a write outside passed")
	}
	if _, denied := policy.Decide(lanes.ToolCall{Tool: "Write", Path: filepath.Join(spec.Dir, "a"), Cwd: spec.Dir}); denied {
		t.Error("a write inside was denied")
	}
}
