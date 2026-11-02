package lanes

import (
	"strings"
	"testing"
)

func testPolicy() DenyPolicy {
	return CompilePolicy(PolicySpec{
		Worktree: "/work/app-lanes/lane-1", Home: "/home/person", DefaultBranches: []string{"main", "master"},
		Writable: []string{"/run/t/lane-1"},
	})
}

func TestPolicyOnBashCommands(t *testing.T) {
	const cwd = "/work/app-lanes/lane-1"
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{name: "an ordinary push", command: "git push origin lane/one"},
		{name: "a push with upstream", command: "git push -u origin HEAD"},
		{name: "a push of the current branch", command: "git push"},
		{name: "a test run", command: "go test ./..."},
		{name: "a commit", command: `git commit -m "fix: force push notes > main"`},
		{name: "a commit message naming a flag", command: `git commit -m "git push --force is denied"`},
		{name: "an echo of a denied command", command: `echo "git push --force"`},

		{name: "force long", command: "git push --force origin lane/one", denied: ReasonForcePush},
		{name: "force short", command: "git push -f origin lane/one", denied: ReasonForcePush},
		{name: "force in a bundle", command: "git push -fu origin lane/one", denied: ReasonForcePush},
		{name: "force with lease", command: "git push --force-with-lease origin lane/one", denied: ReasonForcePush},
		{name: "force with a lease value", command: "git push --force-with-lease=lane/one:abc origin lane/one", denied: ReasonForcePush},
		{name: "force by refspec", command: "git push origin +lane/one", denied: ReasonForcePush},
		{name: "force after a global option", command: "git -C /work/app-lanes/lane-1 -c a=b push -f", denied: ReasonForcePush},
		{name: "force after an assignment", command: "FOO=bar git push -f", denied: ReasonForcePush},
		{name: "force behind a wrapper", command: "env FOO=bar command git push -f", denied: ReasonForcePush},
		{name: "force after a semicolon", command: "git status; git push -f", denied: ReasonForcePush},
		{name: "force after a conjunction", command: "go test ./... && git push -f", denied: ReasonForcePush},
		{name: "force in a pipeline", command: "true | git push -f", denied: ReasonForcePush},
		{name: "force on a second line", command: "git status\ngit push -f", denied: ReasonForcePush},
		{name: "force in a subshell", command: "(git push -f)", denied: ReasonForcePush},
		{name: "force in a substitution", command: "echo $(git push -f)", denied: ReasonForcePush},
		{name: "force in backticks", command: "echo `git push -f`", denied: ReasonForcePush},
		{name: "force through a shell", command: `sh -c "git push -f"`, denied: ReasonForcePush},
		{name: "force through a login shell", command: `bash -lc 'git push --force'`, denied: ReasonForcePush},
		{name: "force through eval", command: `eval "git push -f"`, denied: ReasonForcePush},
		{name: "a quoted command word", command: `"git" 'push' -f`, denied: ReasonForcePush},
		{name: "force in a condition", command: "if true; then git push -f; fi", denied: ReasonForcePush},

		{name: "the default branch", command: "git push origin main", denied: ReasonDefaultBranch},
		{name: "the other default branch", command: "git push origin master", denied: ReasonDefaultBranch},
		{name: "a refspec onto the default branch", command: "git push origin HEAD:main", denied: ReasonDefaultBranch},
		{name: "a full ref", command: "git push origin HEAD:refs/heads/main", denied: ReasonDefaultBranch},
		{name: "a delete of the default branch", command: "git push origin :main", denied: ReasonDefaultBranch},
		{name: "a delete flag", command: "git push --delete origin main", denied: ReasonDefaultBranch},
		{name: "every branch", command: "git push --all origin", denied: ReasonDefaultBranch},
		{name: "a mirror", command: "git push --mirror origin", denied: ReasonDefaultBranch},
		{name: "a push option that takes a value", command: "git push -o ci.skip origin main", denied: ReasonDefaultBranch},

		{name: "a redirect inside", command: "echo x > notes.txt"},
		{name: "a redirect to a subdirectory", command: "echo x >> build/out.log"},
		{name: "a redirect to the null device", command: "go test ./... > /dev/null 2>&1"},
		{name: "a descriptor copy", command: "go test 2>&1"},
		{name: "a redirect to the lane's temp", command: "echo x > /run/t/lane-1/a"},
		{name: "a read from outside", command: "cat /etc/hosts"},
		{name: "a read redirect", command: "wc -l < /etc/hosts"},
		{name: "a here document", command: "cat <<EOF\n> /etc/passwd\nEOF"},
		{name: "a here document that writes inside", command: "cat > notes.txt <<'EOF'\ntext\nEOF"},
		{name: "a comment", command: "echo x # > /etc/passwd"},
		{name: "a copy inside", command: "cp a b"},
		{name: "a move inside", command: "mv a sub/b"},
		{name: "cd inside", command: "cd sub && echo x > y"},
		{name: "the worktree by variable", command: `echo x > "$PWD/y"`},

		{name: "a redirect outside", command: "echo x > /etc/hosts", denied: ReasonOutside},
		{name: "an append outside", command: "echo x >> /tmp/out", denied: ReasonOutside},
		{name: "a clobber outside", command: "echo x >| /tmp/out", denied: ReasonOutside},
		{name: "both streams outside", command: "go test &> /tmp/out", denied: ReasonOutside},
		{name: "a numbered stream outside", command: "go test 2> /tmp/out", denied: ReasonOutside},
		{name: "a redirect with no space", command: "echo x>/tmp/out", denied: ReasonOutside},
		{name: "a climb out", command: "echo x > ../other/file", denied: ReasonOutside},
		{name: "a sibling sharing a prefix", command: "echo x > /work/app-lanes/lane-10/file", denied: ReasonOutside},
		{name: "home by tilde", command: "echo x > ~/file", denied: ReasonOutside},
		{name: "home by variable", command: `echo x > "$HOME/file"`, denied: ReasonOutside},
		{name: "an unknown variable", command: `echo x > "$OUT"`, denied: ReasonOutside},
		{name: "a substitution as a target", command: "echo x > $(mktemp)", denied: ReasonOutside},
		{name: "cd out, then a relative write", command: "cd /tmp && echo x > y", denied: ReasonOutside},
		{name: "cd by variable, then a relative write", command: `cd "$X" && echo x > y`, denied: ReasonOutside},
		{name: "tee outside", command: "echo x | tee /tmp/out", denied: ReasonOutside},
		{name: "tee append outside", command: "echo x | tee -a /tmp/out", denied: ReasonOutside},
		{name: "touch outside", command: "touch /tmp/out", denied: ReasonOutside},
		{name: "mkdir outside", command: "mkdir -p /tmp/dir", denied: ReasonOutside},
		{name: "rm outside", command: "rm -rf /tmp/dir", denied: ReasonOutside},
		{name: "a copy out", command: "cp a /tmp/b", denied: ReasonOutside},
		{name: "a move out", command: "mv a /tmp/b", denied: ReasonOutside},
		{name: "a move in that removes outside", command: "mv /tmp/b a", denied: ReasonOutside},
		{name: "a link out", command: "ln -s a /tmp/b", denied: ReasonOutside},
		{name: "dd out", command: "dd if=a of=/tmp/b", denied: ReasonOutside},
		{name: "sed in place outside", command: "sed -i '' s/a/b/ /tmp/out", denied: ReasonOutside},
		{name: "a write through a shell", command: `bash -c "echo x > /tmp/out"`, denied: ReasonOutside},

		{name: "a stage and a commit", command: "git add plans && git commit -q -m plan"},
		{name: "a commit from a subdirectory", command: "cd sub && git commit -m plan"},
		{name: "a commit by an option inside", command: "git -C sub commit -m plan"},
		{name: "a commit with a global option", command: "git -c commit.gpgsign=false commit -m plan"},
		{name: "a status in another repository", command: "git -C /work/app status"},
		{name: "a push alone", command: "git push origin lane/one"},

		{name: "a commit in another repository", command: "cd /work/app && git commit -m plan", denied: ReasonOutside},
		{name: "a stage in another lane", command: "cd ../lane-2 && git add .", denied: ReasonOutside},
		{name: "a commit elsewhere by option", command: "git -C /work/app commit -m plan", denied: ReasonOutside},
		{name: "a stage elsewhere by a relative option", command: "git -C ../lane-2 add .", denied: ReasonOutside},
		{name: "a git directory elsewhere", command: "git --git-dir=/work/app/.git commit -m plan", denied: ReasonOutside},
		{name: "a git directory as its own word", command: "git --git-dir /work/app/.git add .", denied: ReasonOutside},
		{name: "a work tree elsewhere", command: "git --work-tree=/work/app add .", denied: ReasonOutside},
		{name: "a git directory by variable", command: "GIT_DIR=/work/app/.git git commit -m plan", denied: ReasonOutside},
		{name: "a work tree by variable behind a wrapper", command: "env GIT_WORK_TREE=/work/app git add .", denied: ReasonOutside},
		{name: "an index by variable", command: "GIT_INDEX_FILE=/tmp/index git add .", denied: ReasonOutside},
		{name: "a commit from where it cannot read", command: `cd "$X" && git commit -m plan`, denied: ReasonOutside},
		{name: "a git directory it cannot read", command: `git --git-dir="$X" commit -m plan`, denied: ReasonOutside},
		{name: "a commit redirected out", command: "git commit -m plan > ../other/file", denied: ReasonOutside},
		{name: "a stage elsewhere through a shell", command: `sh -c "cd /work/app && git add ."`, denied: ReasonOutside},

		{name: "a commit and a push", command: "git commit -m plan && git push", denied: ReasonPushApart},
		{name: "a stage, a commit, and a push", command: "git add . && git commit -m plan && git push origin lane/one", denied: ReasonPushApart},
		{name: "a push before a commit", command: "git push; git commit -m plan", denied: ReasonPushApart},
		{name: "a push in a commit's message", command: `git commit -m "$(git push)"`, denied: ReasonPushApart},
		{name: "a push in backticks in a commit", command: "git commit -m `git push`", denied: ReasonPushApart},
		{name: "a push through a shell beside a stage", command: `git add . && sh -c "git push"`, denied: ReasonPushApart},
		{name: "a push through eval beside a commit", command: `git commit -m plan; eval "git push"`, denied: ReasonPushApart},
		{name: "a force push beside a commit", command: "git commit -m plan && git push -f", denied: ReasonForcePush},
	}
	policy := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: cwd})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("reason %q denied %v, want %q", reason, denied, tt.denied)
			}
		})
	}
}

