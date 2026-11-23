package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// In auto mode the CLI runs a read outside its working directories with no prompt, so the hook asks for a lane's file
// tool and shell reads that reach past the worktree, a read-only tree, and the lane's private directory, in every
// permission mode. A read inside any of them is left to the mode.
func TestALanesReadOutsideItsRootsAsksInEveryMode(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(base, "lane")
	tree := filepath.Join(base, "lane.reads", "api")
	for _, dir := range []string{worktree, tree} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	home := "/home/person"
	path := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{
		Worktree: worktree, Home: home, ReadOnlyRoots: []string{tree}, AskReadRoots: []string{worktree, tree},
	}))
	tests := []struct {
		name  string
		tool  string
		input map[string]string
		want  string
	}{
		{"read under /tmp", "Read", map[string]string{"file_path": "/tmp/outside-note.txt"}, "ask"},
		{"read under the home directory", "Read", map[string]string{"file_path": home + "/.ssh/config"}, "ask"},
		{"search of the home directory", "Grep", map[string]string{"path": home, "pattern": "token"}, "ask"},
		{"glob under /tmp", "Glob", map[string]string{"pattern": "/tmp/**/*.go"}, "ask"},
		{"shell read under /tmp", "Bash", map[string]string{"command": "cat /tmp/outside-note.txt"}, "ask"},
		{"shell read under the home directory", "Bash", map[string]string{"command": "cat ~/.ssh/config"}, "ask"},
		{"read in the worktree", "Read", map[string]string{"file_path": filepath.Join(worktree, "main.go")}, ""},
		{"read in a read-only tree", "Read", map[string]string{"file_path": filepath.Join(tree, "handler.go")}, ""},
		{"search in the worktree", "Grep", map[string]string{"pattern": "token"}, ""},
		{"shell read in the worktree", "Bash", map[string]string{"command": "cat main.go"}, ""},
		{"shell read in a read-only tree", "Bash", map[string]string{"command": "cat " + filepath.Join(tree, "handler.go")}, ""},
	}
	for _, mode := range []string{"plan", "dontAsk", "default", "acceptEdits", "auto"} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				input, err := json.Marshal(map[string]any{
					"tool_name": tt.tool, "tool_use_id": "toolu_1", "cwd": worktree, "permission_mode": mode, "tool_input": tt.input,
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

// decideAs runs the hook's decision on one call of a lane, in a mode, the way the CLI hands it over.
func decideAs(t *testing.T, policy string, cwd, tool, transcript string, toolInput map[string]string) string {
	t.Helper()
	input, err := json.Marshal(map[string]any{
		"tool_name": tool, "tool_use_id": "toolu_1", "cwd": cwd, "permission_mode": "auto", "transcript_path": transcript, "tool_input": toolInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, answer := decide(config{event: eventPreToolUse, policy: policy}, input)
	return hookDecision(t, answer)
}

// A link in the worktree that leads out of it reads where it leads, so a read through it asks whatever tool makes it.
func TestAReadThroughALinkOutOfTheWorktreeAsks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree, outside := filepath.Join(base, "lane"), filepath.Join(base, "outside")
	for _, dir := range []string{worktree, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "key"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "main.go"), []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "out")); err != nil {
		t.Fatal(err)
	}
	path := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{Worktree: worktree, Home: "/home/person", AskReadRoots: []string{worktree}}))
	tests := []struct {
		name  string
		tool  string
		input map[string]string
		want  string
	}{
		{"read of a file through the link", "Read", map[string]string{"file_path": filepath.Join(worktree, "out", "key")}, "ask"},
		{"read by a relative path through the link", "Read", map[string]string{"file_path": "out/key"}, "ask"},
		{"search of the link", "Grep", map[string]string{"path": "out", "pattern": "secret"}, "ask"},
		{"glob under the link", "Glob", map[string]string{"pattern": "out/**"}, "ask"},
		{"shell read through the link", "Bash", map[string]string{"command": "cat out/key"}, "ask"},
		{"read of a file in the worktree", "Read", map[string]string{"file_path": filepath.Join(worktree, "main.go")}, ""},
		{"shell read in the worktree", "Bash", map[string]string{"command": "cat main.go"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideAs(t, path, worktree, tt.tool, "", tt.input); got != tt.want {
				t.Errorf("answered %q, want %q", got, tt.want)
			}
		})
	}
}

