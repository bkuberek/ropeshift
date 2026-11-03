//go:build bench

package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/test/doubles"
)

const (
	hookBudget = 100 * time.Millisecond // NFR-PERF-2
	hookRuns   = 1000
	hookToken  = "a-bench-token"
)

// hookScenario is one row of the T5 table, on the real binary.
type hookScenario struct {
	name        string
	event       string
	worker      string // "", "up", "hang", or "stale"
	payload     []byte
	concurrency int
	policy      bool
	viaShell    bool
	baseline    bool
}

// benchDenial is a call a compiled policy has to read to the end of a command line to
// deny, which is the work the budget holds.
const benchDenial = `{"session_id":"s","cwd":"/lanes/bench","tool_name":"Bash","tool_use_id":"t","tool_input":{"command":"go build ./... && go test ./... 2>&1 | tee build.log; git push -f origin HEAD:main"}}`

func toolPayload(size int) []byte {
	head := `{"session_id":"00000000-0000-4000-8000-000000000000","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"echo bench"},"tool_response":{"stdout":"`
	tail := `"}}`
	return []byte(head + strings.Repeat("x", max(0, size-len(head)-len(tail))) + tail)
}

func hookScenarios() []hookScenario {
	small, large := toolPayload(600), toolPayload(256<<10)
	return []hookScenario{
		{name: "baseline-true", baseline: true, concurrency: 1},
		{name: "worker-up", event: "PostToolUse", worker: "up", payload: small, concurrency: 1},
		{name: "worker-up-via-sh", event: "PostToolUse", worker: "up", payload: small, concurrency: 1, viaShell: true},
		{name: "worker-up-256k", event: "PostToolUse", worker: "up", payload: large, concurrency: 1},
		{name: "worker-up-parallel-8", event: "PostToolUse", worker: "up", payload: small, concurrency: 8},
		{name: "worker-down-no-socket", event: "PostToolUse", payload: small, concurrency: 1},
		{name: "worker-down-stale-socket", event: "PostToolUse", worker: "stale", payload: small, concurrency: 1},
		{name: "worker-down-256k", event: "PostToolUse", payload: large, concurrency: 1},
		{name: "worker-down-parallel-8", event: "PostToolUse", payload: small, concurrency: 8},
		{name: "worker-hung", event: "PostToolUse", worker: "hang", payload: small, concurrency: 1},
		{name: "malformed-input", event: "Stop", payload: []byte("{not json"), concurrency: 1},
		{name: "policy-deny-worker-down", event: "PreToolUse", payload: []byte(benchDenial), concurrency: 1, policy: true},
	}
}

// TestHookLatency times the real binary as a process, from start to exit, as an agent
// CLI experiences it. No session is involved.
func TestHookLatency(t *testing.T) {
	binary := benchBinary(t, t.TempDir(), doubles.PrebuiltRopeshift, "cmd/ropeshift")
	worst := time.Duration(0)
	for _, s := range hookScenarios() {
		t.Run(s.name, func(t *testing.T) {
			p99 := measureHook(t, binary, s)
			if !s.baseline {
				worst = max(worst, p99)
			}
		})
	}
	load, _ := exec.Command("/usr/bin/uptime").Output()
	t.Logf("worst p99 %v, machine: %s", worst, bytes.TrimSpace(load))
	if worst >= hookBudget {
		t.Errorf("worst p99 %v reaches %v", worst, hookBudget)
	}
}

func measureHook(t *testing.T, binary string, s hookScenario) time.Duration {
	t.Helper()
	dir, err := os.MkdirTemp("", "rsh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket, spool := filepath.Join(dir, "w.sock"), filepath.Join(dir, "spool.jsonl")
	stop := startHookWorker(t, s.worker, dir, socket)
	defer stop()
	env := []string{
		api.EnvWorkerSocket + "=" + socket, api.EnvSpool + "=" + spool, api.EnvLaneToken + "=" + hookToken,
		api.EnvLaneID + "=lane-1", api.EnvGeneration + "=1", api.EnvWorkOrder + "=wo",
	}
	if s.policy {
		policy := filepath.Join(dir, "policy.json")
		if err := os.WriteFile(policy, []byte(`{"deny":[],"roots":["/lanes/bench"],"default_branches":["main","master"]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		env = append(env, api.EnvPolicy+"="+policy)
	}
	bin, argv := binary, []string{"hook", s.event}
	switch {
	case s.baseline:
		bin, argv = "/usr/bin/true", nil
	case s.viaShell:
		bin, argv = "/bin/sh", []string{"-c", binary + " hook " + s.event}
	}
	times := make([]time.Duration, hookRuns)
	var failed sync.Mutex
	nonZero := 0
	var group sync.WaitGroup
	next := make(chan int)
	for range max(1, s.concurrency) {
		group.Go(func() {
			for i := range next {
				cmd := exec.Command(bin, argv...)
				cmd.Env = env
				cmd.Stdin = bytes.NewReader(s.payload)
				start := time.Now()
				err := cmd.Run()
				times[i] = time.Since(start)
				if err != nil {
					failed.Lock()
					nonZero++
					failed.Unlock()
				}
			}
		})
	}
	for i := range hookRuns {
		next <- i
	}
	close(next)
	group.Wait()
	sorted := slices.Sorted(slices.Values(times))
	p50, p99, worst := sorted[hookRuns/2], sorted[hookRuns*99/100], sorted[hookRuns-1]
	over := 0
	for _, value := range times {
		if value >= hookBudget {
			over++
		}
	}
	good, bad := countSpool(t, spool)
	t.Logf("p50=%v p99=%v max=%v over_100ms=%d spooled=%d", p50, p99, worst, over, good)
	if nonZero != 0 {
		t.Errorf("%d runs exited non-zero", nonZero)
	}
	if bad != 0 {
		t.Errorf("%d corrupt spool lines", bad)
	}
	return p99
}

func countSpool(t *testing.T, path string) (good, bad int) {
	t.Helper()
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		if json.Valid(scanner.Bytes()) {
			good++
		} else {
			bad++
		}
	}
	return good, bad
}

// startHookWorker serves the real worker socket on a real journal file, so the time
// includes the write that makes an ack mean something.
func startHookWorker(t *testing.T, kind, dir, socket string) func() {
	t.Helper()
	switch kind {
	case "up":
		journal, err := sqlite.OpenJournal(filepath.Join(dir, "journal.db"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := journal.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		err = journal.SaveLane(ctx, lanes.Record{
			ID: "lane-1", WorkOrder: "wo", ClaimID: "c1", Fence: 1, Generation: 1,
			TokenDigest: lanes.TokenDigest(hookToken), State: lanes.StateRunning,
		})
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		subject := worker.New(&fake.WorkerLink{}, journal, clock.System{}, worker.Config{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = subject.ServeLanes(ctx, listener)
		}()
		return func() {
			cancel()
			<-done
			_ = journal.Close()
		}
	case "hang":
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var held []net.Conn
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				held = append(held, conn)
				mu.Unlock()
			}
		}()
		return func() {
			_ = listener.Close()
			mu.Lock()
			defer mu.Unlock()
			for _, conn := range held {
				_ = conn.Close()
			}
		}
	case "stale":
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		listener.(*net.UnixListener).SetUnlinkOnClose(false)
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return func() {}
}

var _ = fmt.Sprint
