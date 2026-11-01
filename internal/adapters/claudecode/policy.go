package claudecode

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// usualDefaults are guarded whatever a repository reports, because a failed read of its
// default branch must not open it.
var usualDefaults = []string{"main", "master"}

// promptlessModes are the modes in which a call does not prompt, so a gate needs its own
// `ask` rule for `PermissionRequest` to fire at all.
var promptlessModes = []string{"auto", "bypassPermissions"}

// maxEditRules bounds what a generation's settings hold. The nearest levels come first,
// because other lanes and the repository sit there, and the hook covers what this drops.
const maxEditRules = 2000

func defaultBranches(spec ports.SessionSpec) []string {
	branches := slices.Clone(usualDefaults)
	if spec.DefaultBranch != "" && !slices.Contains(branches, spec.DefaultBranch) {
		branches = append(branches, spec.DefaultBranch)
	}
	return branches
}

func homeOf(spec ports.SessionSpec) string {
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			return value
		}
	}
	return ""
}

// compilePolicy builds the file a generation's hook reads, so a denial needs no worker
// (FR-AUTH-8). It runs before the session starts, because the hook touches nothing else.
func compilePolicy(spec ports.SessionSpec) lanes.DenyPolicy {
	worktree := spec.Dir
	real, err := filepath.EvalSymlinks(spec.Dir)
	if err != nil {
		real = worktree
	}
	var writable []string
	if spec.TempDir != "" {
		writable = append(writable, spec.TempDir)
	}
	return lanes.CompilePolicy(lanes.PolicySpec{
		Worktree: worktree, WorktreeReal: real, Home: homeOf(spec), DefaultBranches: defaultBranches(spec), Writable: writable,
	})
}

// denyRules writes each denial as a deny rule too: a `Bash` rule for a command, and an
// `Edit` rule for a path, because a `Bash` rule does not match a redirect target.
func denyRules(spec ports.SessionSpec) []string {
	rules := []string{"Bash(git push --force*)", "Bash(git push -f*)", "Bash(git push * --force*)", "Bash(git push * -f*)"}
	for _, branch := range defaultBranches(spec) {
		rules = append(rules, "Bash(git push * "+branch+")", "Bash(git push * *:"+branch+")", "Bash(git push * +"+branch+")")
	}
	return append(rules, editRules(spec.Dir)...)
}

// editRules names what lies beside the worktree at each level above it, because no rule
// can say "everything but this directory", and a deny always beats an allow. Something
// created later is the hook's to catch. A name a rule cannot hold is the hook's too.
func editRules(worktree string) []string {
	if worktree == "" {
		return nil
	}
	dir, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		dir = worktree
	}
	var rules []string
	for dir != string(filepath.Separator) && dir != "." {
		parent := filepath.Dir(dir)
		entries, err := os.ReadDir(parent)
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if name == filepath.Base(dir) || strings.ContainsAny(name, "()[]{}*?!\\") {
					continue
				}
				rules = append(rules, "Edit(/"+filepath.Join(parent, name)+")", "Edit(/"+filepath.Join(parent, name)+"/**)")
			}
		}
		dir = parent
	}
	if len(rules) > maxEditRules {
		rules = rules[:maxEditRules]
	}
	return rules
}

// askRules gates what a profile names, on a lane that would otherwise not ask.
func askRules(spec ports.SessionSpec) []string {
	if !slices.Contains(promptlessModes, spec.PermissionMode) {
		return nil
	}
	return slices.Clone(spec.Gates)
}
