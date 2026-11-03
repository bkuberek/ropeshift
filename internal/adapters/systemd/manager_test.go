package systemd_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/systemd"
	"ropeshift.dev/ropeshift/internal/ports"
)

const systemctl = "/usr/bin/systemctl"

// systemctl answers every call, and fails the one named in failing.
func manager(t *testing.T, failing string) (systemd.Manager, *fake.ProcessRunner) {
	t.Helper()
	runner := fake.NewProcessRunner()
	runner.Install(systemctl, func(spec ports.ProcessSpec, _ <-chan struct{}) int {
		if spec.Args[1] == failing {
			return 1
		}
		return 0
	})
	return systemd.Manager{Runner: runner, Tool: systemctl, Dir: filepath.Join(t.TempDir(), "systemd", "user"), Env: []string{"XDG_RUNTIME_DIR=/run/user/1000"}}, runner
}

func calls(runner *fake.ProcessRunner) []string {
	var out []string
	for _, spec := range runner.Spawned() {
		out = append(out, strings.Join(spec.Args, " "))
	}
	return out
}

// Install writes the unit, makes the manager read it, enables it for the next login, and starts it,
// or restarts one already running, so the daemon runs the definition just written.
func TestInstallWritesTheUnitAndStartsIt(t *testing.T) {
	m, runner := manager(t, "")
	file, err := m.Install(t.Context(), "ropeshiftd", []byte("unit"))
	if err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(m.Dir, "ropeshiftd.service") {
		t.Errorf("file %s", file)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the unit's file: %v, %v", info, err)
	}
	if raw, _ := os.ReadFile(file); string(raw) != "unit" {
		t.Errorf("the file holds %q", raw)
	}
	want := []string{"--user daemon-reload", "--user enable ropeshiftd.service", "--user restart ropeshiftd.service"}
	if got := calls(runner); !slices.Equal(got, want) {
		t.Errorf("calls %q", got)
	}
	if spec := runner.Spawned()[0]; !slices.Equal(spec.Env, m.Env) {
		t.Errorf("systemctl's environment %q", spec.Env)
	}
}

func TestAFailedStartIsAnError(t *testing.T) {
	m, _ := manager(t, "restart")
	if _, err := m.Install(t.Context(), "ropeshiftd", []byte("unit")); err == nil {
		t.Error("a start that failed reported success")
	}
}

func TestUninstallStopsTheUnitAndRemovesItsFile(t *testing.T) {
	m, runner := manager(t, "")
	file, err := m.Install(t.Context(), "ropeshiftd", []byte("unit"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(runner.Spawned())
	removed, err := m.Uninstall(t.Context(), "ropeshiftd")
	if err != nil {
		t.Fatal(err)
	}
	if removed != file {
		t.Errorf("removed %s", removed)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file stayed: %v", err)
	}
	if got, want := calls(runner)[before:], []string{"--user disable --now ropeshiftd.service", "--user daemon-reload"}; !slices.Equal(got, want) {
		t.Errorf("calls %q", got)
	}
}

func TestUninstallOfNothingIsNotFound(t *testing.T) {
	m, runner := manager(t, "")
	if _, err := m.Uninstall(t.Context(), "ropeshiftd"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err %v", err)
	}
	if len(runner.Spawned()) != 0 {
		t.Errorf("calls %q", calls(runner))
	}
}
