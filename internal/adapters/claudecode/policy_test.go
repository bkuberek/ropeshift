package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
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

// TestTheQuestionToolFollowsTheRunMode: only `-p` honors `defer`, so a pane is denied.
func TestTheQuestionToolFollowsTheRunMode(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{ports.RunModeHeadless, lanes.QuestionToolDefer},
		{ports.RunModeInteractive, lanes.QuestionToolDeny},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			spec := policySpec(t)
			spec.RunMode = tt.mode
			if got := compilePolicy(spec).QuestionTool; got != tt.want {
				t.Errorf("question tool %q", got)
			}
		})
	}
}

// A planning lane's person is the one who answers, so while they are attached the question tool is left to
// the terminal. Only a planning lane gets that: any other pane, and a planning spec that somehow runs
// headless, keep what the run mode gives them.
func TestAPlanningPanesQuestionToolIsLeftToAnAttachedTerminal(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		planning bool
		want     string
	}{
		{"a planning lane in a pane", ports.RunModeInteractive, true, lanes.QuestionToolTerminalIfAttached},
		{"another lane in a pane", ports.RunModeInteractive, false, lanes.QuestionToolDeny},
		{"a planning spec run headless, which only `-p` can defer", ports.RunModeHeadless, true, lanes.QuestionToolDefer},
		{"another lane headless", ports.RunModeHeadless, false, lanes.QuestionToolDefer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := policySpec(t)
			spec.RunMode, spec.Planning = tt.mode, tt.planning
			policy := compilePolicy(spec)
			if policy.QuestionTool != tt.want {
				t.Errorf("question tool %q, want %q", policy.QuestionTool, tt.want)
			}
			if tt.want == lanes.QuestionToolTerminalIfAttached && policy.AttachedMarker == "" {
				t.Error("a policy that leaves the tool to an attached terminal names no marker to read")
			}
		})
	}
}

