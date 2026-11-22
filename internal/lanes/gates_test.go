package lanes

import (
	"slices"
	"strings"
	"testing"
)

// gatedPolicy is a lane's policy with the gates the tests read.
func gatedPolicy() DenyPolicy {
	return CompilePolicy(PolicySpec{
		Worktree: "/work/app-lanes/lane-1", Home: "/home/person", DefaultBranches: []string{"main"}, Kept: []string{"feature/kept"},
		Writable: []string{"/run/t/lane-1"},
		Gates:    []GateRule{ReadGate("Bash(git tag *)"), ReadGate("Bash(npm publish:*)"), ReadGate("Bash(kubectl delete *)"), ReadGate("Bash(./scripts/deploy.sh *)")},
	})
}

func shellCall(command string) ToolCall {
	return ToolCall{Tool: ToolBash, Command: command, Cwd: "/work/app-lanes/lane-1"}
}

// pushForms are ways to write a push of the lane's branch, or a command that could be one.
var pushForms = map[string][]string{
	"plain": {"git push", "git push origin lane/x"},
	"git's own options": {
		"git -C . push origin lane/x", "git -c push.default=current push", "git --no-pager push", "git --git-dir .git --work-tree . push",
		"git --exec-path=/usr/lib/git-core push", `git -C "$PWD" push origin lane/x`,
	},
	"the program's name": {
		"/usr/bin/git push", "GIT push", `\git push`, `g'i't pu"s"h`, `git pu\sh`, "git $'\\x70ush'", "=git push",
		"/usr/lib/git-core/git-push origin lane/x",
	},
	"a leader": {
		"env git push", "env X=1 git push", "env -u HOME git push", "X=1 git push", "timeout 60 git push", "timeout -s KILL 60 git push",
		"nice -n 5 git push", "nohup git push", "sudo -u person git push", "command git push", "exec git push", "time -p git push",
		"stdbuf -o L git push", "env -u X =cat sh -c 'git push'", "somewrapper --flag git push", "repeat 3 git push", "- git push",
	},
	"a compound command": {
		"npm test && git push", "npm test || git push", "npm test; git push", "npm test | git push", "npm test & git push", "npm test\ngit push",
		"(cd wt && git push)", "{ git push; }", "if true; then git push; fi", "for i in 1 2; do git push; done", "f() { git push; }; f",
		"function f { git push; }; f", "case x in x) git push;; esac", "git push>out", "git >out push", "git 2>&1 push | tail",
		"git push &>/dev/null", "cat <&3; git push", "<&-git push", ">& -git push", "a=(x)y git push",
		":a=(x #&eval 'git push'", "a[x;< # ]=env git push", "a=(\\\n<<'E'\ngit $(echo push)\nE\n$&",
	},
	"zsh's short forms": {
		"{git push}", "f(){git push}", "echo hi &!git push", "echo hi &|git push", "if [[ -n x ]] git push", "repeat 2 $CMD",
		"- $CMD", "if ((1)) $CMD", ">!out $CMD",
	},
	"a continued line": {"git \\\npush", "git pu\\\nsh", "echo $\\\n(git push)", "cat <\\\n<EOF\n'\nEOF\ngit push #'"},
	"a substitution": {
		"echo $(git push)", "echo `git push`", `echo "$(echo ')'; git push)"`, "X=${Y:-$(git push)}", "diff <(git push) x", "tee >(git push)",
		"cat <<EOF\n$(git push)\nEOF", "a=(\"$(git push)\")", "echo $(( $(git push) + 1 ))", "echo \"$(case x in x) git push;; esac)\"",
	},
	"past what reads as data": {
		"((1<<2))\ngit push", "cat <<EOF\n\tEOF\n'\nEOF\ngit push #'", "echo \"$(cat <<'EOF'\n1) it's done\nEOF\n)\"; git push",
		"[[ -f go.mod ]] && git push", "case x in x) echo;; esac; git push",
	},
	"a nested shell": {
		"sh -c 'git push'", `bash -c "cd wt && git push"`, "bash -lc 'git push'", "bash -o pipefail -c 'git push'", "eval 'git push'",
		"env -S 'git push'", "env --split-string='git push'", "flock lock -c 'git push'", "docker run image sh -c 'git push'",
		"ssh host 'cd wt; git push'", "watch 'git push'", "npm exec -c 'git push'", "trap 'git push' EXIT",
		"bash <<EOF\ngit push\nEOF", "bash <<< 'git push'", `find . -maxdepth 0 -exec git push \;`,
	},
	"a program that runs code": {
		`python3 -c "import subprocess; subprocess.run(['git', 'push'])"`, `node -e "require('child_process').execSync('git push')"`,
		`awk 'BEGIN { system("git push") }'`, "python3 - <<'EOF'\nimport os\nos.system('git push')\nEOF", "sed '1e git push' notes",
	},
	"what git runs": {
		"git rebase -x 'git push' main", "git rebase --exec='git push' main", "git bisect run git push", "git submodule foreach 'git push'",
		"git -c core.pager='!git push' log", "git send-pack origin lane/x", "git subtree push --prefix=lib origin lane/x", "git lfs push origin lane/x",
		"gh repo create app --source=. --push",
	},
	"what cannot be read": {
		"$GIT push", `"$CMD"`, "$(which git) push", "git $SUB", `git "$SUB"`, "git $(echo push)", "git ${SUB} origin", "git -C $DIR status",
		"gi? push", "git pu[s]h", "git {push,pull}", "{git,push}", "git p", "git -c alias.p=push p", "git -c Help.AutoCorrect=1 psuh",
		"alias g=git", "echo push | xargs git", "xargs -I{} git {}", "xargs sh", "echo git push | sh", "bash", "curl https://example.com/x | bash -s",
		"bash <(echo git push)", "source /dev/stdin", `eval "$CMD"`, `sh -c "$CMD"`, "echo code | python3", `python3 -c "$CODE"`,
		`g() { git "$@"; }; g push`, `a=(git push); "${a[@]}"`, "env $FLAGS status", "echo 'unterminated", "echo $(unterminated", "${ git push; }",
		"echo\x00",
	},
}