// The CLI saves a large tool output under its own session's folder and tells the session to read it. That read, by the
// Read tool, is the lane's own and asks nothing. Another session's folder, the transcript, and any other file under the
// CLI's directory ask, and so does a shell read of the saved file.
func TestALanesReadOfItsOwnSavedToolOutputAsksNothing(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(base, "lane")
	project := filepath.Join(base, "home", ".claude", "projects", "-work-lane")
	own, other := filepath.Join(project, "session-own"), filepath.Join(project, "session-other")
	for _, dir := range []string{worktree, filepath.Join(own, "tool-results"), filepath.Join(other, "tool-results")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	transcript := own + ".jsonl"
	for _, file := range []string{transcript, filepath.Join(own, "tool-results", "out.txt"), filepath.Join(other, "tool-results", "out.txt")} {
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(other, "tool-results", "out.txt"), filepath.Join(own, "tool-results", "link.txt")); err != nil {
		t.Fatal(err)
	}
	path := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{Worktree: worktree, Home: filepath.Join(base, "home"), AskReadRoots: []string{worktree}}))
	tests := []struct {
		name  string
		tool  string
		input map[string]string
		want  string
	}{
		{"the own saved output", "Read", map[string]string{"file_path": filepath.Join(own, "tool-results", "out.txt")}, ""},
		{"another session's saved output", "Read", map[string]string{"file_path": filepath.Join(other, "tool-results", "out.txt")}, "ask"},
		{"the transcript itself", "Read", map[string]string{"file_path": transcript}, "ask"},
		{"a file of the CLI's directory", "Read", map[string]string{"file_path": filepath.Join(project, "other.jsonl")}, "ask"},
		{"the folder climbed out of", "Read", map[string]string{"file_path": filepath.Join(own, "tool-results", "..", "..", "session-other", "tool-results", "out.txt")}, "ask"},
		{"a link to another session's output", "Read", map[string]string{"file_path": filepath.Join(own, "tool-results", "link.txt")}, "ask"},
		{"a search of the saved output", "Grep", map[string]string{"path": filepath.Join(own, "tool-results"), "pattern": "x"}, "ask"},
		{"a shell read of the saved output", "Bash", map[string]string{"command": "cat " + filepath.Join(own, "tool-results", "out.txt")}, "ask"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideAs(t, path, worktree, tt.tool, transcript, tt.input); got != tt.want {
				t.Errorf("answered %q, want %q", got, tt.want)
			}
		})
	}
	if got := decideAs(t, path, worktree, "Read", "", map[string]string{"file_path": filepath.Join(own, "tool-results", "out.txt")}); got != "ask" {
		t.Errorf("a call that names no transcript answered %q, want ask", got)
	}
}

// The ordinary reads of a lane ask nothing, and a call that makes a link asks, as the hook reads them from the policy
// file the worker wrote.
func TestTheHookLeavesOrdinaryReadsAloneAndAsksForALink(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(base, "lane")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	path := policyFile(t, lanes.CompilePolicy(lanes.PolicySpec{Worktree: worktree, Home: "/home/person", AskReadRoots: []string{worktree}, GitListingsInside: true}))
	for command, want := range map[string]string{
		"git ls-files | xargs wc -l":                "",
		"rg -l TODO | xargs sed -n 1p":              "",
		"for f in *.go; do head -5 \"$f\"; done":    "",
		"F=go.mod; cat $F":                          "",
		"ln -s /etc e && cat e/passwd":              "ask",
		"find -L . -type f | xargs cat":             "ask",
		"perl -ne print /etc/passwd":                "ask",
		"F=$(echo x); cat $F":                       "ask",
		"git ls-files | xargs -a /etc/list cat":     "ask",
		"for f in /etc/*.conf; do cat \"$f\"; done": "ask",
	} {
		if got := decideAs(t, path, worktree, "Bash", "", map[string]string{"command": command}); got != want {
			t.Errorf("%q answered %q, want %q", command, got, want)
		}
	}
}
