package exit

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// laneWorker serves the real worker socket in this process, on a real journal, for
// one lane in generation 3.
type laneWorker struct {
	journal ports.Journal
	subject *worker.Worker
}

func newLaneWorker(t *testing.T, env hookEnv) *laneWorker {
	t.Helper()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := journal.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	err = journal.SaveLane(ctx, lanes.Record{
		ID: "lane-1", WorkOrder: "wo", ClaimID: "c1", Fence: 1, Generation: 3,
		TokenDigest: lanes.TokenDigest(env.token), State: lanes.StateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	subject := worker.New(&fake.WorkerLink{}, journal, clock.System{}, worker.Config{Spool: env.spool})
	listener, err := net.Listen("unix", env.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = subject.ServeLanes(ctx, listener) }()
	return &laneWorker{journal: journal, subject: subject}
}

func (w *laneWorker) hooks(t *testing.T) []lanes.HookRecord {
	t.Helper()
	records, err := w.journal.HookEvents(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestHookReachesTheWorker(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	if run := runHook(t, env, "PostToolUse", toolPayload); run.exit != 0 || run.stdout != "" {
		t.Fatalf("exit %d, stdout %q", run.exit, run.stdout)
	}
	if lines := spoolLines(t, env.spool); len(lines) != 0 {
		t.Errorf("a delivered hook spooled: %+v", lines)
	}
	records := w.hooks(t)
	if len(records) != 1 || records[0].Subject != "toolu_1" || records[0].Generation != 3 || records[0].Count != 1 {
		t.Errorf("records %+v", records)
	}
}

// TestRepeatedEventMergesByMeaning delivers one tool call on the socket twice and
// through the spool once, as a missed ack does.
func TestRepeatedEventMergesByMeaning(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	for range 2 {
		runHook(t, env, "PostToolUse", toolPayload)
	}
	runHook(t, hookEnv{socket: env.socket + ".absent", spool: env.spool, token: env.token}, "PostToolUse", toolPayload)
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	records := w.hooks(t)
	if len(records) != 1 || records[0].Count != 3 {
		t.Errorf("records %+v", records)
	}
	runHook(t, env, "PostToolUse", strings.ReplaceAll(toolPayload, "toolu_1", "toolu_2"))
	if records := w.hooks(t); len(records) != 2 {
		t.Errorf("a second tool call merged into the first: %+v", records)
	}
}

func TestAWrongTokenRecordsNothing(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	stranger := env
	stranger.token = "not-the-token"
	if run := runHook(t, stranger, "PostToolUse", toolPayload); run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	if records := w.hooks(t); len(records) != 0 {
		t.Errorf("records %+v", records)
	}
	if lines := spoolLines(t, env.spool); len(lines) != 0 {
		t.Errorf("a refusal is an answer, and spools nothing: %+v", lines)
	}
}

func TestSpoolDrainsAndEmpties(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	down := hookEnv{socket: env.socket + ".absent", spool: env.spool, token: env.token}
	runHook(t, down, "Stop", `{"session_id":"s1"}`)
	runHook(t, down, "PostToolUse", toolPayload)
	leftover := env.spool + worker.DrainingSuffix
	if err := os.WriteFile(leftover, []byte(`{"event":"Notification","lane":"lane-1","generation":3,"subject":"s1","at_ms":1}`+"\n"+"not a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	if records := w.hooks(t); len(records) != 3 {
		t.Errorf("records %+v", records)
	}
	for _, path := range []string{env.spool, leftover} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", path, err)
		}
	}
}

// mcpSession drives `ropeshift mcp` over stdio.
type mcpSession struct {
	t      *testing.T
	stdin  io.WriteCloser
	stdout *bufio.Reader
	id     int
}

func startMCP(t *testing.T, env hookEnv) *mcpSession {
	t.Helper()
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	cmd := exec.Command(binaries.cli, "mcp")
	cmd.Env = env.list()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("ropeshift mcp did not exit when its input closed")
		}
	})
	return &mcpSession{t: t, stdin: stdin, stdout: bufio.NewReader(stdout)}
}

type mcpReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func (s *mcpSession) send(message map[string]any) {
	s.t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		s.t.Fatal(err)
	}
}

func (s *mcpSession) call(method string, params map[string]any) mcpReply {
	s.t.Helper()
	s.id++
	s.send(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	line, err := s.stdout.ReadBytes('\n')
	if err != nil {
		s.t.Fatal(err)
	}
	var reply mcpReply
	if err := json.Unmarshal(line, &reply); err != nil {
		s.t.Fatalf("%q: %v", line, err)
	}
	if reply.ID != s.id {
		s.t.Fatalf("answered %d, asked %d", reply.ID, s.id)
	}
	return reply
}

func (s *mcpSession) open() {
	s.t.Helper()
	reply := s.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	var result struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		s.t.Fatal(err)
	}
	if result.ProtocolVersion == "" || result.ServerInfo.Name != "ropeshift" || result.Capabilities["tools"] == nil {
		s.t.Errorf("initialize %+v", result)
	}
	s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func (s *mcpSession) report(arguments map[string]any) toolResult {
	s.t.Helper()
	reply := s.call("tools/call", map[string]any{"name": "lane_report", "arguments": arguments})
	if reply.Error != nil {
		s.t.Fatalf("error %+v", reply.Error)
	}
	var result toolResult
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		s.t.Fatal(err)
	}
	return result
}