// A pane's hook reads the marker the worker touches while a person is attached, and a headless session has
// no pane for anyone to attach to.
func TestAPanesPolicyNamesItsAttachedMarker(t *testing.T) {
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	spec.Generation = 3
	want := filepath.Join(spec.ConfigDir, "attached-3")
	if got := compilePolicy(spec).AttachedMarker; got != want {
		t.Errorf("marker %q, want %q", got, want)
	}
	spec.RunMode = ports.RunModeHeadless
	if got := compilePolicy(spec).AttachedMarker; got != "" {
		t.Errorf("a headless session names marker %q", got)
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

// ADR-0023: the branch a lane continues is kept from every push the way a default branch is, in the CLI's
// own rules and in the hook's policy.
func TestTheBranchALaneContinuesIsKeptFromEveryPush(t *testing.T) {
	spec := policySpec(t)
	spec.Kept = []string{"feature/retry"}
	rules := generateSettings(spec).Permissions.Deny
	for _, rule := range []string{"Bash(git push * feature/retry)", "Bash(git push * *:feature/retry)", "Bash(git push * +feature/retry)"} {
		if !slices.Contains(rules, rule) {
			t.Errorf("no rule %s", rule)
		}
	}
	if policy := compilePolicy(spec); !slices.Equal(policy.Kept, []string{"feature/retry"}) {
		t.Errorf("the hook's policy keeps %v", policy.Kept)
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

// FR-LANE-19: every action a profile gates asks, whatever the mode, because an ask rule beats an allow
// rule and holds in a mode that asks nothing, and a push asks on every lane that writes. A rule that asks
// for a call that would ask anyway changes nothing, so no mode is left to reason about.
func TestEveryGateAndEveryPushAsksInEveryMode(t *testing.T) {
	for _, mode := range []string{"plan", "default", "acceptEdits", "dontAsk", "auto"} {
		t.Run(mode, func(t *testing.T) {
			spec := policySpec(t)
			spec.PermissionMode, spec.Gates = mode, []string{"Bash(git tag *)", "Bash(git push *)"}
			if got := generateSettings(spec).Permissions.Ask; !slices.Equal(got, []string{"Bash(git push *)", "Bash(git tag *)"}) {
				t.Errorf("ask %v", got)
			}
		})
	}
	for name, spec := range map[string]func(*ports.SessionSpec){
		"a review lane":   func(spec *ports.SessionSpec) { spec.ReadOnly = true },
		"an orchestrator": func(spec *ports.SessionSpec) { spec.OnlyTools = true },
	} {
		t.Run(name, func(t *testing.T) {
			session := policySpec(t)
			spec(&session)
			session.Gates = []string{"mcp__ropeshift__lane_report"}
			if got := generateSettings(session).Permissions.Ask; !slices.Equal(got, session.Gates) {
				t.Errorf("ask %v", got)
			}
		})
	}
}

// A rule covers a command as the CLI's documented matching does: `Bash(git add *)` is that prefix at
// a word's end, and a compound command runs with no prompt only if a rule covers every part.
func ruleCovers(rule, part string) bool {
	body, ok := strings.CutPrefix(rule, "Bash(")
	if !ok {
		return false
	}
	body = strings.TrimSuffix(body, ")")
	if prefix, trailing := strings.CutSuffix(body, " *"); trailing {
		return part == prefix || strings.HasPrefix(part, prefix+" ")
	}
	return part == body
}

func runsWithNoPrompt(rules []string, command string) bool {
	for part := range strings.SplitSeq(strings.NewReplacer(";", "&&", "|", "&&", "\n", "&&").Replace(command), "&&") {
		part = strings.TrimSpace(part)
		if !slices.ContainsFunc(rules, func(rule string) bool { return ruleCovers(rule, part) }) {
			return false
		}
	}
	return true
}

// A lane with nobody there would wait out `approval_wait` on its own work, so a stage and a commit
// run with no prompt, alone or together.
func TestALaneStagesAndCommitsWithNoPrompt(t *testing.T) {
	spec := policySpec(t)
	spec.Tools = []string{"lane_report"}
	rules := generateSettings(spec).Permissions.Allow
	if !slices.Contains(rules, "mcp__ropeshift__lane_report") {
		t.Errorf("a lane lost its own tool: %v", rules)
	}
	for _, command := range []string{
		"git add plans/retry.md", "git add .", "git commit -m plan", "git add plans && git commit -q -m 'plan the retry budget'",
	} {
		if !runsWithNoPrompt(rules, command) {
			t.Errorf("%q would wait on a member: %v", command, rules)
		}
	}
}

// An orchestrator holds no vendor tools and has no worktree, so it gets its own tools and no more.
func TestAnOrchestratorGetsNoCommitRule(t *testing.T) {
	spec := policySpec(t)
	spec.Tools, spec.OnlyTools = []string{"question_next"}, true
	if got := generateSettings(spec).Permissions.Allow; !slices.Equal(got, []string{"mcp__ropeshift__question_next"}) {
		t.Errorf("allow %v", got)
	}
}

// What a stage and a commit may run with no prompt stops there. Each of these reaches a person or a
// denial: a member's baseline is that the same shape with a stage or a commit does run.
func TestAllowRulesReachNoPushAndNoOtherGitCommand(t *testing.T) {
	spec := policySpec(t)
	rules := generateSettings(spec).Permissions.Allow
	if !runsWithNoPrompt(rules, "git commit -m plan") {
		t.Fatalf("the baseline: a commit prompts, so these prove nothing: %v", rules)
	}
	for _, command := range []string{
		"git push", "git push origin lane/one", "git push --force origin lane/one", "git commit -m plan && git push",
		"git add . ; git push", "git checkout main", "git switch main", "git branch -f main HEAD",
		"git reset --hard main", "git update-ref refs/heads/main HEAD", "git rebase main", "git merge main",
		"git -C ../lane-2 commit -m plan", "git -c core.hooksPath=/tmp/hooks commit -m plan", "git config core.hooksPath /tmp/hooks",
		"GIT_DIR=/work/app/.git git commit -m plan", "cd .. && git commit -m plan",
	} {
		if runsWithNoPrompt(rules, command) {
			t.Errorf("%q would run with no prompt: %v", command, rules)
		}
	}
	for _, rule := range rules {
		if rule == "Bash" || strings.Contains(rule, "push") || strings.HasPrefix(rule, "Bash(*") || strings.HasPrefix(rule, "Bash(git *") {
			t.Errorf("a rule too wide: %s", rule)
		}
	}
}

// The attack on what these rules open, read as a session's hook reads it. A denial here needs no
// worker and no person, so it holds with nobody there.
func TestAnAllowedCommitCarriesNoPushAndWritesNothingOutside(t *testing.T) {
	spec := policySpec(t)
	policy := compilePolicy(spec)
	sibling := filepath.Join(filepath.Dir(spec.Dir), "lane-2")
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{"the baseline, a commit in the worktree", "git add . && git commit -m plan", ""},
		{"a commit and a force push", "git commit -m plan && git push --force", lanes.ReasonForcePush},
		{"a commit and a push", "git commit -m plan && git push", lanes.ReasonPushApart},
		{"a push in a commit's message", `git commit -m "$(git push)"`, lanes.ReasonPushApart},
		{"a commit and a push to the default branch", "git commit -m plan && git push origin trunk", lanes.ReasonDefaultBranch},
		{"a commit in another lane", "cd " + sibling + " && git commit -m plan", lanes.ReasonOutside},
		{"a stage in another lane", "git -C " + sibling + " add .", lanes.ReasonOutside},
		{"a commit in the repository itself", "git --git-dir=" + filepath.Join(filepath.Dir(filepath.Dir(spec.Dir)), "repository", ".git") + " commit -m plan", lanes.ReasonOutside},
		{"a commit that writes beside the worktree", "git commit -m plan > " + filepath.Join(sibling, "file"), lanes.ReasonOutside},
		{"a stage and a delete above the worktree", "git add . && rm -rf ..", lanes.ReasonOutside},
		{"a delete in another lane", "rm -rf " + sibling, lanes.ReasonOutside},
		{"a directory beside the worktree", "mkdir -p ../lane-3", lanes.ReasonOutside},
		{"a directory deleted beside the worktree", "rmdir " + sibling, lanes.ReasonOutside},
		{"a move out of the worktree", "mv notes.md ../notes.md", lanes.ReasonOutside},
		{"a move into the worktree from beside it", "mv " + filepath.Join(sibling, "a") + " a", lanes.ReasonOutside},
		{"a delete of the home directory", "rm -rf ~", lanes.ReasonOutside},
		{"a delete of what a variable names", "rm -rf \"$TARGET\"", lanes.ReasonOutside},
		{"a staged deletion in another lane", "git -C " + sibling + " rm -q a", lanes.ReasonOutside},
		{"a staged rename and a push", "git mv a b && git push", lanes.ReasonPushApart},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(lanes.ToolCall{Tool: lanes.ToolBash, Command: tt.command, Cwd: spec.Dir})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("reason %q denied %v, want %q", reason, denied, tt.denied)
			}
		})
	}
}

