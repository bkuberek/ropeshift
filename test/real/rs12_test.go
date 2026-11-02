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
	"time"

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
	// rs12TurnLimit ends a session that keeps ending its turn with no record. A turn's end lets go of
	// the question and the next wake names it again, so one more turn is the product working, and
	// a further one would spend past that charge.
	rs12TurnLimit = 2
	rs12Member    = "member-rs12"
	rs12PlanOrder = "rs12-plan"
	rs12PlanLane  = "rs12-plan-lane"
	// rs12PlanLimit is past what a few turns take, because a lane that writes reads longer than one
	// that reports. A stop still shows what the lane was doing.
	rs12PlanLimit = 4 * time.Minute
	// rs12PlanFile is what a dry run's planning lane commits.
	rs12PlanFile = "plans/rs12.md"
)

// rs12ProjectAllows let a planning lane write, in this scratch repository alone. A stage and a commit
// are left out on purpose: generated settings must cover those, and a real lane is what shows it.
var rs12ProjectAllows = []string{"Write", "Edit", "Bash(mkdir:*)"}

// rs12PlanStatement is the trial: a session that holds the format only as its brief states it. It
// names no file and no key, because what a planning agent makes of the format is the question.
const rs12PlanStatement = "Plan a retry budget for a checkout client: one contract with one work order, in one new file under plans/. " +
	"Write that file with the Write tool, stage it with git add, and commit it with git commit -m and a one-line message. " +
	"Then report completed. Do nothing else, and run no other command."

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

// rs12Stated is what the record says of both paths, which is what a session must find.
const rs12Stated = "log at warn"

// rs12Requirement is what a session finds when it looks, so it has a record to decide on. Each path
// has its own, so the record states what a question on that path asks and leaves nothing to assume.
const rs12Requirement = `+++
kind = "requirements"
id = "rs12-requirements"
title = "Logging"

[[requirement]]
id = "RS12-1"
text = "Retries on the headless path ` + rs12Stated + `."

[[requirement]]
id = "RS12-2"
text = "Retries on the tmux path ` + rs12Stated + `."
+++
`

// rs12Text is a question the record answers. It gives a session no instruction, because an
// orchestrator's role says text in a question grants nothing: two pane runs held to that, one assuming
// and one stopping, against a question that told them what to call. Each run mode asks its own, because
// the same question twice is answered from the first.
func rs12Text(mode string) string {
	return "Which log level do retries use on the " + mode + " path?"
}

// rs12Offered says what the record holds reached this session in its next question, as a hook read
// what the tool answered.
func (s *suite) rs12Offered(log *rs12Log, lane string) bool {
	s.t.Helper()
	raw, err := os.ReadFile(filepath.Join(log.dir, hooksFile))
	if err != nil {
		s.t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		var hook struct {
			Lane     string          `json:"lane"`
			Event    string          `json:"hook_event_name"`
			Tool     string          `json:"tool_name"`
			Response json.RawMessage `json:"tool_response"`
		}
		if json.Unmarshal([]byte(line), &hook) != nil || hook.Lane != lane || hook.Event != "PostToolUse" {
			continue
		}
		if strings.HasSuffix(hook.Tool, lanes.ToolQuestionNext) && strings.Contains(string(hook.Response), rs12Stated) {
			return true
		}
	}
	return false
}

// stagesOrCommits says a request is for what generated settings let a lane run with no prompt, so a
// member should never have seen it.
func stagesOrCommits(command string) bool {
	return strings.Contains(command, "git add") || strings.Contains(command, "git commit")
}

