package lanes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func readOnlyPolicy() DenyPolicy {
	return CompilePolicy(PolicySpec{
		Worktree: "/work/app-lanes/review-1", Home: "/home/person", DefaultBranches: []string{"main", "master"},
		Writable: []string{"/run/t/review-1"}, ReadOnly: true,
	})
}

// A review lane reads and reports. The commands it may run are the ones that read, and everything that
// writes a commit or a ref is denied however it is spelled, so a review can move no branch.
func TestAReadOnlyPolicyDeniesWhatWritesHistoryAndRefs(t *testing.T) {
	const cwd = "/work/app-lanes/review-1"
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{name: "a log", command: "git log --oneline -20"},
		{name: "a diff", command: "git diff HEAD~1 --stat"},
		{name: "a show", command: "git show HEAD:internal/a.go"},
		{name: "a status", command: "git status --short"},
		{name: "a blame", command: "git blame -L 1,20 internal/a.go"},
		{name: "a grep", command: "git grep -n retry"},
		{name: "a listing", command: "git ls-files | head"},
		{name: "a rev-parse", command: "git rev-parse HEAD"},
		{name: "a global option before a read", command: "git -c core.pager=cat -C /work/app-lanes/review-1 log"},
		{name: "a test run", command: "go test ./..."},
		{name: "a file written in the discarded worktree", command: "echo x > scratch.txt"},
		{name: "a command that names a denied word", command: `echo "git commit -m x"`},

		{name: "a commit", command: `git commit -m "review notes"`, denied: ReasonReadOnly},
		{name: "a commit of everything", command: "git commit -am x", denied: ReasonReadOnly},
		{name: "an empty commit", command: "git commit --allow-empty -m x", denied: ReasonReadOnly},
		{name: "a push", command: "git push origin HEAD:refs/heads/review", denied: ReasonReadOnly},
		{name: "a push of nothing named", command: "git push", denied: ReasonReadOnly},
		{name: "a new branch", command: "git branch review-notes", denied: ReasonReadOnly},
		{name: "a moved branch", command: "git branch -f lane/one HEAD", denied: ReasonReadOnly},
		{name: "a checkout that makes a branch", command: "git checkout -b review-notes", denied: ReasonReadOnly},
		{name: "a checkout of a branch", command: "git checkout lane/one", denied: ReasonReadOnly},
		{name: "a switch", command: "git switch -c review-notes", denied: ReasonReadOnly},
		{name: "a reset", command: "git reset --hard HEAD~1", denied: ReasonReadOnly},
		{name: "a ref update", command: "git update-ref refs/heads/lane/one HEAD", denied: ReasonReadOnly},
		{name: "a symbolic ref", command: "git symbolic-ref HEAD refs/heads/lane/one", denied: ReasonReadOnly},
		{name: "a tag", command: "git tag review-1", denied: ReasonReadOnly},
		{name: "a merge", command: "git merge lane/two", denied: ReasonReadOnly},
		{name: "a rebase", command: "git rebase main", denied: ReasonReadOnly},
		{name: "a cherry-pick", command: "git cherry-pick abc123", denied: ReasonReadOnly},
		{name: "a revert", command: "git revert HEAD", denied: ReasonReadOnly},
		{name: "an am", command: "git am < patch.mbox", denied: ReasonReadOnly},
		{name: "a stash", command: "git stash", denied: ReasonReadOnly},
		{name: "a fetch into a branch", command: "git fetch origin main:main", denied: ReasonReadOnly},
		{name: "a pull", command: "git pull", denied: ReasonReadOnly},
		{name: "a worktree for a new branch", command: "git worktree add -b review ../review-2", denied: ReasonReadOnly},
		{name: "a commit after a global option", command: "git -C /work/app-lanes/review-1 -c a=b commit -m x", denied: ReasonReadOnly},
		{name: "a commit after an assignment", command: "FOO=bar git commit -m x", denied: ReasonReadOnly},
		{name: "a commit behind a wrapper", command: "env FOO=bar command git commit -m x", denied: ReasonReadOnly},
		{name: "a commit after a conjunction", command: "go test ./... && git commit -m x", denied: ReasonReadOnly},
		{name: "a commit in a subshell", command: "(git commit -m x)", denied: ReasonReadOnly},
		{name: "a commit in a substitution", command: "echo $(git commit -m x)", denied: ReasonReadOnly},
		{name: "a commit through a shell", command: `sh -c "git commit -m x"`, denied: ReasonReadOnly},
		{name: "a commit through eval", command: `eval "git commit -m x"`, denied: ReasonReadOnly},
		{name: "a commit in a condition", command: "if true; then git commit -m x; fi", denied: ReasonReadOnly},
		{name: "a commit after cd", command: "cd sub && git commit -m x", denied: ReasonReadOnly},

		{name: "a write outside the worktree", command: "echo x > /etc/hosts", denied: ReasonOutside},
		{name: "a copy outside the worktree", command: "cp a /tmp/b", denied: ReasonOutside},
	}
	policy := readOnlyPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: cwd})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("%q: denied %v with %q, want %q", tt.command, denied, reason, tt.denied)
			}
		})
	}
}

