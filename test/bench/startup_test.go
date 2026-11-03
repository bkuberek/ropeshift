//go:build bench

// Package bench measures the real binary. It is behind the bench tag, because a
// timing budget belongs to a run on a quiet machine and not to every commit.
package bench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/test/doubles"
)

const (
	startupRuns   = 200
	startupBudget = 50 * time.Millisecond // NFR-PERF-1
	daemonRuns    = 20
	daemonBudget  = time.Second // NFR-PERF-1
)

// benchBinary is the binary a bench times: the prebuilt one when a bundle runs, and otherwise one built
// from cmd into dir.
func benchBinary(t *testing.T, dir, name, cmd string) string {
	t.Helper()
	binary, err := doubles.Binary(dir, name, "ropeshift.dev/ropeshift/"+cmd)
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestCommandStartup(t *testing.T) {
	binary := benchBinary(t, t.TempDir(), doubles.PrebuiltRopeshift, "cmd/ropeshift")
	samples := make([]time.Duration, 0, startupRuns)
	for range startupRuns {
		start := time.Now()
		if err := exec.Command(binary, "version", "--json").Run(); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	slices.Sort(samples)
	p50, p99 := samples[len(samples)/2], samples[len(samples)*99/100]
	t.Logf("runs=%d p50=%v p90=%v p95=%v p99=%v max=%v", len(samples), p50, samples[len(samples)*90/100], samples[len(samples)*95/100], p99, samples[len(samples)-1])
	if p99 > startupBudget {
		t.Errorf("p99 %v exceeds %v", p99, startupBudget)
	}
}

// TestTopStartsWithinTheBudget times `ropeshift top` from exec to a frame read from a real daemon, on
// the binary that carries the terminal library, so that library's cost lands here and on every other
// command that shares the binary.
func TestTopStartsWithinTheBudget(t *testing.T) {
	dir := t.TempDir()
	binary := benchBinary(t, dir, doubles.PrebuiltRopeshift, "cmd/ropeshift")
	daemon := benchBinary(t, dir, doubles.PrebuiltDaemon, "cmd/ropeshiftd")
	state, err := os.MkdirTemp("", "rsb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	paths := api.Paths{StateDir: filepath.Join(state, "state"), RunDir: filepath.Join(state, "run")}
	env := []string{"HOME=" + state, "PATH=" + doubles.InstallFakeClaude(t, "no-session"), api.EnvStateDir + "=" + paths.StateDir, api.EnvRunDir + "=" + paths.RunDir, api.EnvWeb + "=" + api.WebOff,
		api.EnvTelemetry + "=127.0.0.1:0"}
	cmd := exec.Command(daemon, "solo")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	client := api.NewUnixClient(paths.ControlSocket())
	defer client.Close()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Health(context.Background()); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	samples := make([]time.Duration, 0, startupRuns)
	for range startupRuns {
		frame := exec.Command(binary, "top", "--once", "--socket", paths.ControlSocket())
		frame.Env = env
		start := time.Now()
		if err := frame.Run(); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	slices.Sort(samples)
	p50, p99 := samples[len(samples)/2], samples[len(samples)*99/100]
	t.Logf("runs=%d p50=%v p90=%v p95=%v p99=%v max=%v", len(samples), p50, samples[len(samples)*90/100], samples[len(samples)*95/100], p99, samples[len(samples)-1])
	if p99 > startupBudget {
		t.Errorf("p99 %v exceeds %v", p99, startupBudget)
	}
}

// TestDaemonAcceptsWithinOneSecond times a real ropeshiftd from exec to its first
// answer on the socket, on a new installation, so the time includes the migrations.
func TestDaemonAcceptsWithinOneSecond(t *testing.T) {
	binary := benchBinary(t, t.TempDir(), doubles.PrebuiltDaemon, "cmd/ropeshiftd")
	samples := make([]time.Duration, 0, daemonRuns)
	for range daemonRuns {
		samples = append(samples, timeDaemon(t, binary))
	}
	slices.Sort(samples)
	worst := samples[len(samples)-1]
	t.Logf("runs=%d p50=%v p90=%v max=%v", len(samples), samples[len(samples)/2], samples[len(samples)*90/100], worst)
	if worst > daemonBudget {
		t.Errorf("slowest start %v exceeds %v", worst, daemonBudget)
	}
}

func timeDaemon(t *testing.T, binary string) time.Duration {
	t.Helper()
	dir, err := os.MkdirTemp("", "rsb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	paths := api.Paths{StateDir: filepath.Join(dir, "state"), RunDir: filepath.Join(dir, "run")}
	cmd := exec.Command(binary, "solo")
	cmd.Env = []string{"HOME=" + dir, "PATH=" + doubles.InstallFakeClaude(t, "no-session"), api.EnvStateDir + "=" + paths.StateDir, api.EnvRunDir + "=" + paths.RunDir, api.EnvWeb + "=" + api.WebOff,
		api.EnvTelemetry + "=127.0.0.1:0"}
	client := api.NewUnixClient(paths.ControlSocket())
	defer client.Close()
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	deadline := start.Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := client.Health(ctx)
		cancel()
		if err == nil {
			return time.Since(start)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the daemon never answered")
	return 0
}
