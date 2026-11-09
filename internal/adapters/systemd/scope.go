// Package systemd is the service manager on Linux: a user unit that runs the daemon, and a
// transient scope for each process that must outlive that unit (D20).
package systemd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// RuntimeVariable names the user manager's runtime directory, through which systemd-run reaches it.
const RuntimeVariable = "XDG_RUNTIME_DIR"

// Scope starts each process that must outlive the worker's service in a transient scope of the
// user's service manager. A stop of a unit ends every process in its control group, and a scope is
// a group of its own. systemd-run execs the process in place once its scope exists, so the process
// ID the runner reports is the process's own. Every other spawn passes through unchanged.
type Scope struct {
	Runner ports.ProcessRunner
	// Tool is systemd-run's absolute path, and Env is env's, which takes back out of the process's
	// environment what only systemd-run needed.
	Tool, Env string
	// RuntimeDir is the user manager's runtime directory.
	RuntimeDir string
}

// Start starts a process, in a scope of its own when it must outlive the worker's service.
func (s Scope) Start(ctx context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	if !spec.Outlive {
		return s.Runner.Start(ctx, spec)
	}
	if err := lanes.CheckSpawn(spec.Path); err != nil {
		return nil, err
	}
	env := spec.Env
	if !holds(spec.Env, RuntimeVariable) {
		env = append(slices.Clone(spec.Env), RuntimeVariable+"="+s.RuntimeDir)
	}
	var unset []string
	for _, name := range scopeVariables {
		if !holds(spec.Env, name) {
			unset = append(unset, "-u", name)
		}
	}
	args := []string{"--user", "--scope", "--quiet", "--collect", "--"}
	if len(unset) > 0 {
		// env reads a word that holds = as a variable to set, so it would never run such a path.
		if strings.Contains(spec.Path, "=") {
			return nil, fmt.Errorf("a scope cannot start %q, because its path holds =", spec.Path)
		}
		args = append(append(args, s.Env), unset...)
	}
	scoped := spec
	scoped.Path, scoped.Args, scoped.Env = s.Tool, append(append(args, spec.Path), spec.Args...), env
	return s.Runner.Start(ctx, scoped)
}

// scopeVariables are what the process would get from systemd-run and not from its spec: the runtime
// directory systemd-run needs, and the scope's invocation, which systemd-run sets itself.
var scopeVariables = []string{"INVOCATION_ID", RuntimeVariable}

func holds(env []string, name string) bool {
	return slices.ContainsFunc(env, func(entry string) bool { return strings.HasPrefix(entry, name+"=") })
}

// UserScope finds systemd-run and env on the worker's PATH, and a user manager that listens in
// its runtime directory. Anything missing leaves no scope, and a process that must outlive the
// worker then has only a session of its own.
func UserScope(runner ports.ProcessRunner, getenv func(string) string) (Scope, bool) {
	runtimeDir := getenv(RuntimeVariable)
	if runtimeDir == "" {
		return Scope{}, false
	}
	info, err := os.Stat(filepath.Join(runtimeDir, "systemd", "private"))
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return Scope{}, false
	}
	tool, toolFound := lookPath("systemd-run", getenv("PATH"))
	env, envFound := lookPath("env", getenv("PATH"))
	if !toolFound || !envFound {
		return Scope{}, false
	}
	return Scope{Runner: runner, Tool: tool, Env: env, RuntimeDir: runtimeDir}, true
}

// lookPath finds an executable file in the absolute directories of a PATH.
func lookPath(name, path string) (string, bool) {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, true
		}
	}
	return "", false
}

var _ ports.ProcessRunner = Scope{}
