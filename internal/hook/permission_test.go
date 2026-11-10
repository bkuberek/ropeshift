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

	"ropeshift.com/ropeshift/internal/lanes"
)

// scriptedWorker answers each look with the next reply, the last one again if the hook looks more
// often, and keeps the lines it took.
type scriptedWorker struct {
	mu    sync.Mutex
	lines []lanes.Request
}

func (w *scriptedWorker) taken() []lanes.Request {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]lanes.Request{}, w.lines...)
}

func aScriptedWorker(t *testing.T, replies ...lanes.Reply) (string, *scriptedWorker) {
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
	worker := new(scriptedWorker)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			raw, err := bufio.NewReader(conn).ReadBytes('\n')
			if err == nil {
				var line lanes.Request
				if json.Unmarshal(raw, &line) == nil {
					worker.mu.Lock()
					worker.lines = append(worker.lines, line)
					at := min(len(worker.lines), len(replies)) - 1
					worker.mu.Unlock()
					if answer, err := json.Marshal(replies[at]); err == nil {
						_, _ = conn.Write(append(answer, '\n'))
					}
				}
			}
			_ = conn.Close()
		}
	}()
	return socket, worker
}

func waiting(id string) lanes.Reply {
	return lanes.Reply{OK: true, Approval: &lanes.ApprovalReply{Decision: lanes.ApprovalWaiting, ID: id, WaitMs: 60000}}
}

// A waiting hook names the request it was given on every look after the first. That is how a worker
// that finds the call landing somewhere else knows which request to close, and a hook that sent nothing
// would leave it open for a member to approve.
func TestAWaitingHookNamesTheRequestItHolds(t *testing.T) {
	denied := lanes.Reply{OK: true, Approval: &lanes.ApprovalReply{Decision: lanes.ApprovalDeny, Reason: lanes.ReasonRequestClosed, ID: "request-1"}}
	socket, worker := aScriptedWorker(t, waiting("request-1"), waiting("request-1"), denied)
	cfg := config{event: eventPermissionRequest, socket: socket, token: "a-token", lane: "lane-1", workOrder: "wo", generation: 1}
	var out bytes.Buffer
	event, input := take(cfg, strings.NewReader(`{"session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"a.md"}}`))
	run(cfg, event, input, &out)

	lines := worker.taken()
	if len(lines) != 3 {
		t.Fatalf("the hook looked %d times: %+v", len(lines), lines)
	}
	if first := lines[0].Approval; first == nil || first.Held != "" || first.Raised || lines[0].Hook == nil {
		t.Errorf("the first look: %+v", lines[0])
	}
	for _, later := range lines[1:] {
		if later.Approval == nil || later.Approval.Held != "request-1" || !later.Approval.Raised || later.Hook != nil {
			t.Errorf("a later look: %+v", later)
		}
	}
	if !strings.Contains(out.String(), `"deny"`) || !strings.Contains(out.String(), lanes.ReasonRequestClosed) {
		t.Errorf("the hook answered %s", out.String())
	}
}

// A `PermissionRequest` for a call the policy defers is one whose peek missed, because a deferral ends
// the turn before that hook runs. It says so to the worker, which parks the lane instead of holding a
// process for the whole wait. A call the policy does not defer, a policy that defers nothing (a pane's),
// and a policy the hook cannot read all leave the worker to its wait.
func TestAPermissionRequestSaysWhenItsPeekMissed(t *testing.T) {
	gated := lanes.DenyPolicy{ApprovalWaitMs: 60000, Gates: []lanes.GateRule{lanes.ReadGate("Bash(git tag *)")}}
	pane := lanes.DenyPolicy{ApprovalWaitMs: 60000}
	const tag = `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git tag v1"}}`
	const status = `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git status"}}`
	tests := []struct {
		name   string
		policy func(t *testing.T) string
		input  string
		missed bool
	}{
		{"a call the policy defers", func(t *testing.T) string { return policyFile(t, gated) }, tag, true},
		{"a call the policy does not defer", func(t *testing.T) string { return policyFile(t, gated) }, status, false},
		{"a policy that defers nothing", func(t *testing.T) string { return policyFile(t, pane) }, tag, false},
		{"a policy that cannot be read", func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.json") }, tag, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			denied := lanes.Reply{OK: true, Approval: &lanes.ApprovalReply{Decision: lanes.ApprovalDeny, Reason: lanes.ReasonApprovalWait, ID: "request-1"}}
			socket, worker := aScriptedWorker(t, denied)
			cfg := config{event: eventPermissionRequest, socket: socket, policy: tt.policy(t), token: "a-token", lane: "lane-1", workOrder: "wo", generation: 1}
			var out bytes.Buffer
			event, input := take(cfg, strings.NewReader(tt.input))
			run(cfg, event, input, &out)

			lines := worker.taken()
			if len(lines) != 1 || lines[0].Approval == nil {
				t.Fatalf("the hook looked %d times: %+v", len(lines), lines)
			}
			if got := lines[0].Approval; got.Missed != tt.missed || got.Peek {
				t.Errorf("the look: %+v", got)
			}
			if !strings.Contains(out.String(), lanes.ReasonApprovalWait) {
				t.Errorf("the hook answered %s", out.String())
			}
		})
	}
}