func TestPolicyOnFileTools(t *testing.T) {
	const cwd = "/work/app-lanes/lane-1"
	tests := []struct {
		name   string
		tool   string
		path   string
		denied bool
	}{
		{name: "a write inside", tool: "Write", path: "/work/app-lanes/lane-1/a.go"},
		{name: "a relative edit", tool: "Edit", path: "sub/a.go"},
		{name: "a notebook inside", tool: "NotebookEdit", path: "/work/app-lanes/lane-1/a.ipynb"},
		{name: "the lane's temp", tool: "Write", path: "/run/t/lane-1/a"},
		{name: "a read is not a write", tool: "Read", path: "/etc/hosts"},
		{name: "a write outside", tool: "Write", path: "/etc/hosts", denied: true},
		{name: "an edit outside", tool: "Edit", path: "/home/person/.ssh/config", denied: true},
		{name: "a multi edit outside", tool: "MultiEdit", path: "/tmp/a", denied: true},
		{name: "a notebook outside", tool: "NotebookEdit", path: "/tmp/a.ipynb", denied: true},
		{name: "a relative climb", tool: "Write", path: "../other/a", denied: true},
		{name: "a clean climb back in is inside", tool: "Write", path: "sub/../a"},
		{name: "another lane", tool: "Write", path: "/work/app-lanes/lane-2/a", denied: true},
		{name: "a write with no path", tool: "Write", path: "", denied: true},
	}
	policy := testPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: tt.tool, Path: tt.path, Cwd: cwd})
			if denied != tt.denied {
				t.Fatalf("denied %v, %q", denied, reason)
			}
			if denied && reason != ReasonOutside {
				t.Errorf("reason %q", reason)
			}
		})
	}
}

