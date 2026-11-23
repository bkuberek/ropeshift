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
	base := t.TempDir()
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
