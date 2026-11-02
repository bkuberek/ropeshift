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
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	// rs12Charge covers one wake with its two calls and the turn that ends it.
	rs12Charge = 5
	// rs12PlanCharge covers a file, a commit, a report, and the turn that ends it, with room for a
	// session that has to look twice at the format.
	rs12PlanCharge = 8
	rs12PlanTurns  = "8"
	// rs12Sessions is one orchestrator in each run mode and one planning lane. The headless init
	// stands for a probe.
	rs12Sessions    = 3
	rs12TurnCeiling = 2*rs12Charge + rs12PlanCharge + 1
	rs12MaxTurns    = "4"
	// rs12ToolLimit ends a session that keeps calling tools past that charge.
	rs12ToolLimit = 4
	rs12Member    = "member-rs12"
	rs12PlanOrder = "rs12-plan"
	rs12PlanLane  = "rs12-plan-lane"
	// rs12PlanFile is what a dry run's planning lane commits.
	rs12PlanFile = "plans/rs12.md"
	// rs12ProjectSettings let a planning lane write and commit, and nothing else, in this scratch
	// repository alone. A headless session has nobody to answer a prompt.
	rs12ProjectSettings = `{"permissions":{"allow":["Write","Edit","Bash(mkdir:*)","Bash(git add:*)","Bash(git commit:*)"]}}
`
)

// rs12PlanStatement is the trial: a session that holds the format only as its brief states it. It
// names no file and no key, because what a planning agent makes of the format is the question.
const rs12PlanStatement = "Plan a retry budget for a checkout client: one contract with one work order, in one new file under plans/. " +
	"Stage and commit that file, then report completed. Do nothing else, and run no other command."

// rs12StandInContract is what a dry run's planning lane commits. A real one writes its own.
const rs12StandInContract = `+++
kind = "contract"
id = "retry-budget"
title = "Add a retry budget"

[[criteria]]
id = "c1"
text = "A call retries twice."

[[work_orders]]
id = "retry-budget-work"
title = "Add the budget"
repo = "."
size = "small"
+++

Retry a failed call twice.
`

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

func standInPlanner() []any {
	write := "mkdir -p plans; printf '%s' '" + rs12StandInContract + "' > " + rs12PlanFile + "; git add plans; git commit -q -m 'plan the retry budget'"
	return []any{
		map[string]any{"init": true},
		standInPrompt(),
		map[string]any{"tool": "Bash", "command": write},
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{"outcome": "completed"}},
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

// rs12Plan runs a lane on a `plan` work order, and reads what it left as intake reads it. This is the
// trial the open question on the file format asks for: the session writes what its brief describes,
// and only the parser says whether that was the format. A member claims and starts it, because that
// takes no window, and a member accepts it, because nothing a lane wrote is a record before then.
func (s *suite) rs12Plan(flags []string) {
	s.t.Helper()
	s.playStandIn([]any{standInPlanner()}, nil)
	if err := os.WriteFile(filepath.Join(s.paths.StateDir, profiles.UserFile), []byte(rs12Profile(lanes.ProfileRunHeadless)), 0o600); err != nil {
		s.t.Fatal(err)
	}
	stream := filepath.Join(s.scratch, "plan-stream.log")
	s.stream = stream
	if err := s.spend.charge("planning", rs12PlanCharge); err != nil {
		s.t.Fatal(err)
	}
	running := s.startDaemonWith(map[string]any{"args": flags, "stream_log": stream})
	s.t.Cleanup(running.stop)
	worker := s.rs12Worker(running)
	var sources api.SourceList
	s.poll(running.stop, "the source", func() bool {
		listed, err := s.tryCLI("source", "list", "--json")
		return err == nil && json.Unmarshal(listed, &sources) == nil && len(sources.Sources) == 1
	})
	s.cli("work", "create", "--id", rs12PlanOrder, "--kind", string(workorders.KindPlan), "--repository", sources.Sources[0].Instance,
		"--title", "Plan the retry budget", "--statement", rs12PlanStatement, "--criterion", "One contract with one work order is committed on this branch.")
	s.cli("work", "review", rs12PlanOrder)
	s.cli("work", "claim", rs12PlanOrder, "--worker", worker)
	s.cli("lane", "start", rs12PlanOrder, "--lane", rs12PlanLane)
	settled := map[workorders.State]bool{workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true}
	var row workorders.WorkOrder
	s.poll(running.stop, "the planning lane's outcome", func() bool {
		s.checkStream(running.stop)
		s.rs12LaneInit(running.stop)
		row = s.workOrder(rs12PlanOrder)
		return settled[row.State]
	})
	if row.State != workorders.Acceptance {
		running.stop()
		s.t.Fatalf("the planning lane ended in %s\n%s", row.State, running.reported())
	}
	var standing workorders.AcceptanceView
	if err := json.Unmarshal(s.cli("work", "acceptance", rs12PlanOrder, "--json"), &standing); err != nil {
		s.t.Fatal(err)
	}
	commit := ""
	for _, artifact := range standing.Artifacts {
		if artifact.Kind == ledger.ArtifactBranch {
			commit = artifact.Digest
		}
	}
	if commit == "" || s.rs12Recorded(commit) {
		s.t.Fatalf("a record before anyone accepted it, or no branch: commit %q, %+v", commit, standing.Artifacts)
	}
	s.cli("work", "accept", rs12PlanOrder)
	s.poll(running.stop, "intake at the accepted commit", func() bool { return s.workOrder(rs12PlanOrder).PlanCommit != "" })
	if !s.rs12Recorded(commit) {
		s.t.Errorf("the accepted commit left no contract")
	}
	if listed := s.cli("source", "list", "--json"); json.Unmarshal(listed, &sources) == nil {
		for _, problem := range sources.Sources[0].Problems {
			s.t.Errorf("intake could not read %s: %s", problem.Path, problem.Detail)
		}
	}
	left := s.settleProcesses(rs12PlanLane)
	running.stop()
	if left != 0 {
		s.t.Errorf("a process of the planning lane remains")
	}
}

// rs12LaneInit stops the run at a lane's first init that shows user configuration.
func (s *suite) rs12LaneInit(stop func()) {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.t.Fatal(err)
	}
	for _, init := range allInits(raw) {
		if err := checkInit(init, []string{"ropeshift"}, laneTools); err != nil {
			s.stopWith(stop, "a planning lane's init: %v", err)
		}
	}
}