// The tools that edit a file are denied outright on a review lane, wherever the path points.
func TestAReadOnlyPolicyDeniesTheEditingTools(t *testing.T) {
	policy := readOnlyPolicy()
	for _, tool := range []string{toolWrite, toolEdit, toolMultiEdit, toolNotebookEdit} {
		for _, path := range []string{"/work/app-lanes/review-1/a.go", "/etc/hosts", "scratch.txt"} {
			reason, denied := policy.Decide(ToolCall{Tool: tool, Path: path, Cwd: "/work/app-lanes/review-1"})
			if !denied || reason != ReasonReadOnly {
				t.Errorf("%s of %s: denied %v with %q", tool, path, denied, reason)
			}
		}
	}
	if reason, denied := policy.Decide(ToolCall{Tool: "Read", Path: "/work/app-lanes/review-1/a.go"}); denied {
		t.Errorf("a read was denied: %s", reason)
	}
}

// The words a reviewer reads in a denial say what to do instead.
func TestTheReadOnlyReasonSaysWhatToDoInstead(t *testing.T) {
	for _, want := range []string{"reads", ToolLaneReport, "commit"} {
		if !strings.Contains(ReasonReadOnly, want) {
			t.Errorf("the reason %q leaves out %q", ReasonReadOnly, want)
		}
	}
}

// Every other lane keeps writing, and its policy gains nothing: the rules a review lane adds do not
// reach it.
func TestAnOrdinaryPolicyIsNotReadOnly(t *testing.T) {
	policy := testPolicy()
	if len(policy.Deny) != 0 {
		t.Errorf("an ordinary policy denies %+v", policy.Deny)
	}
	for _, call := range []ToolCall{
		{Tool: ToolBash, Command: "git commit -m x", Cwd: "/work/app-lanes/lane-1"},
		{Tool: ToolBash, Command: "git branch other", Cwd: "/work/app-lanes/lane-1"},
		{Tool: ToolBash, Command: "git checkout -b other", Cwd: "/work/app-lanes/lane-1"},
		{Tool: toolWrite, Path: "/work/app-lanes/lane-1/a.go", Cwd: "/work/app-lanes/lane-1"},
		{Tool: toolEdit, Path: "/work/app-lanes/lane-1/a.go", Cwd: "/work/app-lanes/lane-1"},
	} {
		if reason, denied := policy.Decide(call); denied {
			t.Errorf("%+v was denied: %s", call, reason)
		}
	}
}