func TestMCPListsAndCallsLaneReport(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	session := startMCP(t, env)
	session.open()
	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(session.call("tools/list", map[string]any{}).Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "lane_report" || listed.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("tools %+v", listed.Tools)
	}
	result := session.report(map[string]any{
		"outcome": "completed", "artifacts": []string{"branch"}, "checks": []map[string]any{{"name": "unit", "passed": true}},
	})
	if result.IsError {
		t.Fatalf("result %+v", result)
	}
	record, err := w.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	var report workorders.Report
	if err := json.Unmarshal(record.Report, &report); err != nil {
		t.Fatal(err)
	}
	if report.Outcome != workorders.OutcomeCompleted || len(report.Checks) != 1 || !report.Checks[0].Passed {
		t.Errorf("report %+v", report)
	}
}

func TestMCPRefusesAWrongToken(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	stranger := env
	stranger.token = "not-the-token"
	session := startMCP(t, stranger)
	session.open()
	result := session.report(map[string]any{"outcome": "completed"})
	if !result.IsError {
		t.Fatalf("a wrong token was accepted: %+v", result)
	}
	record, err := w.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Report) != 0 {
		t.Errorf("report %s", record.Report)
	}
}

func TestMCPRefusesWhatIsNotAReport(t *testing.T) {
	env := newHookEnv(t)
	newLaneWorker(t, env)
	session := startMCP(t, env)
	session.open()
	if result := session.report(map[string]any{"outcome": "almost"}); !result.IsError {
		t.Errorf("an unknown outcome: %+v", result)
	}
	if reply := session.call("tools/call", map[string]any{"name": "work_claim"}); reply.Error == nil {
		t.Error("a tool outside the lane's set was answered")
	}
	if reply := session.call("resources/list", map[string]any{}); reply.Error == nil || reply.Error.Code != -32601 {
		t.Errorf("an unknown method: %+v", reply)
	}
}

func TestAnEarlierReportStands(t *testing.T) {
	env := newHookEnv(t)
	newLaneWorker(t, env)
	session := startMCP(t, env)
	session.open()
	if result := session.report(map[string]any{"outcome": "completed"}); result.IsError {
		t.Fatalf("first %+v", result)
	}
	if result := session.report(map[string]any{"outcome": "completed"}); result.IsError {
		t.Errorf("a retry of the same report: %+v", result)
	}
	if result := session.report(map[string]any{"outcome": "blocked", "reason": "changed my mind"}); !result.IsError {
		t.Errorf("a second outcome: %+v", result)
	}
}

func TestMCPWithNoWorkerSaysSo(t *testing.T) {
	env := newHookEnv(t)
	session := startMCP(t, env)
	session.open()
	if result := session.report(map[string]any{"outcome": "completed"}); !result.IsError {
		t.Errorf("result %+v", result)
	}
}

// TestDaemonServesTheWorkerSocket checks the socket hooks reach, and the stable path
// generated settings name (D18).
func TestDaemonServesTheWorkerSocket(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	paths := in.paths()
	in.waitFor("the worker socket", func() bool {
		info, err := os.Stat(paths.WorkerSocket())
		return err == nil && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0o600
	})
	in.waitFor("the stable link", func() bool {
		_, err := os.Stat(paths.StableBinary())
		return err == nil
	})
	target, err := os.Readlink(paths.StableBinary())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(binaries.cli)
	if err != nil {
		t.Fatal(err)
	}
	if target != resolved {
		t.Errorf("the stable link points at %s", target)
	}
	if out, err := exec.Command(paths.StableBinary(), "version").CombinedOutput(); err != nil {
		t.Errorf("the stable path does not run: %v\n%s", err, out)
	}
	env := hookEnv{socket: paths.WorkerSocket(), spool: paths.Spool(), token: "a-token"}
	if run := runHook(t, env, "Stop", `{}`); run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	if lines := spoolLines(t, env.spool); len(lines) != 0 {
		t.Errorf("a refused hook spooled: %+v", lines)
	}
}

func TestSocketPathsStayUnderTheLimit(t *testing.T) {
	home := strings.Repeat("h", 70)
	paths, err := api.ResolvePaths("darwin", func(string) string { return "" }, "/"+home)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(paths.WorkerSocket()); got > api.MaxSocketPath {
		t.Errorf("worker socket %d bytes", got)
	}
}