// A profile's gate on a commit outranks a rule the driver writes for every lane: ask beats allow, and
// a gate exists because a member wants to read that call.
func TestAGateOnACommitStillAsks(t *testing.T) {
	tests := []struct {
		name string
		gate string
		asks bool
	}{
		{"a gate on every commit", "Bash(git commit *)", true},
		{"a gate on part of a commit", "Bash(git commit --amend *)", true},
		{"a gate on a stage", "Bash(git add *)", true},
		{"a gate on all of git", "Bash(git *)", true},
		{"a gate on every command", "Bash", true},
		{"a gate on a delete", "Bash(rm *)", true},
		{"a gate on a forced delete", "Bash(rm -rf *)", true},
		{"a gate on a file tool", "Write", true},
		{"a gate on every edit", "Edit", true},
		{"a gate on something else", "Bash(git tag *)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := policySpec(t)
			spec.Gates = []string{tt.gate}
			got := generateSettings(spec).Permissions.Ask
			if slices.Contains(got, tt.gate) != tt.asks {
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

// A review lane has no commit rule, so a commit prompts, and a prompt on a lane with nobody there is a
// wait nobody answers. The deny rules answer it first: no editing tool, and no command that writes a
// commit or a ref, in the settings the CLI enforces and in the policy a hook reads.
func TestAReadOnlySessionHoldsNoCommitRuleAndDeniesEveryWrite(t *testing.T) {
	spec := policySpec(t)
	spec.Tools, spec.ReadOnly = []string{"lane_report"}, true
	permissions := generateSettings(spec).Permissions
	if !slices.Contains(permissions.Allow, "mcp__ropeshift__lane_report") {
		t.Errorf("a review lane lost its own tool: %v", permissions.Allow)
	}
	for _, rule := range permissions.Allow {
		if strings.Contains(rule, "git") {
			t.Errorf("a review lane may run with no prompt: %s", rule)
		}
	}
	for _, tool := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit"} {
		if !slices.Contains(permissions.Deny, tool) {
			t.Errorf("%s is not denied: %v", tool, permissions.Deny)
		}
	}
	for _, rule := range []string{"Bash(git commit*)", "Bash(git push*)", "Bash(git branch*)", "Bash(git tag*)", "Bash(git update-ref*)", "Bash(git checkout*)"} {
		if !slices.Contains(permissions.Deny, rule) {
			t.Errorf("%s is not denied: %v", rule, permissions.Deny)
		}
	}
	policy := compilePolicy(spec)
	for _, command := range []string{"git commit -m x", "git branch review", "git push origin HEAD:refs/heads/review"} {
		if reason, denied := policy.Decide(lanes.ToolCall{Tool: lanes.ToolBash, Command: command, Cwd: spec.Dir}); !denied || reason != lanes.ReasonReadOnly {
			t.Errorf("%q: denied %v with %q", command, denied, reason)
		}
	}
	if reason, denied := policy.Decide(lanes.ToolCall{Tool: "Write", Path: filepath.Join(spec.Dir, "a.go"), Cwd: spec.Dir}); !denied || reason != lanes.ReasonReadOnly {
		t.Errorf("a write: denied %v with %q", denied, reason)
	}
	if reason, denied := policy.Decide(lanes.ToolCall{Tool: lanes.ToolBash, Command: "git log --oneline && git diff HEAD~1", Cwd: spec.Dir}); denied {
		t.Errorf("a read was denied: %s", reason)
	}
}

// Every other session keeps what it had: the commit rules, and no blanket denial of editing.
func TestASessionThatWritesIsNotReadOnly(t *testing.T) {
	spec := policySpec(t)
	permissions := generateSettings(spec).Permissions
	for _, tool := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "Bash(git commit*)", "Bash(git branch*)"} {
		if slices.Contains(permissions.Deny, tool) {
			t.Errorf("a writing session denies %s", tool)
		}
	}
	if !runsWithNoPrompt(permissions.Allow, "git commit -m plan") {
		t.Errorf("a writing session lost its commit rule: %v", permissions.Allow)
	}
	if reason, denied := compilePolicy(spec).Decide(lanes.ToolCall{Tool: lanes.ToolBash, Command: "git commit -m x", Cwd: spec.Dir}); denied {
		t.Errorf("a writing session's commit was denied: %s", reason)
	}
}

// ADR-0022: the worktrees of the other repositories a lane reads are directories the session may read with
// no prompt, and never write: the CLI's own rules deny an edit there, and the hook's policy denies a git
// command that writes there.
func TestTheRepositoriesALaneReadsAreReadOnly(t *testing.T) {
	spec := policySpec(t)
	api := filepath.Join(filepath.Dir(spec.Dir), "lane-1.reads", "api")
	spec.ReadOnlyDirs = []string{api}
	settings := generateSettings(spec)
	if !slices.Equal(settings.Permissions.AdditionalDirectories, []string{api}) {
		t.Errorf("the session may read %v", settings.Permissions.AdditionalDirectories)
	}
	for _, rule := range []string{"Edit(/" + api + ")", "Edit(/" + api + "/**)"} {
		if !slices.Contains(settings.Permissions.Deny, rule) {
			t.Errorf("no rule %s", rule)
		}
	}
	if policy := compilePolicy(spec); !slices.Contains(policy.ReadOnlyRoots, api) {
		t.Errorf("the hook's policy holds read-only roots %v", policy.ReadOnlyRoots)
	}
	if plain := generateSettings(policySpec(t)); len(plain.Permissions.AdditionalDirectories) != 0 {
		t.Errorf("a lane that reads nothing may read %v", plain.Permissions.AdditionalDirectories)
	}
}

// editAllowed reads `Edit` allow rules as the CLI documents them: `//` starts an absolute path, `/**`
// takes everything under it, and a path through a link is allowed only when the path asked for and
// the file it resolves to both match.
func editAllowed(rules []string, asked, resolved string) bool {
	covers := func(path string) bool {
		return slices.ContainsFunc(rules, func(rule string) bool {
			pattern, ok := strings.CutPrefix(rule, "Edit(//")
			if !ok {
				return false
			}
			pattern = "/" + strings.TrimSuffix(pattern, ")")
			if dir, all := strings.CutSuffix(pattern, "/**"); all {
				return strings.HasPrefix(path, dir+"/")
			}
			return path == pattern
		})
	}
	return covers(asked) && covers(resolved)
}

// FR-LANE-19: a lane that writes creates, edits, and deletes files in its own worktree, and stages and
// commits that work, with no prompt. Every file tool is covered by an `Edit` rule on the worktree, and
// the shell programs that make, move, and delete files by a rule of their own.
func TestALaneThatWritesWorksInItsWorktreeWithNoPrompt(t *testing.T) {
	spec := policySpec(t)
	rules := generateSettings(spec).Permissions.Allow
	for _, path := range []string{"a.go", "docs/notes.md", "deep/er/still/x.txt"} {
		file := filepath.Join(spec.Dir, path)
		if !editAllowed(rules, file, file) {
			t.Errorf("an edit of %s would wait on a member: %v", path, rules)
		}
	}
	for _, command := range []string{
		"mkdir -p docs/notes", "rm -f old.txt", "rm -rf build", "rmdir docs/notes", "mv notes.md docs/notes.md",
		"git rm -q old.txt", "git mv a.go b.go", "git add -A && git commit -q -m work", "mkdir -p docs && mv a docs/a && git add docs",
	} {
		if !runsWithNoPrompt(rules, command) {
			t.Errorf("%q would wait on a member: %v", command, rules)
		}
	}
}

// FR-LANE-19: what runs with no prompt stops at the worktree. A write beside it, above it, in another lane,
// in the repository, in the home directory, or through a link that leads out of it, is no rule's to allow.
func TestNoAllowRuleReachesOutsideTheWorktree(t *testing.T) {
	spec := policySpec(t)
	rules := generateSettings(spec).Permissions.Allow
	lanesDir := filepath.Dir(spec.Dir)
	for _, path := range []string{
		filepath.Join(lanesDir, "lane-2", "a.go"), filepath.Join(lanesDir, "lane-1.reads", "a.go"), filepath.Join(lanesDir, "a.go"),
		filepath.Join(filepath.Dir(lanesDir), "repository", "a.go"), spec.Dir + "-other/a.go", "/home/person/.ssh/config", "/etc/hosts",
	} {
		if editAllowed(rules, path, path) {
			t.Errorf("an edit of %s runs with no prompt: %v", path, rules)
		}
	}
	inside := filepath.Join(spec.Dir, "out", "a.go")
	if editAllowed(rules, inside, filepath.Join(lanesDir, "lane-2", "a.go")) {
		t.Errorf("an edit through a link runs with no prompt: %v", rules)
	}
	for _, rule := range rules {
		if strings.HasPrefix(rule, "Edit(") && !strings.HasSuffix(rule, filepath.Base(spec.Dir)+"/**)") {
			t.Errorf("an edit rule names more than the worktree: %s", rule)
		}
	}
	for _, command := range []string{"cp a ../a", "ln -s /etc/hosts hosts", "touch ../a", "sed -i s/a/b/ a.go", "install a ../a", "chmod 777 ..", "find . -delete"} {
		if runsWithNoPrompt(rules, command) {
			t.Errorf("%q runs with no prompt: %v", command, rules)
		}
	}
}

// A worktree named through a link gets a rule for each name, because the CLI resolves a path before it
// matches it, and `/tmp` is a link on macOS.
func TestAWorktreeNamedThroughALinkIsAllowedByEachName(t *testing.T) {
	spec := policySpec(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(spec.Dir, link); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(spec.Dir)
	if err != nil {
		t.Fatal(err)
	}
	spec.Dir = link
	rules := generateSettings(spec).Permissions.Allow
	if !editAllowed(rules, filepath.Join(link, "a.go"), filepath.Join(real, "a.go")) {
		t.Errorf("an edit in the worktree would wait on a member: %v", rules)
	}
}

// A worktree whose path holds a character a rule reads as a pattern would widen the rule, so it gets no
// edit rule, and an edit there asks as it did before.
func TestAWorktreePathThatReadsAsAPatternGetsNoEditRule(t *testing.T) {
	spec := policySpec(t)
	spec.Dir = filepath.Join(t.TempDir(), "lanes", "lane-[1]")
	if err := os.MkdirAll(spec.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rule := range generateSettings(spec).Permissions.Allow {
		if strings.HasPrefix(rule, "Edit(") {
			t.Errorf("a rule %s", rule)
		}
	}
}

// The shell programs run with no prompt only where a hook holds them to the worktree: a session with no
// hook, or no worktree, gets none, and nor does a session that reads or holds only its own tools.
func TestFileProgramsNeedAHookAndAWorktree(t *testing.T) {
	for name, change := range map[string]func(*ports.SessionSpec){
		"no hook":         func(spec *ports.SessionSpec) { spec.Binary = "" },
		"no worktree":     func(spec *ports.SessionSpec) { spec.Dir = "" },
		"a review lane":   func(spec *ports.SessionSpec) { spec.ReadOnly = true },
		"an orchestrator": func(spec *ports.SessionSpec) { spec.OnlyTools = true },
	} {
		t.Run(name, func(t *testing.T) {
			spec := policySpec(t)
			change(&spec)
			for _, rule := range generateSettings(spec).Permissions.Allow {
				for _, program := range lanes.FilePrograms() {
					if rule == "Bash("+program+" *)" {
						t.Errorf("a rule %s", rule)
					}
				}
				if (spec.ReadOnly || spec.OnlyTools || spec.Dir == "") && strings.HasPrefix(rule, "Edit(") {
					t.Errorf("a rule %s", rule)
				}
			}
		})
	}
}
