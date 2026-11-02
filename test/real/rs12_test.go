//go:build realsession

package real

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

const (
	// rs12Charge covers one wake with its two calls and the turn that ends it.
	rs12Charge = 5
	// rs12Sessions is one orchestrator in each run mode. The headless init stands for a probe.
	rs12Sessions    = 2
	rs12TurnCeiling = 2*rs12Charge + 1
	rs12MaxTurns    = "4"
	// rs12ToolLimit ends a session that keeps calling tools past that charge.
	rs12ToolLimit = 4
	rs12Member    = "member-rs12"
)

// rs12PaneFlags leave out paneGuardFlags' `--tools Bash`, which a lane's suites need and which would
// hand an orchestrator what its worker took away.
var rs12PaneFlags = []string{"--setting-sources", "project", "--strict-mcp-config"}

func rs12Profile(mode string) string {
	return fmt.Sprintf(`run_mode = %q

[agent]
model = %q
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
wake_debounce = "1s"
wake_min_interval = "2s"
ask_wait = "5m"
handoff_timeout = "2s"
max_restarts = 0
`, mode, sessionModel)
}

// rs12Text is a question a session can answer alone, in one call, and whose answer shows it read it.
func rs12Text(scope string) string {
	return "Which log level do retries use? Take this question with question_next, then call decision_record once, with this question's ID, " +
		"status decided, statement warn, rationale given, scope " + scope + ", reversibility reversible, and citations RS12. Then end your turn with the single word done."
}

func standInOrchestrator(question, scope string) []any {
	return []any{
		map[string]any{"init": true},
		map[string]any{"status": map[string]any{"context_window": map[string]any{"current_usage": map[string]any{"input_tokens": 12000}}}},
		map[string]any{"emit": map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": map[string]any{
			"type": "message_start", "message": map[string]any{"usage": map[string]any{"input_tokens": 12000}},
		}}},
		map[string]any{"mcp": lanes.ToolQuestionNext},
		map[string]any{"mcp": lanes.ToolDecisionRecord, "arguments": map[string]any{
			"question": question, "status": "decided", "statement": "warn", "rationale": "given", "scope": scope,
			"reversibility": "reversible", "citations": []string{"RS12"},
		}},
		standInResult(),
	}
}

// rs12Tools are the MCP tools an orchestrator's session may hold, as a session names them.
func rs12Tools() []string {
	out := make([]string, 0, len(lanes.OrchestratorTools))
	for _, tool := range lanes.OrchestratorTools {
		out = append(out, "mcp__ropeshift__"+tool)
	}
	return out
}

// rs12Init stops the run at the first init that shows user configuration, which is the probe this
// suite's budget leaves no room for, and says how many of its own tools a session was offered.
func (s *suite) rs12Init(stop func()) int {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		s.t.Fatal(err)
	}
	offered := 0
	for _, init := range allInits(raw) {
		if err := checkInit(init, []string{"ropeshift"}, rs12Tools()); err != nil {
			s.stopWith(stop, "an orchestrator's init: %v", err)
		}
		offered = 0
		for _, tool := range init.Tools {
			if strings.HasPrefix(tool, "mcp__") {
				offered++
			}
		}
	}
	return offered
}

// rs12Read opens the ledger of the running daemon, for what a member would read of it.
func (s *suite) rs12Read(read func(ports.ReadTx) error) {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	if err := store.Read(s.t.Context(), rs6Scope, read); err != nil {
		s.t.Fatal(err)
	}
}