// The policy file is written once and read by a hook with no worker, and a worker rewrites it to hold a
// lane. A rewrite that read and wrote it back must keep what makes a review lane read-only.
func TestAReadOnlyPolicySurvivesBeingWrittenAndRead(t *testing.T) {
	raw, err := json.Marshal(readOnlyPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var read DenyPolicy
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if reason, denied := read.Decide(ToolCall{Tool: ToolBash, Command: "git commit -m x", Cwd: "/work/app-lanes/review-1"}); !denied || reason != ReasonReadOnly {
		t.Errorf("after a round trip a commit was denied %v with %q", denied, reason)
	}
	if reason, denied := read.Decide(ToolCall{Tool: toolWrite, Path: "/work/app-lanes/review-1/a.go"}); !denied || reason != ReasonReadOnly {
		t.Errorf("after a round trip a write was denied %v with %q", denied, reason)
	}
}

// ADR-0023: a lane never moves the branch it continues. No push reaches it, and no command that moves or
// checks out a branch names it, while reading it stays open.
func TestAPolicyKeepsTheBranchALaneContinues(t *testing.T) {
	const cwd = "/work/app-lanes/lane-1"
	policy := CompilePolicy(PolicySpec{
		Worktree: cwd, Home: "/home/person", DefaultBranches: []string{"main"}, Kept: []string{"feature/retry"},
	})
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{name: "a log of it", command: "git log --oneline feature/retry"},
		{name: "a diff against it", command: "git diff feature/retry..HEAD"},
		{name: "a show of a file on it", command: "git show feature/retry:internal/a.go"},
		{name: "a push of the lane's own branch", command: "git push -u origin HEAD:lane/lane-1"},
		{name: "a branch with a longer name", command: "git branch -f feature/retry-2 HEAD"},

		{name: "a push to it", command: "git push origin HEAD:feature/retry", denied: ReasonKept},
		{name: "a push of it by name", command: "git push origin feature/retry", denied: ReasonKept},
		{name: "a push to its full ref", command: "git push origin HEAD:refs/heads/feature/retry", denied: ReasonKept},
		{name: "a delete of it", command: "git push origin :feature/retry", denied: ReasonKept},
		{name: "a forced move", command: "git branch -f feature/retry HEAD", denied: ReasonKept},
		{name: "a delete", command: "git branch -D feature/retry", denied: ReasonKept},
		{name: "a rename", command: "git branch -m feature/retry old", denied: ReasonKept},
		{name: "a switch to it", command: "git switch feature/retry", denied: ReasonKept},
		{name: "a checkout of it", command: "git checkout feature/retry", denied: ReasonKept},
		{name: "a ref update", command: "git update-ref refs/heads/feature/retry HEAD", denied: ReasonKept},
		{name: "a push to the default branch still", command: "git push origin HEAD:main", denied: ReasonDefaultBranch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: cwd})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("%q: denied %v with %q, want %q", tt.command, denied, reason, tt.denied)
			}
		})
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var read DenyPolicy
	if err := json.Unmarshal(raw, &read); err != nil || len(read.Kept) != 1 || read.Kept[0] != "feature/retry" {
		t.Errorf("the policy as a hook reads it: %+v, %v", read.Kept, err)
	}
}

// ADR-0022: the read-only worktrees of other repositories a lane reads take no write, no commit, no push,
// and no branch change, as a review lane's worktree takes none, while the lane's own worktree works as
// before.
func TestAPolicyKeepsTheReadOnlyWorktreesReadOnly(t *testing.T) {
	const cwd = "/work/plan-lanes/lane-1"
	const api = "/work/plan-lanes/lane-1.reads/api"
	policy := CompilePolicy(PolicySpec{
		Worktree: cwd, Home: "/home/person", DefaultBranches: []string{"main"}, ReadOnlyRoots: []string{api},
	})
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{name: "a log there", command: "git -C " + api + " log --oneline"},
		{name: "a grep there", command: "cd " + api + " && git grep -n retry"},
		{name: "a read of a file there", command: "cat " + api + "/README.md"},
		{name: "a commit in the lane's own worktree", command: "git commit -m plan"},
		{name: "a branch in the lane's own worktree", command: "git branch -f lane/lane-1 HEAD"},

		{name: "a commit there", command: "git -C " + api + " commit -m x", denied: ReasonReadOnlyTree},
		{name: "a commit after cd", command: "cd " + api + " && git commit -m x", denied: ReasonReadOnlyTree},
		{name: "a checkout there", command: "git -C " + api + " checkout -b mine", denied: ReasonReadOnlyTree},
		{name: "a switch there", command: "cd " + api + " && git switch main", denied: ReasonReadOnlyTree},
		{name: "a reset there", command: "git -C " + api + " reset --hard HEAD~1", denied: ReasonReadOnlyTree},
		{name: "a push from there", command: "git -C " + api + " push origin HEAD:main", denied: ReasonReadOnlyTree},
		{name: "a stage there", command: "git -C " + api + " add .", denied: ReasonReadOnlyTree},
		{name: "a work tree option", command: "git --work-tree=" + api + " commit -m x", denied: ReasonReadOnlyTree},
		{name: "a write there", command: "echo x > " + api + "/notes.txt", denied: ReasonOutside},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: cwd})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("%q: denied %v with %q, want %q", tt.command, denied, reason, tt.denied)
			}
		})
	}
	if reason, denied := policy.Decide(ToolCall{Tool: toolEdit, Path: api + "/main.go", Cwd: cwd}); !denied || reason != ReasonOutside {
		t.Errorf("an edit there: denied %v with %q", denied, reason)
	}
}