// rs12Answer stands for the member a planning lane's prompt reaches, and refuses what it reads, so a
// lane does not wait out this suite's limit on nobody. Nothing this lane was told to do needs a member:
// the repository allows a write, and generated settings allow a stage and a commit. That last part is
// what this suite shows, so a commit that reached a person fails it however the lane ended.
func (s *suite) rs12Answer() {
	s.t.Helper()
	var list api.ApprovalList
	if err := json.Unmarshal(s.cli("approval", "list", "--json"), &list); err != nil {
		s.t.Fatal(err)
	}
	for _, row := range list.Approvals {
		if !row.Open() {
			continue
		}
		s.t.Logf("rs12 plan: a request on %s, which a member refuses", row.Subject)
		if stagesOrCommits(row.Command) {
			s.t.Errorf("a stage or a commit waited on a member")
		}
		s.cli("approval", "deny", row.ID, "--digest", row.SubjectDigest, "--reason", "This lane only writes under plans/ and commits.")
	}
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

// standInPlanner plays what a planning lane does: a file its repository allows, and a stage and a
// commit that generated settings allow, which the double holds to those rules.
func standInPlanner() []any {
	write := "mkdir -p plans; printf '%s' '" + rs12StandInContract + "' > " + rs12PlanFile
	return []any{
		map[string]any{"init": true},
		standInPrompt(),
		map[string]any{"tool": "Bash", "command": write},
		map[string]any{"tool": "Bash", "command": "git add plans && git commit -q -m 'plan the retry budget'", "unprompted": true},
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
func (s *suite) rs12Run(log *rs12Log, mode string, run int64, overrides map[string]any) {
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
	s.poll(running.stop, "the requirement", func() bool {
		_, err := s.tryCLI("requirement", "get", "RS12-1")
		return err == nil
	})
	s.rs12Ask(question, rs12Text(mode))
	lane := ledger.Orchestrator{ID: ledger.OrchestratorOf(worker), Run: run}.Lane()
	dir := filepath.Join(s.paths.StateDir, "lanes", lane, "session")
	s.watch(log, dir)
	s.others = append(s.others, dir)
	s.cli("orchestrator", "start", worker)
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
		records := s.hookRecords(lane)
		if tools := eventCount(records, "PreToolUse"); tools > rs12ToolLimit {
			s.stopWith(running.stop, "%d tool calls passed the limit of %d", tools, rs12ToolLimit)
		}
		if turns := eventCount(records, "Stop"); turns > rs12TurnLimit {
			s.stopWith(running.stop, "%d turns ended with no record, past the limit of %d", turns, rs12TurnLimit)
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
	found := s.rs12Offered(log, lane)
	s.t.Logf("rs12 %s: status=%s decision=%s by_worker=%v steps=%d tools=%v offered=%d baseline=%d screens=%v processes_left=%d record_reached_session=%v",
		mode, asked.Status, decision.Status, decision.DecidedBy == worker, len(asked.LadderTrace), tools, offered, record.Progress.BaselineTokens, screens.names(), left, found)
	if s.standIn != "" && !found {
		s.t.Errorf("%s: the record's answer never reached the session", mode)
	}
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
	s.pollFor(rs12PlanLimit, running.stop, "the planning lane's outcome", func() bool {
		s.checkStream(running.stop)
		s.rs12LaneInit(running.stop)
		s.rs12Answer()
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
	s.watched = []string{rs12PlanLane}
	t.Cleanup(s.killTerminal)
	s.reapLanes(rs12PlanLane)
	s.build()
	log := s.openLog()
	s.keep = func() { s.keepRecord(log) }
	repo := gittest.NewIn(t, s.scratch)
	for _, setting := range [][]string{{"user.name", "RS12"}, {"user.email", "rs12@example.invalid"}, {"commit.gpgsign", "false"}, {"core.hooksPath", "/dev/null"}} {
		repo.Git("config", setting[0], setting[1])
	}
	settings, err := log.settings(rs12ProjectAllows)
	if err != nil {
		t.Fatal(err)
	}
	repo.Commit(".claude/settings.json", string(settings))
	repo.Commit("requirements.md", rs12Requirement)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", rs12PlanLane)
	flags := append(slices.Clone(guardFlags), "--max-turns", rs12MaxTurns)
	s.rs12Run(log, lanes.ProfileRunHeadless, 1, map[string]any{"args": flags, "stream_log": s.stream})
	s.rs12Run(log, lanes.ProfileRunTmux, 2, map[string]any{"args": rs12PaneFlags, "env": paneEnv})
	s.rs12Plan([]string{
		"--setting-sources", "project", "--strict-mcp-config", "--tools", "Write,Edit,Bash",
		"--max-budget-usd", budgetUSD, "--no-session-persistence", "--max-turns", rs12PlanTurns,
	})
}