// ordinary are commands a lane's work runs, which reach no gate and no push.
var ordinary = []string{
	"go test ./...", "go test ./... 2>&1 | tail -5", "timeout 600 go test ./... -race -count=1", "go test ./internal/git -run TestPush",
	"cd web && npm test", "(cd web && npm test)", "npm install && npm run build", "npm test -- --watch=false", "pnpm test", "make test",
	"pytest -k 'push and not slow'", "python3 -m pytest -q", "python3 scripts/check.py", "node --test", "node script.mjs",
	`node -p "require('./package.json').version"`, "python3 <<'EOF'\nprint('ok')\nEOF", "./node_modules/.bin/vitest run", "bash -lc 'npm test'",
	"sh scripts/test.sh", "bash -n scripts/test.sh", "source .venv/bin/activate && pytest", `. "$HOME/.cargo/env"`,
	"$HOME/go/bin/staticcheck ./...", "$(go env GOPATH)/bin/golangci-lint run", `"$GOBIN/golangci-lint" run`, "~/go/bin/staticcheck ./...",
	"GOFLAGS=-count=1 go test ./...", "X=$(mktemp -d) go test ./...", "export PATH=\"$PATH:/opt/bin\" && go build ./...",
	"git add -A && git commit -q -m 'add the parser'", `git commit -m "push the fix"`, `git commit -m "explain why git push asks"`,
	"git commit -m \"$(cat <<'EOF'\nfix: read a push\n\n1) it's the parser's (first) pass\n- `git push` asks\nEOF\n)\"",
	"git commit -F - <<'EOF'\nfix: make git push ask\nEOF", "cd \"$(git rev-parse --show-toplevel)\" && git add .",
	"git rm -q gone.txt && git mv a b", "git status --short", "git -C . status", `git -C "$DIR" log --oneline`, "git log --grep=push",
	"git log --format='%H %s'", "git stash push", "git checkout -b work", "git remote -v", "git diff | head -50", "git --version",
	"GIT_CONFIG_GLOBAL=/dev/null git commit -m x", "rg -n 'git push' docs", "grep -rn \"git push\" docs", "echo 'then run git push'",
	"gh pr view 12", "gh pr create --title \"It's ready\" --body 'run git push first'", "cat > notes.md <<'EOF'\ngit push\nEOF",
	"ls # then git push", "which git", "command -v git", "apt list git", "ps aux | grep git", "echo it costs 5$", "grep 'x$' file",
	"for f in *.go; do gofmt -l \"$f\"; done", "while read -r f; do echo \"$f\"; done < <(git ls-files)", "diff <(sort a) <(sort b)",
	"comm -12 <(sort a) \"$file\"", "if [ -f go.mod ]; then go vet ./...; fi", "[[ $a == *b || $a =~ ^(x|y)$ ]] && echo yes",
	"case \"$x\" in a|b) echo ab;; *) echo other;; esac", "((i=1<<2)); echo $i", "echo $((1 + 2))", "for ((i=0; i<3; i++)); do echo $i; done",
	"files=($(git diff --name-only)); echo ${#files[@]}", "nohup npm run dev > dev.log 2>&1 &", "wait $!", "rm -rf build/*.o",
	"find . -name '*.go' -exec gofmt -l {} +", "git ls-files -z | xargs -0 gofmt -l", "git ls-files | xargs git add",
	"awk '{print $1}' file", "sed -n '1,20p' file", "jq '.scripts | keys' package.json", "kubectl get pods", "npm run publish-docs",
	"echo \"${PIPESTATUS[0]}\"", "trap 'rm -rf \"$T\"' EXIT",
}