// FR-LANE-19: a stage of a deletion or a rename runs with no prompt, as a stage does, so the hook holds it
// where it holds a stage: to this lane's worktree, apart from a push, and out of the worktrees it reads.
func TestAPolicyHoldsAStagedDeletionOrRenameToTheWorktree(t *testing.T) {
	const cwd = "/work/app-lanes/lane-1"
	const api = "/work/app-lanes/lane-1.reads/api"
	policy := CompilePolicy(PolicySpec{Worktree: cwd, Home: "/home/person", DefaultBranches: []string{"main"}, ReadOnlyRoots: []string{api}})
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{name: "a staged deletion", command: "git rm -q old.txt"},
		{name: "a staged rename", command: "git mv notes.md docs/notes.md"},
		{name: "a deletion and a commit", command: "git rm -q old.txt && git commit -m tidy"},

		{name: "a deletion in another lane", command: "git -C /work/app-lanes/lane-2 rm -q old.txt", denied: ReasonOutside},
		{name: "a rename after a climb", command: "cd .. && git mv a b", denied: ReasonOutside},
		{name: "a deletion and a push", command: "git rm -q old.txt && git push", denied: ReasonPushApart},
		{name: "a rename and a push", command: "git mv a b; git push origin lane/one", denied: ReasonPushApart},
		{name: "a deletion in a worktree it reads", command: "git -C " + api + " rm -q README.md", denied: ReasonReadOnlyTree},
		{name: "a rename in a worktree it reads", command: "cd " + api + " && git mv a b", denied: ReasonReadOnlyTree},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: cwd})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("%q: denied %v with %q, want %q", tt.command, denied, reason, tt.denied)
			}
		})
	}
}

// FR-LANE-19: the programs generated settings let make, move, and delete files with no prompt are the ones
// whose every operand a hook reads, and nothing that runs another program or makes a link.
func TestTheFileProgramsAreThoseAHookReadsWhole(t *testing.T) {
	for _, program := range FilePrograms() {
		if rule, listed := writers[program]; !listed || rule != writesEvery {
			t.Errorf("%s: a hook does not read every operand it changes", program)
		}
	}
	for _, program := range []string{"ln", "cp", "install", "sed", "touch", "tee", "dd", "chmod", "find", "xargs", "sh"} {
		if slices.Contains(FilePrograms(), program) {
			t.Errorf("%s runs with no prompt", program)
		}
	}
}

// FR-LANE-19: a program that runs with no prompt changes nothing through a link, because a link in the
// worktree can lead out of it, and a hook reads a path's text. Its target is denied where any part of it
// inside the worktree is a link, and its last part too, because `rm -r link/` empties what the link names.
// What is not a link works as before.
func TestAFileProgramChangesNothingThroughALink(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "app-lanes", "lane-1")
	outside := filepath.Join(root, "kept")
	for _, dir := range []string{filepath.Join(worktree, "docs"), outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs", filepath.Join(worktree, "docs-link")); err != nil {
		t.Fatal(err)
	}
	policy := CompilePolicy(PolicySpec{Worktree: worktree, Home: "/home/person", DefaultBranches: []string{"main"}}).WithLinks(diskLinks)
	tests := []struct {
		name    string
		command string
		denied  string
	}{
		{name: "a directory made", command: "mkdir -p docs/notes"},
		{name: "a file deleted", command: "rm -f docs/old.txt"},
		{name: "a directory deleted", command: "rm -rf docs/notes && rmdir docs"},
		{name: "a file moved", command: "mv notes.md docs/notes.md"},
		{name: "a file deleted at its full path", command: "rm " + filepath.Join(worktree, "docs", "a.txt")},

		{name: "a delete through a link", command: "rm -rf out/data", denied: ReasonLink},
		{name: "a link emptied", command: "rm -r out/", denied: ReasonLink},
		{name: "a link removed", command: "rm out", denied: ReasonLink},
		{name: "a directory made through a link", command: "mkdir -p out/x", denied: ReasonLink},
		{name: "a move into a link", command: "mv notes.md out", denied: ReasonLink},
		{name: "a move out of a link", command: "mv out/secret notes.md", denied: ReasonLink},
		{name: "a link inside the worktree", command: "rm docs-link/a.txt", denied: ReasonLink},
		{name: "a delete after cd into a link", command: "cd out && rm -rf data", denied: ReasonLink},
		{name: "a delete outside", command: "rm -rf " + outside, denied: ReasonOutside},
		{name: "a delete after a climb", command: "rm -rf ../lane-2", denied: ReasonOutside},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: tt.command, Cwd: worktree})
			if denied != (tt.denied != "") || reason != tt.denied {
				t.Errorf("%q: denied %v with %q, want %q", tt.command, denied, reason, tt.denied)
			}
		})
	}
	unread := CompilePolicy(PolicySpec{Worktree: worktree}).WithLinks(func(string) (bool, error) { return false, os.ErrPermission })
	if reason, denied := unread.Decide(ToolCall{Tool: ToolBash, Command: "rm notes.md", Cwd: worktree}); !denied || reason != ReasonLink {
		t.Errorf("a disk that cannot say: denied %v with %q", denied, reason)
	}
	if _, denied := CompilePolicy(PolicySpec{Worktree: worktree}).Decide(ToolCall{Tool: ToolBash, Command: "rm out/data", Cwd: worktree}); denied {
		t.Error("a policy with no disk read one")
	}
}

