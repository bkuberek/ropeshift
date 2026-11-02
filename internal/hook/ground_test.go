package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
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
	run(cfg, strings.NewReader(`{"session_id":"s2","source":"clear"}`), &out)
	if !strings.Contains(out.String(), "generation 2") {
		t.Errorf("the session heard %s", out.String())
	}
	if dialDeadline+groundAck >= hardDeadline {
		t.Errorf("a re-grounding hook can pass its bound: %v and %v of %v", dialDeadline, groundAck, hardDeadline)
	}
}
