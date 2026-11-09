//go:build realsession

package real

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	rs13HeadlessLane = "rs13-headless"
	rs13PaneLane     = "rs13-pane"
	// rs13Charge covers one lane's brief and its one tool call, and a pane's tool limit holds it there.
	rs13Charge      = 4
	rs13Sessions    = 3
	rs13TurnCeiling = 1 + 2*rs13Charge
	rs13MaxTurns    = "3"
	rs13ToolLimit   = 3
	// rs13Cap is D3's proposed size bound, which the measured overhead is read against.
	rs13Cap = 256 << 20
)

func rs13Profile(mode string) string {
	return `run_mode = "` + mode + `"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`
}

// rs13Contract asks for one report, with a word that appears nowhere but in the brief, so finding it
// in telemetry would show a prompt got in.
func rs13Contract(id, word string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Report through telemetry"

[[criteria]]
id = "c1"
text = "The lane reports its outcome"

[[work_orders]]
id = "` + id + `-order"
title = "Report through telemetry"
size = "small"
+++

This work order tests what a lane sends to the worker's telemetry receiver, and it has no other
work. Change no file, read no file, and run no command. Call the lane_report tool once, with
outcome completed and reason set to the single word ` + word + `. Then end your turn with the
single word done.
`
}

// standInTelemetryReport is what a dry run's lane plays: one report, as the brief asks. A pane draws a
// status line first, which a headless session never runs.
func standInTelemetryReport(word string, pane bool) []any {
	steps := []any{map[string]any{"init": true}}
	if pane {
		steps = append(steps, map[string]any{"status": map[string]any{"context_window": map[string]any{"current_usage": map[string]any{"input_tokens": 9000}}}})
	}
	return append(steps,
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
			"outcome": "completed", "reason": word, "artifacts": []string{"none"},
			"checks": []map[string]any{{"name": "stand-in", "passed": true}},
		}},
		standInResult(),
	)
}

// rs13Collector is the one place this suite's export goes: a stand-in OTLP endpoint on loopback,
// which keeps every body it is sent.
type rs13Collector struct {
	mu     sync.Mutex
	bodies []string
	server *httptest.Server
}

func newRS13Collector(t *testing.T) *rs13Collector {
	t.Helper()
	c := &rs13Collector{}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		c.mu.Lock()
		c.bodies = append(c.bodies, r.URL.Path+" "+string(raw))
		c.mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *rs13Collector) heard() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bodies)
}

// rs13TelemetryWait is how long the agent's telemetry may take to arrive after its session ended. The
// CLI exports at exit, so a minute is generous, and none at all is this suite's finding, not a stop.
const rs13TelemetryWait = time.Minute

// agentRecords reads the local store, through the CLI, until the agent's telemetry for a lane is in or
// the wait runs out.
func (s *suite) agentRecords(lane string) []api.TelemetryRecord {
	s.t.Helper()
	var records api.TelemetryRecords
	for deadline := time.Now().Add(rs13TelemetryWait); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if err := json.Unmarshal(s.cli("telemetry", "query", "--lane", lane, "--limit", "10000", "--json"), &records); err != nil {
			s.t.Fatal(err)
		}
		for _, record := range records.Records {
			if strings.HasPrefix(record.Name, "claude_code.") {
				return records.Records
			}
		}
	}
	return records.Records
}

// rs13Lane is what one lane left in telemetry, as counts and nothing a session wrote.
type rs13Lane struct {
	state    workorders.State
	agent    int
	metrics  int
	logs     int
	requests int
	tagged   bool
	content  bool
	left     int
}

// readLane names what a lane's agent telemetry fails to show: every record tagged with its lane,
// work order, generation, role, worker, driver, profile, and workspace, and no word of its brief.
func readLane(records []api.TelemetryRecord, lane, id, word string) rs13Lane {
	out := rs13Lane{tagged: true}
	for _, record := range records {
		if !strings.HasPrefix(record.Name, "claude_code.") {
			continue
		}
		out.agent++
		switch record.Signal {
		case ports.SignalMetric:
			out.metrics++
		case ports.SignalLog:
			out.logs++
		}
		if record.Name == "claude_code.api_request" {
			out.requests++
		}
		for key, want := range map[string]string{
			lanes.AttrLane: lane, lanes.AttrWorkOrder: id, lanes.AttrGeneration: "1", lanes.AttrRole: lanes.RoleWriter,
			lanes.AttrWorker: "solo", lanes.AttrDriver: "claude-code", lanes.AttrProfile: "default", lanes.AttrWorkspace: "default",
		} {
			if record.Attributes[key] != want {
				out.tagged = false
			}
		}
		raw, _ := json.Marshal(record)
		out.content = out.content || strings.Contains(string(raw), word)
	}
	return out
}

func (l rs13Lane) failures(mode string) []string {
	var out []string
	fail := func(on bool, text string) {
		if on {
			out = append(out, mode+": "+text)
		}
	}
	fail(l.state != workorders.Acceptance, "the work order is "+string(l.state))
	fail(l.agent == 0, "no agent telemetry reached the receiver")
	fail(!l.tagged, "an agent record lacks a ropeshift tag")
	fail(l.content, "the brief's word reached telemetry")
	fail(l.left != 0, "a process of the session remains")
	return out
}

// TestRS13 runs a real lane in each run mode with telemetry on, which only a real session can show:
// the agent's OTLP reaching the worker's receiver tagged with `ropeshift.*` attributes, no prompt in
// it, an export that drops account attributes, and what telemetry costs in bytes against D3's bounds.
// The export goes only to a collector this test starts on loopback.
func TestRS13(t *testing.T) {
	s := start(t, "rs13")
	s.watched = []string{rs13HeadlessLane, rs13PaneLane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs13Sessions, rs13TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapLanes(rs13HeadlessLane)
	s.build()
	s.probe()

	collector := newRS13Collector(t)
	s.daemonEnv = []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.server.URL, api.EnvTelemetry + "=127.0.0.1:0"}
	words := map[string]string{"headless": rs9Word(t), "pane": rs9Word(t)}
	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs13Profile(lanes.ProfileRunHeadless))
	headless.Commit("contract.md", rs13Contract("rs13h", words["headless"]))
	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs13Profile(lanes.ProfileRunTmux))
	pane.Commit("contract.md", rs13Contract("rs13p", words["pane"]))
	s.repo = headless.Dir
	s.worktree = filepath.Join(filepath.Dir(headless.Dir), filepath.Base(headless.Dir)+"-lanes", rs13HeadlessLane)
	paneWorktree := filepath.Join(filepath.Dir(pane.Dir), filepath.Base(pane.Dir)+"-lanes", rs13PaneLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, paneWorktree}
	for _, lane := range []string{"headless", "pane"} {
		if err := s.spend.charge(lane, rs13Charge); err != nil {
			t.Fatal(err)
		}
	}
	settled := map[workorders.State]bool{workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true}
	results := map[string]rs13Lane{}

	s.playStandIn([]any{standInTelemetryReport(words["headless"], false)}, nil)
	first := s.startDaemonWith(map[string]any{
		"args": append(append([]string{}, guardFlags...), "--max-turns", rs13MaxTurns), "stream_log": s.stream,
	})
	t.Cleanup(first.stop)
	headlessID := s.dispatch(first, rs13HeadlessLane)
	s.poll(first.stop, "the headless lane's outcome", func() bool {
		s.checkStream(first.stop)
		return settled[s.workOrder(headlessID).State]
	})
	state := s.workOrder(headlessID).State
	left := s.settleProcesses(rs13HeadlessLane)
	var records []api.TelemetryRecord
	if state == workorders.Acceptance {
		records = s.agentRecords(rs13HeadlessLane)
	}
	first.stop()
	result := readLane(records, rs13HeadlessLane, headlessID, words["headless"])
	result.state, result.left = state, left
	results["headless"] = result

	s.repo = pane.Dir
	s.playStandIn([]any{standInTelemetryReport(words["pane"], true)}, nil)
	second := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(second.stop)
	var paneID string
	s.poll(second.stop, "the pane's work order", func() bool {
		out, err := s.tryCLI("work", "list", "--json")
		if err != nil {
			return false
		}
		var list api.WorkOrderList
		if err := json.Unmarshal(out, &list); err != nil {
			t.Fatal(err)
		}
		for _, row := range list.WorkOrders {
			if row.ID != headlessID {
				paneID = row.ID
			}
		}
		return paneID != ""
	})
	s.cli("work", "review", paneID)
	var workers api.WorkerList
	if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
		t.Fatal(err)
	}
	s.cli("work", "claim", paneID, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", paneID, "--lane", rs13PaneLane)
	screens := &screenLog{}
	s.poll(second.stop, "the pane's outcome", func() bool {
		s.watchPane(second.stop, rs13PaneLane+"-g1", screens)
		if tools := eventCount(s.hookRecords(rs13PaneLane), "PreToolUse"); tools > rs13ToolLimit {
			s.stopWith(second.stop, "%d tool calls passed the limit of %d", tools, rs13ToolLimit)
		}
		return settled[s.workOrder(paneID).State]
	})
	state = s.workOrder(paneID).State
	left = s.settleProcesses(rs13PaneLane)
	records = nil
	if state == workorders.Acceptance {
		records = s.agentRecords(rs13PaneLane)
	}
	second.stop()
	result = readLane(records, rs13PaneLane, paneID, words["pane"])
	result.state, result.left = state, left
	results["pane"] = result

	store, err := sqlite.OpenTelemetry(filepath.Join(s.paths.StateDir, "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	size, err := store.Size(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.Records(t.Context(), ports.TelemetryQuery{Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	spans := map[string]int{}
	own := 0
	for _, record := range all {
		if strings.HasPrefix(record.Name, "ropeshift.") {
			own++
		}
		if record.Signal == ports.SignalSpan {
			spans[record.Name]++
		}
	}
	heard := collector.heard()
	exportedAgent, exportedAccount, exportedWord := false, false, false
	for _, body := range heard {
		exportedAgent = exportedAgent || strings.Contains(body, "claude_code.")
		exportedAccount = exportedAccount || strings.Contains(body, `"key":"user.`) || strings.Contains(body, `"key":"organization.`)
		exportedWord = exportedWord || strings.Contains(body, words["headless"]) || strings.Contains(body, words["pane"])
	}
	requests := results["headless"].requests + results["pane"].requests
	perRequest := int64(0)
	if requests > 0 {
		perRequest = size / int64(requests)
	}
	for _, mode := range []string{"headless", "pane"} {
		r := results[mode]
		t.Logf("rs13: %s: state=%s agent_records=%d metrics=%d logs=%d api_requests=%d tagged=%v content=%v processes_left=%d",
			mode, r.state, r.agent, r.metrics, r.logs, r.requests, r.tagged, r.content, r.left)
	}
	t.Logf("rs13: store_bytes=%d records=%d own_records=%d spans=%v", size, len(all), own, spans)
	t.Logf("rs13: export: requests=%d agent=%v account_attributes=%v brief_word=%v", len(heard), exportedAgent, exportedAccount, exportedWord)
	if perRequest > 0 {
		t.Logf("rs13: overhead: bytes_per_api_request=%d api_requests_under_the_256MiB_bound=%d", perRequest, int64(rs13Cap)/perRequest)
	}
	t.Logf("rs13: screens=%v", screens.names())

	for _, mode := range []string{"headless", "pane"} {
		for _, failure := range results[mode].failures(mode) {
			t.Error(failure)
		}
	}
	if !exportedAgent {
		t.Error("the configured exporter heard none of the agent's telemetry")
	}
	if exportedAccount {
		t.Error("an export carries an account attribute")
	}
	if exportedWord {
		t.Error("an export carries a brief's word")
	}
	if spans[ledger.SpanLane] < 2 {
		t.Errorf("ropeshift's own lane spans: %d", spans[ledger.SpanLane])
	}
	if size > rs13Cap {
		t.Error(fmt.Sprintf("the store holds %d bytes, past its bound", size))
	}
}