// rs12Recorded says the ledger holds a contract read at a commit, and logs what the trial asks: how
// much of the format a session got right, by count and with none of its text.
func (s *suite) rs12Recorded(commit string) bool {
	s.t.Helper()
	var plans api.PlanList
	if err := json.Unmarshal(s.cli("plan", "list", "--json"), &plans); err != nil {
		s.t.Fatal(err)
	}
	found := false
	for _, plan := range plans.Plans {
		var view api.PlanView
		if err := json.Unmarshal(s.cli("plan", "show", plan.ID, "--json"), &view); err != nil {
			s.t.Fatal(err)
		}
		for _, contract := range view.Contracts {
			if !strings.HasSuffix(contract.Source, "@"+commit) {
				continue
			}
			found = true
			s.t.Logf("rs12 plan: contract versions=%d criteria=%d work_orders=%d statement_bytes=%d", contract.Version, len(contract.Criteria), len(view.WorkOrders), len(contract.Statement))
			if len(contract.Criteria) == 0 || len(view.WorkOrders) == 0 || strings.TrimSpace(contract.Statement) == "" {
				s.t.Errorf("a contract with nothing in it: %d criteria, %d work orders", len(contract.Criteria), len(view.WorkOrders))
			}
			for _, order := range view.WorkOrders {
				if order.State != workorders.ContractReview {
					s.t.Errorf("a planned work order reads %s", order.State)
				}
			}
		}
	}
	return found
}

// TestRS12 wakes a real orchestrator on a question, in each run mode, and runs a real planning lane.
// Only a real session can show a decision wake with a working record tool, what the tools of its role
// cost in its baseline, and how well an agent writes the repository file format from its brief.
func TestRS12(t *testing.T) {
	s := start(t, "rs12")
	s.spend.sessionCeiling, s.spend.turnCeiling = rs12Sessions, rs12TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapLanes(rs12PlanLane)
	s.build()
	repo := gittest.NewIn(t, s.scratch)
	for _, setting := range [][]string{{"user.name", "RS12"}, {"user.email", "rs12@example.invalid"}, {"commit.gpgsign", "false"}, {"core.hooksPath", "/dev/null"}} {
		repo.Git("config", setting[0], setting[1])
	}
	repo.Commit(".claude/settings.json", rs12ProjectSettings)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", rs12PlanLane)
	flags := append(slices.Clone(guardFlags), "--max-turns", rs12MaxTurns)
	s.rs12Run(lanes.ProfileRunHeadless, 1, map[string]any{"args": flags, "stream_log": s.stream})
	s.rs12Run(lanes.ProfileRunTmux, 2, map[string]any{"args": rs12PaneFlags, "env": paneEnv})
	s.rs12Plan([]string{
		"--setting-sources", "project", "--strict-mcp-config", "--tools", "Write,Edit,Bash",
		"--max-budget-usd", budgetUSD, "--no-session-persistence", "--max-turns", rs12PlanTurns,
	})
}
