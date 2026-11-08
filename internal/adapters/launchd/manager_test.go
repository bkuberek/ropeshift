package launchd_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/launchd"
	"ropeshift.dev/ropeshift/internal/ports"
)

const launchctl = "/bin/launchctl"

// launchd answers as a domain that holds the labels in loaded, which a bootout lets go at once, and
// fails a bootstrap when failing.
func manager(t *testing.T, loaded []string, failing bool) (launchd.Manager, *fake.ProcessRunner) {
	t.Helper()
	runner := fake.NewProcessRunner()
	var mu sync.Mutex
	loaded = slices.Clone(loaded)
	runner.Install(launchctl, func(spec ports.ProcessSpec, _ <-chan struct{}) int {
		mu.Lock()
		defer mu.Unlock()
		label := strings.TrimPrefix(spec.Args[1], "gui/501/")
		switch spec.Args[0] {
		case "print":
			if slices.Contains(loaded, label) {
				return 0
			}
			return 113
		case "bootout":
			loaded = slices.DeleteFunc(loaded, func(held string) bool { return held == label })
		case "bootstrap":
			if failing {
				return 5
			}
			loaded = append(loaded, strings.TrimSuffix(filepath.Base(spec.Args[2]), ".plist"))
		}
		return 0
	})
	return launchd.Manager{Runner: runner, Tool: launchctl, Domain: "gui/501", Dir: filepath.Join(t.TempDir(), "LaunchAgents"), Env: []string{"HOME=/home/u"}}, runner
}

func calls(runner *fake.ProcessRunner) []string {
	var out []string
	for _, spec := range runner.Spawned() {
		out = append(out, strings.Join(spec.Args, " "))
	}
	return out
}

func TestInstallWritesTheAgentAndLoadsIt(t *testing.T) {
	m, runner := manager(t, nil, false)
	file, err := m.Install(t.Context(), "test.agent", []byte("plist"))
	if err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(m.Dir, "test.agent.plist") {
		t.Errorf("file %s", file)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the agent's file: %v, %v", info, err)
	}
	if raw, _ := os.ReadFile(file); string(raw) != "plist" {
		t.Errorf("the file holds %q", raw)
	}
	if got, want := calls(runner), []string{"print gui/501/test.agent", "bootstrap gui/501 " + file}; !slices.Equal(got, want) {
		t.Errorf("calls %q", got)
	}
	if spec := runner.Spawned()[0]; !slices.Equal(spec.Env, m.Env) {
		t.Errorf("launchctl's environment %q", spec.Env)
	}
}

// A second install replaces the agent launchd holds, because a bootstrap of a loaded label fails.
func TestInstallReplacesALoadedAgent(t *testing.T) {
	m, runner := manager(t, []string{"test.agent"}, false)
	file, err := m.Install(t.Context(), "test.agent", []byte("plist"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"print gui/501/test.agent", "bootout gui/501/test.agent", "print gui/501/test.agent", "bootstrap gui/501 " + file}
	if got := calls(runner); !slices.Equal(got, want) {
		t.Errorf("calls %q", got)
	}
}

// launchd lets a label go a moment after bootout returns, and a bootstrap before then fails, so a
// manager waits until the domain no longer holds it.
func TestAnUnloadWaitsForLaunchdToLetTheLabelGo(t *testing.T) {
	runner := fake.NewProcessRunner()
	var mu sync.Mutex
	lingering := -1
	runner.Install(launchctl, func(spec ports.ProcessSpec, _ <-chan struct{}) int {
		mu.Lock()
		defer mu.Unlock()
		switch spec.Args[0] {
		case "print":
			if lingering == 0 {
				return 113
			}
			if lingering > 0 {
				lingering--
			}
			return 0
		case "bootout":
			lingering = 3
		case "bootstrap":
			if lingering != 0 {
				return 5
			}
		}
		return 0
	})
	m := launchd.Manager{Runner: runner, Tool: launchctl, Domain: "gui/501", Dir: t.TempDir()}
	file, err := m.Install(t.Context(), "test.agent", []byte("plist"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"print gui/501/test.agent", "bootout gui/501/test.agent", "print gui/501/test.agent", "print gui/501/test.agent", "print gui/501/test.agent", "print gui/501/test.agent", "bootstrap gui/501 " + file}
	if got := calls(runner); !slices.Equal(got, want) {
		t.Errorf("calls %q", got)
	}
}

// A label launchd never lets go of is an error, and nothing loads in its place.
func TestALabelThatStaysIsAnError(t *testing.T) {
	runner := fake.NewProcessRunner()
	runner.Install(launchctl, func(ports.ProcessSpec, <-chan struct{}) int { return 0 })
	m := launchd.Manager{Runner: runner, Tool: launchctl, Domain: "gui/501", Dir: t.TempDir(), Patience: 50 * time.Millisecond}
	if _, err := m.Install(t.Context(), "test.agent", []byte("plist")); err == nil {
		t.Error("installed over a label that never went")
	}
	if slices.ContainsFunc(calls(runner), func(call string) bool { return strings.HasPrefix(call, "bootstrap") }) {
		t.Errorf("calls %q", calls(runner))
	}
}

func TestAFailedLoadIsAnError(t *testing.T) {
	m, _ := manager(t, nil, true)
	if _, err := m.Install(t.Context(), "test.agent", []byte("plist")); err == nil {
		t.Error("a bootstrap that failed reported success")
	}
}

func TestUninstallUnloadsTheAgentAndRemovesItsFile(t *testing.T) {
	m, runner := manager(t, []string{"test.agent"}, false)
	file, err := m.Install(t.Context(), "test.agent", []byte("plist"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(runner.Spawned())
	removed, err := m.Uninstall(t.Context(), "test.agent")
	if err != nil {
		t.Fatal(err)
	}
	if removed != file {
		t.Errorf("removed %s", removed)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file stayed: %v", err)
	}
	want := []string{"print gui/501/test.agent", "bootout gui/501/test.agent", "print gui/501/test.agent"}
	if got := calls(runner)[before:]; !slices.Equal(got, want) {
		t.Errorf("calls %q", got)
	}
}

func TestUninstallOfNothingIsNotFound(t *testing.T) {
	m, _ := manager(t, nil, false)
	if _, err := m.Uninstall(t.Context(), "test.agent"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err %v", err)
	}
}
