package worker

import (
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
)

// A shell command is a gate when any command in it is a push or a call a gate names, whatever stands before that
// command, and when it holds what the reading does not follow. Anything else is no gate, so Approve all still covers it.
func TestAShellCommandIsAGateWhenAnyCommandInItIs(t *testing.T) {
	t.Parallel()
	gates := []lanes.GateRule{lanes.ReadGate("Bash(git tag *)"), lanes.ReadGate("Bash(npm publish:*)")}
	for command, want := range map[string]bool{
		"git push":                              true,
		"git push origin feature":               true,
		"git commit -m x && git push":           true,
		"cd wt && git push":                     true,
		"git -C . push":                         true,
		"git -C wt -c user.name=x push":         true,
		"git --git-dir .git --work-tree . push": true,
		"git --no-pager push":                   true,
		"env X=1 git push":                      true,
		"X=1 Y=2 git push":                      true,
		"env -u HOME X=1 git push":              true,
		"/usr/bin/git push":                     true,
		"GIT push":                              true,
		`\git push`:                             true,
		`g'i't pu"s"h`:                          true,
		`git pu\sh`:                             true,
		"npm test || git push":                  true,
		"npm test; git push":                    true,
		"npm test | git push":                   true,
		"npm test & git push":                   true,
		"npm test\ngit push":                    true,
		"(cd wt && git push)":                   true,
		"{ git push; }":                         true,
		"if true; then git push; fi":            true,
		"nohup git push":                        true,
		"timeout 60 git push":                   true,
		"git push>out":                          true,
		"git >out push":                         true,
		"git 2>&1 push | tail":                  true,
		"git push &>/dev/null":                  true,
		"bash -c 'git push'":                    true,
		`sh -c "cd wt && git push"`:             true,
		"eval 'git push'":                       true,
		"env -S 'git push'":                     true,
		"ssh host 'cd wt; git push'":            true,
		"echo push | xargs git":                 true,
		"git -c alias.p=push p":                 true,
		"git -c Help.AutoCorrect=1 psuh":        true,
		"git tag v1":                            true,
		"npm install && git -C . tag v2":        true,
		"env X=1 npm publish":                   true,
		"git $(echo push)":                      true,
		"git `echo push`":                       true,
		"$GIT push":                             true,
		"git ${SUB}":                            true,
		`git "$SUB"`:                            true,
		"git $'\\x70ush'":                       true,
		`npm install "unterminated`:             true,
		"npm install 'unterminated":             true,
		"gi? push":                              true,
		"git pu[s]h":                            true,
		"git {push,pull}":                       true,
		"cat <<EOF\ngit push\nEOF":              true,
		"bash <<< 'git push'":                   true,
		"diff <(git push) x":                    true,
		"git push >":                            true,
		"npm install":                           false,
		"npm install && npm test":               false,
		"cd wt && git status":                   false,
		"git -C . status":                       false,
		"git log --grep=push":                   false,
		"git stash push":                        false,
		"go test ./... 2>&1 | tail -5":          false,
		`git commit -m "push the fix"`:          false,
		`gh pr create --title "It's ready"`:     false,
		"rm -f build/*.o":                       false,
		"if [ -f go.mod ]; then go vet; fi":     false,
		"ls # then git push":                    false,
		"echo it costs 5$":                      false,
		"git --version":                         false,
	} {
		if got := shellGated(command, gates); got != want {
			t.Errorf("%q reads as gate %v, want %v", command, got, want)
		}
	}
}

// What the hook asks for as a push or a gate is a gate to the worker, so no batch and no judgment covers it, whichever
// shell tool it runs in.
func TestWhatTheHookAsksForIsAGate(t *testing.T) {
	t.Parallel()
	run := &laneRun{gates: []string{"Bash(git tag *)"}}
	for tool, command := range map[string]string{"Bash": "git p", "Monitor": "git -C . push", "PowerShell": "Get-ChildItem"} {
		if !run.gated(authority.ToolCall{Tool: tool, Command: command}) {
			t.Errorf("%s %q is no gate", tool, command)
		}
	}
	if run.gated(authority.ToolCall{Tool: "Monitor", Command: "tail -f app.log"}) {
		t.Error("a monitor of a log is a gate")
	}
}
