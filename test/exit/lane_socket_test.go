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

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// laneWorker serves the real worker socket in this process, on a real journal, for
// one lane in generation 3.
type laneWorker struct {
	journal ports.Journal
	subject *worker.Worker
	// reports holds what the worker said it could not do, in the order it said it.
	reports chan error
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
	reports := make(chan error, 16)
	subject := worker.New(&fake.WorkerLink{}, journal, clock.System{}, worker.Config{Spool: env.spool, Report: func(err error) {
		select {
		case reports <- err:
		default:
		}
	}})
	listener, err := net.Listen("unix", env.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = subject.ServeLanes(ctx, listener) }()
	return &laneWorker{journal: journal, subject: subject, reports: reports}
}

func (w *laneWorker) hooks(t *testing.T) []lanes.HookRecord {
	t.Helper()
	records, err := w.journal.HookEvents(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// postToolUse is the event toolPayload makes a hook send for lane-1 in generation 3.
var postToolUse = lanes.HookEvent{Lane: "lane-1", Generation: 3, Subject: "toolu_1", Event: "PostToolUse"}

// copiesOf counts the spool lines that are copies of want, and fails on any other line.
func copiesOf(t *testing.T, lines []lanes.HookEvent, want lanes.HookEvent) int {
	t.Helper()
	copies := 0
	for _, line := range lines {
		if line.Lane != want.Lane || line.Generation != want.Generation || line.Subject != want.Subject || line.Event != want.Event {
			t.Errorf("the spool holds an event that is not %s of %s: %+v", want.Event, want.Subject, line)
			continue
		}
		copies++
	}
	return copies
}

// awaitHooks reads the worker's records until ready holds for them, and returns them.
func (w *laneWorker) awaitHooks(t *testing.T, what string, ready func([]lanes.HookRecord) bool) []lanes.HookRecord {
	t.Helper()
	var records []lanes.HookRecord
	if happened, why := exitWaits.await(func() bool {
		records = w.hooks(t)
		return ready(records)
	}); !happened {
		t.Fatalf("%s did not happen: records %+v, and the wait %s", what, records, why)
	}
	return records
}

// quietWindow is how long a test watches for a write that should never come. It is well past a
// hook's hard deadline, so a write the worker started for a hook that has since exited has landed.
const quietWindow = 500 * time.Millisecond

// A hook delivers its event at least once: the worker has it before the ack, or the spool has it.
// A hook that misses its ack under load spools an event the worker may also have, so the spool may
// hold copies of the event and no other line, and a drain merges those copies by meaning.

func TestHookReachesTheWorker(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	if run := runHook(t, env, "PostToolUse", toolPayload); run.exit != 0 || run.stdout != "" {
		t.Fatalf("exit %d, stdout %q", run.exit, run.stdout)
	}
	copies := copiesOf(t, spoolLines(t, env.spool), postToolUse)
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The worker counts the socket call once at most and each spooled copy once, and at least one of
	// them reached it. With no copy spooled, the ack means the one socket call did.
	records := w.awaitHooks(t, "the event's record", func(records []lanes.HookRecord) bool {
		return len(records) == 1 && records[0].Count >= 1
	})
	if got := records[0]; got.Subject != "toolu_1" || got.Generation != 3 || got.Count > int64(1+copies) {
		t.Errorf("records %+v after %d spooled copies", records, copies)
	}
	if copies == 0 && records[0].Count != 1 {
		t.Errorf("an acked hook was counted %d times: %+v", records[0].Count, records)
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
	copies := copiesOf(t, spoolLines(t, env.spool), postToolUse)
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Each of the three hooks reached the worker at least once, and a hook counts twice only when it
	// both reached the socket and spooled. All of them are one record.
	records := w.awaitHooks(t, "the three deliveries merged", func(records []lanes.HookRecord) bool {
		return len(records) == 1 && records[0].Count >= 3
	})
	if records[0].Count > int64(2+copies) {
		t.Errorf("records %+v after %d spooled copies", records, copies)
	}
	runHook(t, env, "PostToolUse", strings.ReplaceAll(toolPayload, "toolu_1", "toolu_2"))
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	records = w.awaitHooks(t, "the second tool call's record", func(records []lanes.HookRecord) bool { return len(records) >= 2 })
	if len(records) != 2 {
		t.Errorf("records %+v", records)
	}
}

// A wrong token proves no lane. The worker refuses the call before it writes anything, and a hook
// that missed the refusal spools its event sealed with that token, which a drain drops because the
// seal is not the lane's. Either way the worker records nothing.
func TestAWrongTokenRecordsNothing(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	stranger := env
	stranger.token = "not-the-token"
	if run := runHook(t, stranger, "PostToolUse", toolPayload); run.exit != 0 {
		t.Fatalf("exit %d", run.exit)
	}
	copiesOf(t, spoolLinesSealedWith(t, env.spool, stranger.token), postToolUse)
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(quietWindow); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if records := w.hooks(t); len(records) != 0 {
			t.Fatalf("records %+v", records)
		}
	}
}

func TestSpoolDrainsAndEmpties(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	down := hookEnv{socket: env.socket + ".absent", spool: env.spool, token: env.token}
	runHook(t, down, "Stop", `{"session_id":"s1"}`)
	runHook(t, down, "PostToolUse", toolPayload)
	leftover := env.spool + worker.DrainingSuffix
	line, err := lanes.SealHook(env.token, lanes.HookEvent{Event: "Notification", Lane: "lane-1", Generation: 3, Subject: "s1", AtMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, append(line, []byte("\nnot a line\n")...), 0o600); err != nil {
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
	select {
	case err := <-w.reports:
		if !strings.Contains(err.Error(), "1 lines") {
			t.Errorf("the worker said %v", err)
		}
	default:
		t.Error("a line that proves nothing went unreported")
	}
}

// A process that can reach the spool and holds no token of the lane writes a line that names it, and
// the worker takes nothing from it, on a real hook's real file.
func TestAForgedLineInARealSpoolIsDropped(t *testing.T) {
	env := newHookEnv(t)
	w := newLaneWorker(t, env)
	down := hookEnv{socket: env.socket + ".absent", spool: env.spool, token: "not-the-lanes-token"}
	runHook(t, down, "PostToolUse", toolPayload)
	if err := w.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	if records := w.hooks(t); len(records) != 0 {
		t.Errorf("records %+v", records)
	}
	if _, err := os.Stat(env.spool); !os.IsNotExist(err) {
		t.Errorf("the spool remains: %v", err)
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

// A lane's session names the decision tool through the real binary and the real socket, and the
// worker hears of no decision. The tool is not listed either, which the test below reads.
func TestALaneSessionCannotCallTheDecisionTool(t *testing.T) {
	env := newHookEnv(t)
	newLaneWorker(t, env)
	session := startMCP(t, env)
	session.open()
	reply := session.call("tools/call", map[string]any{
		"name": lanes.ToolApprovalDecide, "arguments": map[string]any{"id": "a1", "digest": "sha256:1", "allow": true},
	})
	if reply.Error == nil {
		t.Fatalf("a lane reached the decision tool: %s", reply.Result)
	}
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
	if len(listed.Tools) != len(lanes.Tools) {
		t.Fatalf("tools %+v", listed.Tools)
	}
	for i, tool := range listed.Tools {
		if tool.Name != lanes.Tools[i] || tool.Description == "" || tool.InputSchema["type"] != "object" {
			t.Errorf("tool %d: %+v", i, tool)
		}
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

// The tool carries what a lane wrote to its worker, and a lane the worker is not following
// reads why it was refused.
func TestMCPCarriesAHandoffToItsWorker(t *testing.T) {
	env := newHookEnv(t)
	newLaneWorker(t, env)
	session := startMCP(t, env)
	session.open()
	reply := session.call("tools/call", map[string]any{
		"name": lanes.ToolLaneHandoff, "arguments": map[string]any{"next_steps": []string{"Fix the test"}, "carries_next_step": true},
	})
	if reply.Error != nil {
		t.Fatalf("error %+v", reply.Error)
	}
	var result toolResult
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "nothing is following this lane") {
		t.Errorf("result %+v", result)
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