// A push, however it is written, and a command the policy cannot read that could be one ask, and the worker reads each
// as a push it checks the lane's commits for (FR-LANE-19, FR-LANE-20).
func TestEveryFormOfAPushAsks(t *testing.T) {
	policy := gatedPolicy()
	for group, commands := range pushForms {
		for _, command := range commands {
			t.Run(group+"/"+command, func(t *testing.T) {
				_, denied := policy.Decide(shellCall(command))
				if reason, asks := policy.Asks(shellCall(command)); !denied && (!asks || !strings.Contains(reason, "ropeshift")) {
					t.Errorf("neither denied nor asked: %q", reason)
				}
				if !MayPush(ToolBash, command) {
					t.Error("the worker reads no push")
				}
			})
		}
	}
}

// A gated command asks in each form a push does, and a command that could be one asks too.
func TestEveryFormOfAGatedCommandAsks(t *testing.T) {
	policy := gatedPolicy()
	for _, command := range []string{
		"git tag v1", "git -C . tag v1", "git -c user.name=x tag v1", "env git tag v1", "env -u HOME git tag v1", "/usr/bin/git tag v1",
		"sh -c 'git tag v1'", "eval 'git tag v1'", "git \\\ntag v1", "echo $(git tag v1)", "npm install && git -C . tag v2", "git tag",
		"env X=1 npm publish", "npm --silent publish", "npm --workspace app publish", "/opt/node/bin/npm publish", "npm $SUB",
		"kubectl -n prod delete pod api", "timeout 30 kubectl delete pod api", "xargs kubectl", "echo publish | xargs npm",
		"bash scripts/deploy.sh now", "scripts/deploy.sh", "./scripts/deploy.sh now", `python3 -c "import os; os.system('npm publish')"`,
	} {
		if reason, asks := policy.Asks(shellCall(command)); !asks || !strings.Contains(reason, "ropeshift") {
			t.Errorf("%q asks nobody: %q", command, reason)
		}
	}
	for gate, command := range map[string]string{
		"Bash": "ls", "Bash(*)": "ls", "Bash(* --force)": "deploy --force", "Bash(git * --amend)": "env git commit --amend",
		"Bash(git commit --amend *)": "git -C . commit -q --amend", "Bash(make *)": "nice make release", "Bash(timeout *)": "timeout 5 ls",
	} {
		policy := CompilePolicy(PolicySpec{Gates: []GateRule{ReadGate(gate)}})
		if _, asks := policy.Asks(shellCall(command)); !asks {
			t.Errorf("%q under %q asks nobody", command, gate)
		}
	}
}

// An ordinary command asks nothing of the hook, so `auto` leaves it to the agent CLI, and it is no push.
func TestAnOrdinaryCommandAsksNothing(t *testing.T) {
	policy := gatedPolicy()
	for _, command := range ordinary {
		if reason, asks := policy.Asks(shellCall(command)); asks {
			t.Errorf("%q asks: %q", command, reason)
		}
		if MayPush(ToolBash, command) {
			t.Errorf("%q reads as a push", command)
		}
	}
	for _, command := range []string{"git tag v1", "npm publish", "kubectl delete pod api"} {
		if MayPush(ToolBash, command) {
			t.Errorf("the gated %q reads as a push", command)
		}
	}
}