func (s *suite) rs12Ask(id, text string) {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	ops := control.NewQuestions(store, questions.BatchPolicy{})
	if _, err := ops.Ask(s.t.Context(), rs6Scope, questions.Ask{ID: id, Text: text}, ledger.Actor{Kind: ledger.ActorMember, ID: rs12Member}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *suite) rs12Worker(d *daemon) string {
	s.t.Helper()
	var workers api.WorkerList
	s.poll(d.stop, "the worker's session", func() bool {
		listed, err := s.tryCLI("worker", "list", "--json")
		if err != nil || json.Unmarshal(listed, &workers) != nil {
			return false
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	return workers.Workers[0].ID
}

// rs12Run wakes one orchestrator on one question, in one run mode, and reads what it did. Each
// start is a run of its own, so the second lane has a name of its own.
func (s *suite) rs12Run(mode string, run int64, overrides map[string]any) {
	s.t.Helper()
	question, scope := "rs12-"+mode, "rs12-logging-"+mode
	s.playStandIn([]any{standInOrchestrator(question, scope)}, nil)
	if err := os.MkdirAll(s.paths.StateDir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.paths.StateDir, profiles.UserFile), []byte(rs12Profile(mode)), 0o600); err != nil {
		s.t.Fatal(err)
	}
	if err := s.spend.charge(mode, rs12Charge); err != nil {
		s.t.Fatal(err)
	}
	running := s.startDaemonWith(overrides)
	s.t.Cleanup(running.stop)
	worker := s.rs12Worker(running)
	s.rs12Ask(question, rs12Text(scope))
	s.cli("orchestrator", "start", worker)
	lane := ledger.Orchestrator{ID: ledger.OrchestratorOf(worker), Run: run}.Lane()
	s.others = append(s.others, filepath.Join(s.paths.StateDir, "lanes", lane, "session"))
	screens := &screenLog{}
	var asked questions.Question
	offered := 0
	// A run that stops leaves nothing behind to read, so what it saw goes in the log, by name only.
	defer func() {
		if !s.t.Failed() {
			return
		}
		for _, record := range s.hookRecords(lane) {
			s.t.Logf("rs12 %s: hook %s tool=%s count=%d", mode, record.Event, record.Tool, record.Count)
		}
		s.t.Logf("rs12 %s: screens=%v", mode, screens.names())
	}()
	s.poll(running.stop, "the orchestrator's decision", func() bool {
		s.checkStream(running.stop)
		offered = s.rs12Init(running.stop)
		if mode == lanes.ProfileRunTmux {
			s.watchPane(running.stop, lane+"-g1", screens)
		}
		if tools := eventCount(s.hookRecords(lane), "PreToolUse"); tools > rs12ToolLimit {
			s.stopWith(running.stop, "%d tool calls passed the limit of %d", tools, rs12ToolLimit)
		}
		s.rs12Read(func(tx ports.ReadTx) error {
			var err error
			asked, err = tx.Questions().Question(question)
			return err
		})
		return asked.Status == questions.StatusAnswered
	})
	var decision questions.Decision
	s.rs12Read(func(tx ports.ReadTx) error {
		var err error
		decision, err = tx.Questions().Decision(asked.DecisionID)
		return err
	})
	tools := []string{}
	for _, record := range s.hookRecords(lane) {
		if record.Event == "PreToolUse" && !slices.Contains(tools, record.Tool) {
			tools = append(tools, record.Tool)
		}
	}
	slices.Sort(tools)
	s.cli("orchestrator", "stop", worker)
	var row ledger.Orchestrator
	s.poll(running.stop, "the orchestrator's end", func() bool {
		if err := json.Unmarshal(s.cli("orchestrator", "show", worker, "--json"), &row); err != nil {
			s.t.Fatal(err)
		}
		return row.Actual == "ended"
	})
	left := s.settleProcesses(lane)
	running.stop()
	journal, err := sqlite.OpenJournal(filepath.Join(s.paths.StateDir, api.JournalFileName))
	if err != nil {
		s.t.Fatal(err)
	}
	defer journal.Close()
	record, err := journal.Lane(s.t.Context(), lane)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Logf("rs12 %s: status=%s decision=%s by_worker=%v steps=%d tools=%v offered=%d baseline=%d screens=%v processes_left=%d",
		mode, asked.Status, decision.Status, decision.DecidedBy == worker, len(asked.LadderTrace), tools, offered, record.Progress.BaselineTokens, screens.names(), left)
	if decision.Status != questions.DecisionDecided || decision.DecidedBy != worker {
		s.t.Errorf("%s: the decision reads %s, by %q", mode, decision.Status, decision.DecidedBy)
	}
	if !asked.Ran(questions.RungLookup) || !asked.Ran(questions.RungOpen) {
		s.t.Errorf("%s: the ladder holds %+v", mode, asked.LadderTrace)
	}
	if want := "mcp__ropeshift__" + lanes.ToolDecisionRecord; !slices.Contains(tools, want) {
		s.t.Errorf("%s: tools %v", mode, tools)
	}
	if record.Progress.BaselineTokens == 0 {
		s.t.Errorf("%s: no baseline, so the tools' cost is unread", mode)
	}
	if left != 0 {
		s.t.Errorf("%s: a process of the session remains", mode)
	}
}

// TestRS12 wakes a real orchestrator on a question, in each run mode. Only a real session can show a
// decision wake with a working record tool, and what the tools of its role cost in its baseline. The
// planning lane the plan names here comes with U43, which runs this suite again.
func TestRS12(t *testing.T) {
	s := start(t, "rs12")
	s.spend.sessionCeiling, s.spend.turnCeiling = rs12Sessions, rs12TurnCeiling
	t.Cleanup(s.killTerminal)
	s.build()
	flags := append(slices.Clone(guardFlags), "--max-turns", rs12MaxTurns)
	s.rs12Run(lanes.ProfileRunHeadless, 1, map[string]any{"args": flags, "stream_log": s.stream})
	s.rs12Run(lanes.ProfileRunTmux, 2, map[string]any{"args": rs12PaneFlags, "env": paneEnv})
}