// diskLinks reads links as a hook does.
func diskLinks(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil && info.Mode()&os.ModeSymlink != 0, err
}

// A session whose reads are scoped reads only under its read roots: a file outside them, or a search
// that names one, is denied whatever a rule allowed, and a search with no path reads where it stands.
func TestAReadOutsideTheReadRootsIsDenied(t *testing.T) {
	policy := CompilePolicy(PolicySpec{Worktree: "/run/a/session", ReadRoots: []string{"/run/a/session", "/run/a/repositories/api"}})
	cwd := "/run/a/session"
	for _, call := range []ToolCall{
		{Tool: "Read", Path: "/home/x/.ssh/id_ed25519", Cwd: cwd},
		{Tool: "Read", Path: "/proc/42/environ", Cwd: cwd},
		{Tool: "Read", Path: "../../state/ledger.db", Cwd: cwd},
		{Tool: "Grep", Path: "/home/x/.config/gh", Cwd: cwd},
		{Tool: "Glob", Path: "/state", Cwd: cwd},
		{Tool: "Grep", Cwd: "/elsewhere"},
		{Tool: "Read", Path: "$HOME/secret", Cwd: cwd},
		{Tool: "Read", Path: "~/.ssh/config", Cwd: cwd},
	} {
		if reason, denied := policy.Decide(call); !denied || reason != ReasonReadOutside {
			t.Errorf("%+v: %q %v", call, reason, denied)
		}
	}
	for _, call := range []ToolCall{
		{Tool: "Read", Path: "/run/a/repositories/api/main.go", Cwd: cwd},
		{Tool: "Read", Path: "notes.md", Cwd: cwd},
		{Tool: "Grep", Cwd: cwd},
		{Tool: "Glob", Path: "/run/a/repositories/api", Cwd: cwd},
	} {
		if reason, denied := policy.Decide(call); denied {
			t.Errorf("%+v was denied: %s", call, reason)
		}
	}
	if _, denied := CompilePolicy(PolicySpec{Worktree: "/w"}).Decide(ToolCall{Tool: "Read", Path: "/etc/hosts", Cwd: "/w"}); denied {
		t.Error("a lane with no read roots was held to them")
	}
}

// A session held to one program runs it alone in a shell: another program, a second command, a
// substitution, a variable, or a redirect to or from a file is denied whatever a rule or a mode allowed.
func TestASessionHeldToOneProgramRunsNothingElse(t *testing.T) {
	policy := CompilePolicy(PolicySpec{Worktree: "/run/a/session", Program: "ropeshift"})
	cwd := "/run/a/session"
	for _, command := range []string{
		"cat /home/x/.ssh/id_ed25519",
		"ls",
		"ropeshift lane list && cat /etc/hosts",
		"ropeshift lane list; ls",
		"ropeshift lane list | sh",
		"ropeshift lane list > /tmp/out",
		"ropeshift orchestrator attach < /home/x/.ssh/id_ed25519",
		"ropeshift message send --body \"$(cat /etc/hosts)\"",
		"ropeshift message send --body \"$HOME\"",
		"ropeshift message send --body `id`",
		"FOO=1 ropeshift lane list",
		"env ropeshift lane list",
		"/usr/local/bin/ropeshift lane list",
		"$R lane list",
		"ropeshift lane list\ncat /etc/hosts",
	} {
		if reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: command, Cwd: cwd}); !denied || !strings.Contains(reason, "ropeshift") {
			t.Errorf("%q: %q %v", command, reason, denied)
		}
	}
	for _, command := range []string{
		"ropeshift lane list",
		"ropeshift lane list --json 2>&1",
		"ropeshift message send --orchestrator atlas --body 'done; next: the $5 fix'",
		"'ropeshift' work show WO-1",
	} {
		if reason, denied := policy.Decide(ToolCall{Tool: ToolBash, Command: command, Cwd: cwd}); denied {
			t.Errorf("%q was denied: %s", command, reason)
		}
	}
	if _, denied := CompilePolicy(PolicySpec{Worktree: "/w"}).Decide(ToolCall{Tool: ToolBash, Command: "ls", Cwd: "/w"}); denied {
		t.Error("a session held to no program was held to one")
	}
}