// A worktree can be named through a link, as `/tmp` is on macOS, and the CLI reports the
// real path, so a policy holds both.
func TestPolicyHoldsEachNameOfTheWorktree(t *testing.T) {
	policy := CompilePolicy(PolicySpec{Worktree: "/tmp/w", WorktreeReal: "/private/tmp/w"})
	for _, path := range []string{"/tmp/w/a", "/private/tmp/w/a"} {
		if _, denied := policy.Decide(ToolCall{Tool: "Write", Path: path, Cwd: "/private/tmp/w"}); denied {
			t.Errorf("%s was denied", path)
		}
	}
}

func TestAPolicyWithNoWorktreeChecksCommandsOnly(t *testing.T) {
	policy := DenyPolicy{Deny: []DenyRule{{Tool: "Bash", CommandPrefix: "echo bench", Reason: "a bench denial"}}}
	if reason, denied := policy.Decide(ToolCall{Tool: "Bash", Command: "echo bench"}); !denied || reason != "a bench denial" {
		t.Errorf("reason %q denied %v", reason, denied)
	}
	if _, denied := policy.Decide(ToolCall{Tool: "Write", Path: "/etc/hosts"}); denied {
		t.Error("a path check with no worktree")
	}
}

func TestPolicyTakesAnyShellInput(t *testing.T) {
	policy := testPolicy()
	for _, command := range []string{"", ";;;", "'unterminated", `"unterminated`, "$(", "`", "<<", ">", "echo \\", "((((", "$((1+1))", strings.Repeat("(", 5000)} {
		policy.Decide(ToolCall{Tool: ToolBash, Command: command, Cwd: "/work/app-lanes/lane-1"})
	}
}

func TestPermissionRaises(t *testing.T) {
	tests := []struct {
		name  string
		from  string
		to    string
		raise bool
	}{
		{"the same mode", "default", "default", false},
		{"down to plan", "default", "plan", false},
		{"up to accept edits", "default", "acceptEdits", true},
		{"up to auto", "default", "auto", true},
		{"down from auto", "auto", "default", false},
		{"up to bypass", "auto", "bypassPermissions", true},
		{"a mode nobody knows", "default", "everything", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RaisesPermission(tt.from, tt.to); got != tt.raise {
				t.Errorf("raises %v", got)
			}
		})
	}
}
