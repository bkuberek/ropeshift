package exit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// The account attributes the stand-in CLI's records carry, which only the local store may keep.
var standInAccount = []string{"stand-in@example.com", "00000000-0000-0000-0000-00000000c0de", "stand-in-user", "user.email", "organization.id"}

// collector is a stand-in OTLP endpoint on loopback, the only place an export in this suite goes.
type collector struct {
	mu     sync.Mutex
	bodies []string
	server *httptest.Server
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, r.URL.Path+" "+string(raw))
		c.mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *collector) heard() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.bodies...)
}

// TestAStopWritesTheSpansItHasNotRead stops a daemon whose ledger moved after its last read into
// spans: what the ledger recorded still reaches the store as the daemon stops, so a lane that ended
// just before a stop has its span.
func TestAStopWritesTheSpansItHasNotRead(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--telemetry-tick", "1h", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	d.stop()
	store, err := sqlite.OpenTelemetry(filepath.Join(in.paths().StateDir, "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spans, err := store.Records(t.Context(), ports.TelemetryQuery{Name: ledger.SpanLane})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Lane != "lane-1" {
		t.Errorf("lane spans %+v", spans)
	}
}

// TestLaneTelemetryReachesTheReceiver runs a lane on the real binary and the real driver: the agent
// sends its telemetry to the worker's receiver, tagged with the lane, its work order, and its
// generation, with no content in it. The local store keeps the account attributes, and the export a
// person configured drops them (FR-OBS-2, FR-OBS-3, FR-OBS-4, FR-OBS-5, NFR-PRIV-1).
func TestLaneTelemetryReachesTheReceiver(t *testing.T) {
	in := newInstall(t)
	exporter := newCollector(t)
	in.daemonEnv = []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + exporter.server.URL}
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--telemetry-tick", "50ms", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	var records api.TelemetryRecords
	in.waitFor("the lane's own telemetry and its lane span", func() bool {
		if err := json.Unmarshal([]byte(in.must("telemetry", "query", "--limit", "1000", "--json")), &records); err != nil {
			t.Fatal(err)
		}
		agent, lane := false, false
		for _, record := range records.Records {
			agent = agent || record.Name == "claude_code.api_request"
			lane = lane || record.Name == ledger.SpanLane
		}
		return agent && lane
	})
	var lanesOut api.LaneTelemetryList
	if err := json.Unmarshal([]byte(in.must("telemetry", "lanes", "--json")), &lanesOut); err != nil {
		t.Fatal(err)
	}
	d.stop()

	for _, record := range records.Records {
		if !strings.HasPrefix(record.Name, "claude_code.") {
			continue
		}
		if record.Lane != "lane-1" || record.WorkOrder != id || record.Generation != 1 || record.Attributes[lanes.AttrRole] != lanes.RoleWriter ||
			record.Attributes[lanes.AttrWorker] != "solo" || record.Attributes[lanes.AttrDriver] != "claude-code" {
			t.Errorf("the agent's record is not tagged: %+v", record)
		}
		if record.Name == "claude_code.user_prompt" && record.Attributes["prompt"] != "<REDACTED>" {
			t.Errorf("a prompt reached telemetry with no opt-in: %v", record.Attributes["prompt"])
		}
		if record.Attributes["user.email"] != "stand-in@example.com" {
			t.Errorf("the local store lost an account attribute: %v", record.Attributes)
		}
	}
	if len(lanesOut.Lanes) != 1 || lanesOut.Lanes[0].Lane != "lane-1" || lanesOut.Lanes[0].ContextTokens == 0 {
		t.Errorf("lane telemetry %+v", lanesOut)
	}

	heard := exporter.heard()
	if len(heard) == 0 {
		t.Fatal("the configured exporter heard nothing")
	}
	agentHeard := false
	for _, body := range heard {
		agentHeard = agentHeard || strings.Contains(body, "claude_code.api_request")
		for _, account := range standInAccount {
			if strings.Contains(body, account) {
				t.Errorf("an export carries %s", account)
			}
		}
		if strings.Contains(body, "Add the retry budget") {
			t.Error("an export carries the brief")
		}
	}
	if !agentHeard {
		t.Error("the agent's telemetry was not forwarded")
	}

	// The store is a file of its own beside the ledger.
	store, err := sqlite.OpenTelemetry(filepath.Join(in.paths().StateDir, "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spans, err := store.Records(t.Context(), ports.TelemetryQuery{Signal: ports.SignalSpan, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, span := range spans {
		found[span.Name] = true
		if strings.HasPrefix(span.Name, "ropeshift.") && span.Detail["trace_id"] != ledger.WorkOrderTrace(id) && span.Name != ledger.SpanEffect {
			t.Errorf("span %s is not in its work order's trace", span.Name)
		}
	}
	for _, name := range []string{ledger.SpanClaim, ledger.SpanLane, ledger.SpanGeneration, ledger.SpanEffect} {
		if !found[name] {
			t.Errorf("no %s span: %v", name, found)
		}
	}
}