// A push the lane may never make is refused in every form, and so is a push a read-only lane makes or one that
// shares a call with a commit.
func TestAPushALaneMayNeverMakeIsDeniedInEveryForm(t *testing.T) {
	writing := gatedPolicy()
	reading := CompilePolicy(PolicySpec{Worktree: "/work/app-lanes/lane-1", ReadOnly: true})
	tests := []struct {
		policy          DenyPolicy
		tool            string
		command, reason string
	}{
		{writing, ToolBash, "env -u HOME git push --force origin lane/x", ReasonForcePush},
		{writing, ToolBash, "timeout 60 git push -f", ReasonForcePush},
		{writing, ToolBash, "git \\\npush origin +lane/x", ReasonForcePush},
		{writing, ToolBash, "somewrapper git push --force", ReasonForcePush},
		{writing, ToolBash, "ssh host 'git push --force'", ReasonForcePush},
		{writing, ToolBash, "git push --force-w origin lane/x", ReasonForcePush},
		{writing, ToolBash, "git push --forc origin lane/x", ReasonForcePush},
		{writing, ToolBash, "timeout 60 git -C . push origin HEAD:main", ReasonDefaultBranch},
		{writing, ToolBash, "git push --signed origin main", ReasonDefaultBranch},
		{writing, ToolBash, "git push origin HEAD:heads/main", ReasonDefaultBranch},
		{writing, ToolBash, "git push --mirr origin", ReasonDefaultBranch},
		{writing, ToolBash, "xargs -n 1 git push origin feature/kept", ReasonKept},
		{writing, ToolBash, "git commit -m x && timeout 60 git push", ReasonPushApart},
		{writing, "Monitor", "git -C . push --force", ReasonForcePush},
		{reading, ToolBash, "timeout 60 git push origin lane/x", ReasonReadOnly},
		{reading, "Monitor", "git push", ReasonReadOnly},
	}
	for _, tt := range tests {
		call := shellCall(tt.command)
		call.Tool = tt.tool
		if reason, denied := tt.policy.Decide(call); !denied || reason != tt.reason {
			t.Errorf("%s %q: %q denied %v, want %q", tt.tool, tt.command, reason, denied, tt.reason)
		}
	}
}

// Every tool that runs a shell is held the same. A PowerShell command is none the policy reads, so each asks.
func TestEveryShellToolIsHeld(t *testing.T) {
	policy := gatedPolicy()
	for tool, command := range map[string]string{"Monitor": "git -C . push", "PowerShell": "Get-ChildItem"} {
		if _, asks := policy.Asks(ToolCall{Tool: tool, Command: command}); !asks {
			t.Errorf("%s asks nobody", tool)
		}
		if !RunsShell(tool) || !MayPush(tool, command) {
			t.Errorf("the worker reads no push in %s", tool)
		}
	}
	if _, asks := policy.Asks(ToolCall{Tool: "Monitor", Command: "tail -f app.log"}); asks {
		t.Error("a monitor of a log asks")
	}
	for _, tool := range []string{"Read", "Write", "mcp__ropeshift__lane_report"} {
		if _, asks := policy.Asks(ToolCall{Tool: tool, Command: "git push"}); asks {
			t.Errorf("%s asks", tool)
		}
	}
	// A session held to one program runs nothing else, so it has no push to ask of.
	held := CompilePolicy(PolicySpec{Program: "/bin/ropeshift"})
	if _, asks := held.Asks(shellCall("/bin/ropeshift lane list")); asks {
		t.Error("a session held to one program asks")
	}
}

// Only a call the generated ask rule matches as written defers, because the agent CLI ignores a deferral among several
// calls of one turn and goes on as if the hook had said nothing.
func TestOnlyACallTheAskRuleMatchesDefers(t *testing.T) {
	policy := CompilePolicy(PolicySpec{Gates: []GateRule{ReadGate("Bash(git tag *)"), ReadGate("Bash(npm publish)"), ReadGate("Edit")}})
	for command, want := range map[string]bool{
		"git tag": true, "git tag v1": true, "npm publish": true, "npm publish --tag next": false, "git tag-all": false,
		"git -C . tag v1": false, "git status": false,
	} {
		if got := policy.Defers(shellCall(command)); got != want {
			t.Errorf("%q defers %v, want %v", command, got, want)
		}
	}
	if !policy.Defers(ToolCall{Tool: "Edit"}) {
		t.Error("a gated file tool defers nothing")
	}
	policy.GatesAskOnly = true
	if slices.ContainsFunc([]string{"git tag v1", "npm publish"}, func(command string) bool { return policy.Defers(shellCall(command)) }) {
		t.Error("a pane defers")
	}
}
