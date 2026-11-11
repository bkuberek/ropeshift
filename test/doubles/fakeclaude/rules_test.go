package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The double reads rules as the permissions page documents them, so an exit test that leans on it shows
// what the CLI would do: these rows come from that page's wildcard table and its symlink rules.
func TestTheDoubleReadsRulesAsTheCLIDocumentsThem(t *testing.T) {
	var s settings
	s.Permissions.Allow = []string{"Bash(git commit *)", "Bash(ls *)", "Bash(rm *)"}
	s.Permissions.Ask = []string{"Bash(git push *)"}
	s.Permissions.Deny = []string{"Bash(git push --force*)"}
	commands := []struct {
		command string
		want    ruling
	}{
		{"ls", ruleAllows},
		{"ls -la", ruleAllows},
		{"lsof", rulePrompts},
		{"git commit -m x && rm a", ruleAllows},
		{"git commit -m x && curl example.com", rulePrompts},
		{"git push", rulePrompts},
		{"rm a; git push origin HEAD", rulePrompts},
		{"git push --force origin HEAD", ruleDenies},
		{"git -C . push", rulePrompts},
	}
	for _, tt := range commands {
		if got := s.ruleCommand(tt.command); got != tt.want {
			t.Errorf("%q: %v, want %v", tt.command, got, tt.want)
		}
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree, outside := filepath.Join(root, "lane-1"), filepath.Join(root, "outside")
	for _, dir := range []string{worktree, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nothing"), filepath.Join(worktree, "dangling")); err != nil {
		t.Fatal(err)
	}
	var files settings
	files.Permissions.Allow = []string{"Edit(/" + worktree + "/**)"}
	files.Permissions.Deny = []string{"Edit(/" + filepath.Join(root, "kept") + "/**)"}
	paths := []struct {
		path string
		want ruling
	}{
		{filepath.Join(worktree, "a.go"), ruleAllows},
		{filepath.Join(worktree, "new", "dir", "a.go"), ruleAllows},
		{filepath.Join(worktree, "out", "a.go"), rulePrompts},
		{filepath.Join(outside, "a.go"), rulePrompts},
		{filepath.Join(root, "kept", "a.go"), ruleDenies},
		{filepath.Join(worktree, "dangling"), ruleDenies},
	}
	for _, tt := range paths {
		if got := files.ruleFile("Write", tt.path); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.path, got, tt.want)
		}
	}
}
