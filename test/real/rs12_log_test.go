//go:build realsession

package real

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles/evidence"
)

const (
	// envEvidence names where a run keeps what its sessions did. Unset, it lands under a directory
	// git ignores in this repository.
	envEvidence = "ROPESHIFT_EVIDENCE_DIR"
	// hooksFile takes one line per hook as a session runs, so a run that stops leaves what it saw.
	hooksFile = "hooks.jsonl"
	// recordFile is where the run stood at its end, written before cleanup.
	recordFile = "run.json"
	// evidenceHookTimeout bounds a hook that appends one line.
	evidenceHookTimeout = 5
)

// loggedEvents are the events a session's hooks report, the ones a lane listens to.
var loggedEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PermissionDenied", "PostToolUse",
	"PostToolUseFailure", "Notification", "PreCompact", "PostCompact", "Stop", "StopFailure", "SessionEnd",
}

// rs12Log keeps what each session of one run did where a run's cleanup cannot reach it: two runs left
// nothing to explain what happened, because this suite's own evidence holds no text. The hook lines
// land as a session runs, and the record lands first thing in cleanup. Neither goes in a commit.
type rs12Log struct {
	dir  string
	hook string
}

// evidenceRoot is where a run's directory goes.
func evidenceRoot() (string, error) {
	if dir := os.Getenv(envEvidence); dir != "" {
		return filepath.Abs(dir)
	}
	return filepath.Abs(filepath.Join("..", "..", ".scratch", "evidence"))
}

// openLog makes this run's directory and builds the hook that writes to it.
func (s *suite) openLog() *rs12Log {
	s.t.Helper()
	root, err := evidenceRoot()
	if err != nil {
		s.t.Fatal(err)
	}
	if root == s.scratch || strings.HasPrefix(root, s.scratch+string(filepath.Separator)) {
		s.t.Fatalf("%s falls inside what cleanup removes", envEvidence)
	}
	kind := "real"
	if s.standIn != "" {
		kind = "stand-in"
	}
	dir := filepath.Join(root, fmt.Sprintf("rs12-%s-%s", time.Now().UTC().Format("20060102T150405Z"), kind))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	hook := filepath.Join(s.binDir, "evidencehook")
	build := exec.Command("go", "build", "-o", hook, "ropeshift.dev/ropeshift/test/doubles/evidence/evidencehook")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		s.t.Fatalf("build: %v\n%s", err, out)
	}
	s.t.Logf("evidence: this run keeps what its sessions did under %s", filepath.Base(dir))
	return &rs12Log{dir: dir, hook: hook}
}

