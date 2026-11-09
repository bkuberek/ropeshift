package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
)

// slowWorker answers one line after a delay, as a worker does with commits ahead of its answer.
func slowWorker(t *testing.T, delay time.Duration, reply lanes.Reply) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "w.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
			return
		}
		time.Sleep(delay)
		raw, err := json.Marshal(reply)
		if err == nil {
			_, _ = conn.Write(append(raw, '\n'))
		}
	}()
	return socket
}

// A clear's answer follows two journal commits, and a session that hears no answer starts with no
// generation, which nothing else tells it. A hook that re-grounds a session gets most of its time
// for that answer, and stays inside the bound a hook is held to.
func TestAClearedSessionHearsItsGenerationFromASlowWorker(t *testing.T) {
	cfg := config{
		event: eventSessionStart, socket: slowWorker(t, 40*time.Millisecond, lanes.Reply{OK: true, Generation: 2}), token: "a-token",
		lane: "lane-1", workOrder: "wo", generation: 1,
	}
	var out bytes.Buffer
	event, input := take(cfg, strings.NewReader(`{"session_id":"s2","source":"clear"}`))
	run(cfg, event, input, &out)
	if !strings.Contains(out.String(), "generation 2") {
		t.Errorf("the session heard %s", out.String())
	}
	if dialDeadline+groundAck >= hardDeadline {
		t.Errorf("a re-grounding hook can pass its bound: %v and %v of %v", dialDeadline, groundAck, hardDeadline)
	}
}

// silentWorker takes one line and answers nothing, as a worker held up on a loaded machine does. It
// says when it has the line, which a hook sends once it waits on the answer.
func silentWorker(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "w.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	taken, released := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(released)
		_ = listener.Close()
	})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
			return
		}
		close(taken)
		<-released
	}()
	return socket, taken
}

// lockedBuffer is a hook's stdout, which its deadline and its answer can both write.
type lockedBuffer struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.String()
}

// A loaded machine can hold a re-grounding hook past its deadline while it waits on the worker. The
// session still hears who it is, because that comes from the policy file and needs no worker, and a
// clear's session hears no generation rather than a wrong one. It hears one answer, and the event
// reaches the spool once.
func TestARegroundingHookPastItsDeadlineStillGroundsTheSession(t *testing.T) {
	socket, taken := silentWorker(t)
	cfg := config{
		event: eventSessionStart, socket: socket, spool: filepath.Join(t.TempDir(), "spool.jsonl"), token: "a-token",
		lane: "lane-1", workOrder: "wo", generation: 1, out: new(outbox),
	}
	event, input := take(cfg, strings.NewReader(`{"session_id":"s2","source":"clear"}`))
	var out lockedBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(cfg, event, input, &out)
	}()
	select {
	case <-taken:
	case <-time.After(5 * time.Second):
		t.Fatal("the hook never asked the worker")
	}
	cfg.out.settle(cfg)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the hook never finished after its deadline, and the session heard %q", out.String())
	}
	var answer struct {
		Specific struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out.String()), &answer); err != nil {
		t.Fatalf("the session heard %q: %v", out.String(), err)
	}
	if context := answer.Specific.Context; !strings.Contains(context, "lane-1") || !strings.Contains(context, "wo") ||
		strings.Contains(context, "generation") {
		t.Errorf("the session heard %q", context)
	}
	raw, err := os.ReadFile(cfg.spool)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 1 {
		t.Errorf("the spool holds %d lines", lines)
	}
}
