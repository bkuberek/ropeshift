package systemd_test

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/systemd"
	"ropeshift.dev/ropeshift/internal/ports"
)

const (
	tool = "/usr/bin/systemd-run"
	env  = "/usr/bin/env"
	run  = "/run/user/1000"
)

func scoped(t *testing.T) (systemd.Scope, *fake.ProcessRunner) {
	t.Helper()
	runner := fake.NewProcessRunner()
	runner.Install(tool, fake.Block)
	runner.Install("/bin/true", fake.Block)
	return systemd.Scope{Runner: runner, Tool: tool, Env: env, RuntimeDir: run}, runner
}

// A process that must outlive the worker starts in a scope of its own, through systemd-run, which needs
// the user manager's runtime directory. env takes that variable back out, so the process sees exactly
// its spec's environment, and what it reads and writes stays its own.
func TestAnOutlivingProcessStartsInAScope(t *testing.T) {
	scope, runner := scoped(t)
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("")
	spec := ports.ProcessSpec{
		Path: "/state/bin/ropeshift", Args: []string{"lane-shim", "--dir", "/state/lanes/l1"}, Dir: "/work",
		Env: []string{"HOME=/home/u", "PATH=/usr/bin"}, Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Detached: true, Outlive: true,
	}
	process, err := scope.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	_ = process.Signal(ports.SignalKill)
	got := runner.Spawned()[0]
	wantArgs := []string{
		"--user", "--scope", "--quiet", "--collect", "--",
		env, "-u", "INVOCATION_ID", "-u", "XDG_RUNTIME_DIR", "/state/bin/ropeshift", "lane-shim", "--dir", "/state/lanes/l1",
	}
	if got.Path != tool || !slices.Equal(got.Args, wantArgs) {
		t.Errorf("started %s %q", got.Path, got.Args)
	}
	if want := []string{"HOME=/home/u", "PATH=/usr/bin", "XDG_RUNTIME_DIR=" + run}; !slices.Equal(got.Env, want) {
		t.Errorf("environment %q", got.Env)
	}
	if got.Dir != "/work" || got.Stdin != stdin || got.Stdout != &stdout || got.Stderr != &stderr || !got.Detached {
		t.Errorf("the spec lost a field: %+v", got)
	}
	if !slices.Equal(spec.Env, []string{"HOME=/home/u", "PATH=/usr/bin"}) {
		t.Errorf("the caller's environment changed: %q", spec.Env)
	}
}

// systemd-run adds INVOCATION_ID to the process's environment too, so env takes out each variable the
// spec does not hold, and only those.
func TestAProcessKeepsTheVariablesItsSpecHolds(t *testing.T) {
	tests := []struct {
		name  string
		env   []string
		unset []string
	}{
		{"the runtime directory", []string{"XDG_RUNTIME_DIR=/run/user/1000"}, []string{"-u", "INVOCATION_ID"}},
		{"an invocation", []string{"INVOCATION_ID=abc"}, []string{"-u", "XDG_RUNTIME_DIR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope, runner := scoped(t)
			spec := ports.ProcessSpec{Path: "/bin/true", Env: tt.env, Outlive: true}
			if _, err := scope.Start(t.Context(), spec); err != nil {
				t.Fatal(err)
			}
			got := runner.Spawned()[0]
			want := append(append([]string{"--user", "--scope", "--quiet", "--collect", "--", env}, tt.unset...), "/bin/true")
			if !slices.Equal(got.Args, want) {
				t.Errorf("args %q", got.Args)
			}
			if !slices.Contains(got.Env, tt.env[0]) {
				t.Errorf("environment %q", got.Env)
			}
		})
	}
}

func TestAProcessThatHoldsBothNeedsNoEnv(t *testing.T) {
	scope, runner := scoped(t)
	spec := ports.ProcessSpec{Path: "/bin/true", Env: []string{"INVOCATION_ID=abc", "XDG_RUNTIME_DIR=/run/user/1000"}, Outlive: true}
	if _, err := scope.Start(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	got := runner.Spawned()[0]
	if want := []string{"--user", "--scope", "--quiet", "--collect", "--", "/bin/true"}; !slices.Equal(got.Args, want) {
		t.Errorf("args %q", got.Args)
	}
	if !slices.Equal(got.Env, spec.Env) {
		t.Errorf("environment %q", got.Env)
	}
}

func TestAProcessThatEndsWithTheWorkerStartsAsItIs(t *testing.T) {
	scope, runner := scoped(t)
	spec := ports.ProcessSpec{Path: "/bin/true", Args: []string{"x"}, Env: []string{"PATH=/usr/bin"}, Detached: true}
	if _, err := scope.Start(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	got := runner.Spawned()[0]
	if got.Path != "/bin/true" || !slices.Equal(got.Args, spec.Args) || !slices.Equal(got.Env, spec.Env) || !got.Detached {
		t.Errorf("started %+v", got)
	}
}

// env reads a word that holds = as a variable to set, so a path like that would never run.
func TestAScopeRefusesWhatItCannotStart(t *testing.T) {
	scope, runner := scoped(t)
	for _, path := range []string{"/state=1/bin/ropeshift", "ropeshift", "/bin/sh"} {
		if _, err := scope.Start(t.Context(), ports.ProcessSpec{Path: path, Outlive: true}); err == nil {
			t.Errorf("%s started", path)
		}
	}
	if spawned := runner.Spawned(); len(spawned) != 0 {
		t.Errorf("spawned %+v", spawned)
	}
}

// userManager makes a runtime directory whose user manager answers, and a PATH that holds both tools.
func userManager(t *testing.T) (runtimeDir, path string) {
	t.Helper()
	runtimeDir, err := os.MkdirTemp("/tmp", "rsd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if err := os.Mkdir(filepath.Join(runtimeDir, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "systemd", "private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	path = t.TempDir()
	for _, name := range []string{"systemd-run", "env"} {
		if err := os.WriteFile(filepath.Join(path, name), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return runtimeDir, path
}

func TestAUserManagerThatAnswersGivesAScope(t *testing.T) {
	runtimeDir, path := userManager(t)
	scope, ok := systemd.UserScope(fake.NewProcessRunner(), variables{"XDG_RUNTIME_DIR": runtimeDir, "PATH": "relative:" + path}.Get)
	if !ok {
		t.Fatal("no scope")
	}
	if scope.Tool != filepath.Join(path, "systemd-run") || scope.Env != filepath.Join(path, "env") || scope.RuntimeDir != runtimeDir {
		t.Errorf("scope %+v", scope)
	}
}

func TestNoScopeWithoutAUserManager(t *testing.T) {
	runtimeDir, path := userManager(t)
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"no runtime directory", map[string]string{"PATH": path}},
		{"a runtime directory with no manager", map[string]string{"XDG_RUNTIME_DIR": t.TempDir(), "PATH": path}},
		{"no systemd-run", map[string]string{"XDG_RUNTIME_DIR": runtimeDir, "PATH": t.TempDir()}},
		{"no PATH", map[string]string{"XDG_RUNTIME_DIR": runtimeDir}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := systemd.UserScope(fake.NewProcessRunner(), variables(tt.env).Get); ok {
				t.Error("a scope with nothing to make it")
			}
		})
	}
}

type variables map[string]string

func (v variables) Get(name string) string { return v[name] }