// settings is a project settings file: the hooks that keep the log, beside the generated ones, and
// what a project allows.
func (l *rs12Log) settings(allow []string) ([]byte, error) {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type group struct {
		Hooks []hook `json:"hooks"`
	}
	command := lanes.ShellQuote(l.hook) + " " + lanes.ShellQuote(filepath.Join(l.dir, hooksFile))
	hooks := map[string][]group{}
	for _, event := range loggedEvents {
		hooks[event] = []group{{Hooks: []hook{{Type: "command", Command: command, Timeout: evidenceHookTimeout}}}}
	}
	file := map[string]any{"hooks": hooks}
	if len(allow) != 0 {
		file["permissions"] = map[string]any{"allow": allow}
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// watch gives a session that runs outside any repository the same hooks. An orchestrator's session
// reads its project settings from the private directory it starts in.
func (s *suite) watch(log *rs12Log, dir string) {
	s.t.Helper()
	raw, err := log.settings(nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), raw, 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// loggedLane is where one lane stood in the journal, by what a reader needs, with no token's digest
// and no session.
type loggedLane struct {
	ID         string   `json:"id"`
	WorkOrder  string   `json:"work_order"`
	Generation int64    `json:"generation"`
	State      string   `json:"state"`
	Actual     string   `json:"actual"`
	Branch     string   `json:"branch"`
	Report     string   `json:"report"`
	Blocking   []string `json:"blocking,omitempty"`
}

// loggedHook is one merged hook record of a lane.
type loggedHook struct {
	Lane       string `json:"lane"`
	Generation int64  `json:"generation"`
	Event      string `json:"event"`
	Tool       string `json:"tool,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Count      int64  `json:"count"`
}

// loggedResult is how one headless turn ended, as its stream said.
type loggedResult struct {
	Subtype  string `json:"subtype"`
	IsError  bool   `json:"is_error"`
	Turns    int64  `json:"num_turns"`
	Terminal string `json:"terminal_reason"`
	Text     string `json:"result"`
}

// rs12Record is where a run stood at its end.
type rs12Record struct {
	LedgerLanes []ledger.LaneRow       `json:"ledger_lanes"`
	WorkOrders  []workorders.WorkOrder `json:"work_orders"`
	Requests    []authority.Approval   `json:"requests"`
	Questions   []questions.Question   `json:"questions"`
	Decisions   []questions.Decision   `json:"decisions"`
	Lanes       []loggedLane           `json:"journal_lanes"`
	Hooks       []loggedHook           `json:"hooks"`
	Results     []loggedResult         `json:"results"`
	Daemons     []string               `json:"daemons"`
	Problems    []string               `json:"read_problems,omitempty"`
}

// readLedger takes what the ledger holds. A run that stopped before it existed has none.
func (r *rs12Record) readLedger(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	store, err := sqlite.Open(path, sqlstore.Options{})
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Read(ctx, rs6Scope, func(tx ports.ReadTx) error {
		var err error
		if r.LedgerLanes, err = tx.Lanes().Rows(); err != nil {
			return err
		}
		if r.WorkOrders, err = tx.WorkOrders().List(); err != nil {
			return err
		}
		if r.Requests, err = tx.Approvals().All(); err != nil {
			return err
		}
		if r.Questions, err = tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords}); err != nil {
			return err
		}
		r.Decisions, err = tx.Questions().Decisions(ports.RecordFilter{Limit: ports.MaxRecords})
		return err
	})
}

// readJournal takes each lane's own state, and what its hooks reported.
func (r *rs12Record) readJournal(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	journal, err := sqlite.OpenJournal(path)
	if err != nil {
		return err
	}
	defer journal.Close()
	records, err := journal.Lanes(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		r.Lanes = append(r.Lanes, loggedLane{
			ID: record.ID, WorkOrder: record.WorkOrder, Generation: record.Generation, State: record.State,
			Actual: string(record.Actual), Branch: record.Branch, Report: string(record.Report), Blocking: record.Blocking,
		})
		hooks, err := journal.HookEvents(ctx, record.ID)
		if err != nil {
			return err
		}
		for _, hook := range hooks {
			r.Hooks = append(r.Hooks, loggedHook{
				Lane: record.ID, Generation: hook.Generation, Event: hook.Event, Tool: hook.Tool, Detail: hook.Detail, Count: hook.Count,
			})
		}
	}
	return nil
}

// readStreams takes how each headless turn ended.
func (r *rs12Record) readStreams(scratch string) error {
	paths, err := filepath.Glob(filepath.Join(scratch, "*stream.log"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			var kept struct {
				Type string `json:"type"`
				loggedResult
			}
			if json.Unmarshal([]byte(line), &kept) == nil && kept.Type == "result" {
				r.Results = append(r.Results, kept.loggedResult)
			}
		}
	}
	return nil
}

// keepRecord writes where the run stood. It runs before anything is removed, on a run that failed
// as on one that passed, and what it cannot read it says on the record.
func (s *suite) keepRecord(log *rs12Log) {
	s.t.Helper()
	ctx := context.Background()
	var record rs12Record
	reads := []error{
		record.readLedger(ctx, filepath.Join(s.paths.StateDir, api.LedgerFileName)),
		record.readJournal(ctx, filepath.Join(s.paths.StateDir, api.JournalFileName)),
		record.readStreams(s.scratch),
	}
	for _, err := range reads {
		if err != nil {
			record.Problems = append(record.Problems, err.Error())
		}
	}
	for _, d := range s.daemons {
		record.Daemons = append(record.Daemons, d.reported())
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		s.t.Error(err)
		return
	}
	text := evidence.Scrub(scrubbed(string(raw), s.scratch, s.home), s.home)
	if err := os.WriteFile(filepath.Join(log.dir, recordFile), []byte(text+"\n"), 0o600); err != nil {
		s.t.Error(err)
	}
	if err := errors.Join(reads...); err != nil {
		s.t.Errorf("the run's record is short: %v", err)
	}
	if sessions, _ := s.spend.used(); sessions != 0 {
		s.checkHooksKept(log)
	}
}

// checkHooksKept says a run that started a session kept what that session did, because a log that
// came up empty looks the same as no log until someone needs it.
func (s *suite) checkHooksKept(log *rs12Log) {
	s.t.Helper()
	raw, err := os.ReadFile(filepath.Join(log.dir, hooksFile))
	if err != nil {
		s.t.Errorf("no hook reached the log: %v", err)
		return
	}
	s.t.Logf("evidence: the log kept %d hooks", strings.Count(string(raw), "\n"))
}
