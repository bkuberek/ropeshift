//go:build bench

// Package bench measures the real binary. It is behind the bench tag, because a
// timing budget belongs to a run on a quiet machine and not to every commit.
package bench

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const (
	startupRuns   = 200
	startupBudget = 50 * time.Millisecond // NFR-PERF-1
)

func TestCommandStartup(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ropeshift")
	build := exec.Command("go", "build", "-o", binary, "ropeshift.dev/ropeshift/cmd/ropeshift")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
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