// readRootsOnDisk is a session directory and a read-only tree whose links lead out of the read roots, to
// nowhere, and back in, with a directory beside them that no root holds.
func readRootsOnDisk(t *testing.T) (session, tree, outside string) {
	t.Helper()
	base := t.TempDir()
	session, tree, outside = filepath.Join(base, "session"), filepath.Join(base, "tree"), filepath.Join(base, "outside")
	for _, dir := range []string{session, filepath.Join(tree, "src"), filepath.Join(outside, "dir")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(outside, "secret.txt"), filepath.Join(outside, "dir", "key.txt"), filepath.Join(tree, "real.txt")} {
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for link, to := range map[string]string{
		"secret-link": filepath.Join(outside, "secret.txt"), "dir-link": filepath.Join(outside, "dir"), "dangling": filepath.Join(tree, "missing"),
		"chain-1": "chain-2", "chain-2": "real.txt",
	} {
		if err := os.Symlink(to, filepath.Join(tree, link)); err != nil {
			t.Fatal(err)
		}
	}
	return session, tree, outside
}

// With the disk, a read holds to where a path leads: a link in a read-only tree that leads out of the read
// roots, through a file or a directory, or to nothing, is denied, and a chain of links that ends inside is
// read. A Glob pattern whose fixed part lies outside the roots is denied too.
func TestAReadThroughALinkHoldsToWhereItLeads(t *testing.T) {
	session, tree, outside := readRootsOnDisk(t)
	policy := CompilePolicy(PolicySpec{Worktree: session, ReadRoots: []string{session, tree}}).WithRealPaths(filepath.EvalSymlinks)
	for _, call := range []ToolCall{
		{Tool: "Read", Path: filepath.Join(tree, "secret-link"), Cwd: session},
		{Tool: "Read", Path: filepath.Join(tree, "dir-link", "key.txt"), Cwd: session},
		{Tool: "Grep", Path: filepath.Join(tree, "dir-link"), Cwd: session},
		{Tool: "Read", Path: filepath.Join(tree, "dangling"), Cwd: session},
		{Tool: "Read", Path: "secret-link", Cwd: tree},
		{Tool: "Glob", Path: tree, Pattern: outside + "/*", Cwd: session},
		{Tool: "Glob", Path: tree, Pattern: "../outside/*", Cwd: session},
		{Tool: "Glob", Path: tree, Pattern: "dir-link/*", Cwd: session},
		{Tool: "Glob", Path: tree, Pattern: "**/../../outside/*", Cwd: session},
		{Tool: "Glob", Pattern: "/*", Cwd: session},
	} {
		if reason, denied := policy.Decide(call); !denied || reason != ReasonReadOutside {
			t.Errorf("%+v: %q %v", call, reason, denied)
		}
	}
	for _, call := range []ToolCall{
		{Tool: "Read", Path: filepath.Join(tree, "chain-1"), Cwd: session},
		{Tool: "Read", Path: filepath.Join(tree, "real.txt"), Cwd: session},
		{Tool: "Grep", Path: tree, Cwd: session},
		{Tool: "Glob", Path: tree, Pattern: "**/*.go", Cwd: session},
		{Tool: "Glob", Path: tree, Pattern: "src/*.go", Cwd: session},
		{Tool: "Glob", Pattern: "*.md", Cwd: session},
	} {
		if reason, denied := policy.Decide(call); denied {
			t.Errorf("%+v was denied: %s", call, reason)
		}
	}
}
