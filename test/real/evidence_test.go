//go:build realsession

package real

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

// A run's evidence says what each session did and where its lane stood, in kinds and names. It
// keeps no text of a session's, no ID, and no path, so it can be read with nothing to scrub.

// timelineLine reads what a timeline keeps of one stream line.
type timelineLine struct {
	Type           string `json:"type"`
	Subtype        string `json:"subtype"`
	SessionID      string `json:"session_id"`
	IsError        bool   `json:"is_error"`
	IsReplay       bool   `json:"isReplay"`
	TerminalReason string `json:"terminal_reason"`
	Message        struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// timelineBlock is one block of a message, by kind. Name is a tool's, and a tool's input stays out.
type timelineBlock struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	IsError bool   `json:"is_error"`
}

// promptKind names a message the worker wrote, because the worker's own words are no session's,
// and which of them a session was answering is what a failed run needs to show.
func promptKind(text string) string {
	switch {
	case strings.HasPrefix(text, "This lane parked"):
		return "resume prompt"
	case text == lanes.HandoffInstruction():
		return "handoff instruction"
	case text == lanes.Nudge:
		return "nudge"
	case strings.HasPrefix(text, lanes.Restarted):
		return "restart prompt"
	}
	return "brief"
}

// messageSteps reads one message's content as what it did. A plain string is a prompt, and a list
// holds tool calls, their results, and text, of which only the kinds stay.
func messageSteps(line timelineLine) []string {
	var text string
	if json.Unmarshal(line.Message.Content, &text) == nil {
		return []string{promptKind(text)}
	}
	var blocks []timelineBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return nil
	}
	var steps []string
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			steps = append(steps, "called "+block.Name)
		case "tool_result":
			if block.IsError {
				steps = append(steps, "tool refused")
			} else {
				steps = append(steps, "tool answered")
			}
		case "text":
			steps = append(steps, "wrote text")
		}
	}
	return steps
}

// timeline turns a stream into one line per thing a session did, with each session numbered in
// the order it appeared, so a reader sees two generations of one session and no ID.
func timeline(stream []byte) []string {
	var out []string
	var sessions []string
	for raw := range bytes.SplitSeq(stream, []byte("\n")) {
		var line timelineLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if line.SessionID != "" && !slices.Contains(sessions, line.SessionID) {
			sessions = append(sessions, line.SessionID)
		}
		var steps []string
		switch line.Type {
		case "system":
			if line.Subtype == "init" {
				steps = []string{"init"}
			}
		case "assistant":
			steps = messageSteps(line)
		case "user":
			steps = messageSteps(line)
		case "result":
			steps = []string{fmt.Sprintf("result %s error=%v reason=%s", line.Subtype, line.IsError, line.TerminalReason)}
		}
		for _, step := range steps {
			out = append(out, fmt.Sprintf("session %d: %s", slices.Index(sessions, line.SessionID)+1, step))
		}
	}
	return slices.Compact(out)
}

// journalStanding reads a lane from the journal the worker wrote, by kind and count.
func journalStanding(record lanes.Record, effects []lanes.Effect) []string {
	out := []string{fmt.Sprintf("journal: state=%s actual=%s generation=%d reported=%v blocking=%d resuming=%v",
		record.State, record.Actual, record.Generation, len(record.Report) != 0, len(record.Blocking), record.ResumeSession != "")}
	if parking := record.Parking; parking != nil {
		out = append(out, fmt.Sprintf("journal: parking trigger=%s generation=%d begun=%v pending=%v skip_narrative=%v",
			parking.Trigger, parking.Generation, parking.Begun, parking.Pending != "", parking.SkipNarrative))
	}
	if handoff := record.Handoff; handoff != nil {
		out = append(out, fmt.Sprintf("journal: handoff narrative=%v refused=%v document=%v begun=%v",
			handoff.Narrative != nil, handoff.Refused != "", len(handoff.Document) != 0, handoff.Begun))
	}
	for _, effect := range effects {
		out = append(out, fmt.Sprintf("journal: effect generation=%d kind=%s state=%s attempts=%d", effect.Generation, effect.Kind, effect.State, effect.Attempts))
	}
	return out
}

// ledgerStanding reads a lane from the ledger, by kind and count.
func ledgerStanding(row ledger.LaneRow, generations []ledger.Generation, wakes []string, asked []questions.Question) []string {
	out := []string{fmt.Sprintf("ledger: lane actual=%s generation=%d liveness=%s conditions=%v", row.Status.ActualState, row.Generation, row.Status.Liveness, row.Status.Conditions)}
	for _, generation := range generations {
		out = append(out, fmt.Sprintf("ledger: generation=%d start=%s end=%s session=%v responded=%v handoff_in=%v handoff_out=%v",
			generation.Number, generation.StartReason, generation.EndReason, generation.SessionID != "", generation.LastResponseAt != 0, generation.HandoffIn != "", generation.HandoffOut != ""))
	}
	out = append(out, wakes...)
	counts := map[string]int{}
	for _, question := range asked {
		counts[string(question.Status)]++
	}
	return append(out, fmt.Sprintf("ledger: questions %v", counts))
}

// laneEvidence is everything both stores hold of a lane. A read that fails says so and stops
// nothing, because this runs on a failed run, and the failure is what a reader came for.
func (s *suite) laneEvidence(lane string) []string {
	s.t.Helper()
	var out []string
	journal, err := sqlite.OpenJournal(filepath.Join(s.paths.StateDir, api.JournalFileName))
	if err != nil {
		return []string{fmt.Sprintf("journal: %v", err)}
	}
	defer journal.Close()
	record, err := journal.Lane(context.Background(), lane)
	if err != nil {
		out = append(out, fmt.Sprintf("journal: %v", err))
	}
	effects, err := journal.Effects(context.Background())
	if err != nil {
		out = append(out, fmt.Sprintf("journal: %v", err))
	}
	effects = slices.DeleteFunc(effects, func(effect lanes.Effect) bool { return effect.Lane != lane })
	out = append(out, journalStanding(record, effects)...)
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		return append(out, fmt.Sprintf("ledger: %v", err))
	}
	defer store.Close()
	err = store.Read(context.Background(), rs6Scope, func(tx ports.ReadTx) error {
		row, err := tx.Lanes().Row(lane)
		if err != nil {
			return err
		}
		generations, err := tx.Lanes().Generations(lane)
		if err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(row.WorkOrder)
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("ledger: work order state=%s fence=%d to_resume=%v", order.State, order.Fence, order.ResumeLaneID != ""))
		stored, err := tx.Wakes().All()
		if err != nil {
			return err
		}
		var wakes []string
		for _, wake := range stored {
			if wake.Lane == lane {
				wakes = append(wakes, fmt.Sprintf("ledger: wake %s open=%v by=%s", strings.TrimPrefix(wake.ID, lane+"/"), wake.Open(), wake.FiredBy))
			}
		}
		asked, err := tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords})
		if err != nil {
			return err
		}
		out = append(out, ledgerStanding(row, generations, wakes, asked)...)
		approvals, err := tx.Approvals().OfLane(lane)
		if err != nil {
			return err
		}
		for _, a := range approvals {
			out = append(out, fmt.Sprintf("ledger: request generation=%d state=%s channel=%s decided_kind=%s reason=%q", a.Generation, a.State, a.Channel, a.DecidedKind, reasonClass(a.Reason)))
		}
		return nil
	})
	if err != nil {
		out = append(out, fmt.Sprintf("ledger: %v", err))
	}
	return out
}

// uuidForm is a session's ID wherever a worker's report names one.
var uuidForm = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// scrubbed keeps a worker's report and takes out what names this machine or a session.
func scrubbed(text, scratch, home string) string {
	text = strings.ReplaceAll(text, scratch, "<scratch>")
	if home != "" {
		text = strings.ReplaceAll(text, home, "<home>")
	}
	return uuidForm.ReplaceAllString(text, "<session>")
}

// keptScreens reads the screens a failed start left for a person, in shape only.
func (s *suite) keptScreens(lane string) []string {
	s.t.Helper()
	paths, err := filepath.Glob(filepath.Join(s.paths.StateDir, "lanes", lane, "screen-*.txt"))
	if err != nil {
		return nil
	}
	var out []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		log := &screenLog{}
		log.note(filepath.Base(path), string(raw))
		out = append(out, log.shapes...)
	}
	return out
}

// reportEvidence says what the sessions did and where each lane stood. It always runs for a suite
// that names its lanes, and a failed run adds what the worker reported.
func (s *suite) reportEvidence() {
	s.t.Helper()
	if len(s.watched) == 0 {
		return
	}
	if raw, err := os.ReadFile(s.stream); err == nil {
		s.logf("evidence: what the headless sessions did:\n  %s", strings.Join(timeline(raw), "\n  "))
	}
	if _, err := os.Stat(filepath.Join(s.paths.StateDir, api.JournalFileName)); err != nil {
		return
	}
	for _, lane := range s.watched {
		s.logf("evidence: %s:\n  %s", lane, strings.Join(s.laneEvidence(lane), "\n  "))
		counts, tools := s.hookEvidence(lane)
		if len(counts) != 0 {
			s.logf("evidence: %s hooks per generation: %v", lane, counts)
		}
		if len(tools) != 0 {
			s.logf("evidence: %s tools per generation: %v", lane, tools)
		}
		if s.t.Failed() {
			for _, screen := range s.keptScreens(lane) {
				s.logf("evidence: %s kept a screen:\n%s", lane, screen)
			}
		}
	}
	if !s.t.Failed() {
		return
	}
	for i, d := range s.daemons {
		s.logf("evidence: daemon %d reported:\n%s", i+1, scrubbed(d.reported(), s.scratch, s.home))
	}
}

// hookEvidence reads a lane's hooks, as counts and as the tools it called, and a read that fails
// leaves them out, because this runs on the way to cleaning up and nothing here may stop that.
func (s *suite) hookEvidence(lane string) (counts, tools []string) {
	s.t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(s.paths.StateDir, api.JournalFileName))
	if err != nil {
		return nil, nil
	}
	defer journal.Close()
	records, err := journal.HookEvents(context.Background(), lane)
	if err != nil {
		return nil, nil
	}
	return hookCounts(records), hookTools(records)
}

// hookTools names the tools a lane called, by generation and in the order it first called each,
// with a repeat counted. A name is all it keeps: no argument, answer, or reply.
func hookTools(records []lanes.HookRecord) []string {
	calls := slices.DeleteFunc(slices.Clone(records), func(record lanes.HookRecord) bool {
		return record.Event != "PreToolUse" || record.Tool == ""
	})
	slices.SortStableFunc(calls, func(a, b lanes.HookRecord) int {
		if a.Generation != b.Generation {
			return cmp.Compare(a.Generation, b.Generation)
		}
		return cmp.Compare(a.FirstMs, b.FirstMs)
	})
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		name := fmt.Sprintf("g%d %s", call.Generation, call.Tool)
		if call.Count > 1 {
			name += fmt.Sprintf(" x%d", call.Count)
		}
		out = append(out, name)
	}
	return out
}

// hookCounts sums a lane's hooks by generation and event.
func hookCounts(records []lanes.HookRecord) []string {
	counts := map[string]int64{}
	for _, record := range records {
		counts[fmt.Sprintf("g%d %s", record.Generation, record.Event)] += record.Count
	}
	out := make([]string, 0, len(counts))
	for name, count := range counts {
		out = append(out, fmt.Sprintf("%s=%d", name, count))
	}
	slices.Sort(out)
	return out
}

func TestStopEvidenceKeepsNoTextOfASession(t *testing.T) {
	const secret = "a reply that names a person"
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"type":"user","isReplay":true,"session_id":"11111111-1111-4111-8111-111111111111","message":{"content":"` + secret + `"}}`,
		`{"type":"assistant","session_id":"11111111-1111-4111-8111-111111111111","message":{"content":[{"type":"text","text":"` + secret + `"},{"type":"tool_use","name":"mcp__ropeshift__ask","input":{"text":"` + secret + `"}}]}}`,
		`{"type":"user","session_id":"11111111-1111-4111-8111-111111111111","message":{"content":[{"type":"tool_result","is_error":true,"content":"` + secret + `"}]}}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","session_id":"11111111-1111-4111-8111-111111111111","result":"` + secret + `"}`,
		`{"type":"system","subtype":"init","session_id":"22222222-2222-4222-8222-222222222222"}`,
		`not a line of JSON`,
	}, "\n")
	got := timeline([]byte(stream))
	want := []string{
		"session 1: init", "session 1: brief", "session 1: wrote text", "session 1: called mcp__ropeshift__ask",
		"session 1: tool refused", "session 1: result error_during_execution error=true reason=aborted_streaming", "session 2: init",
	}
	if !slices.Equal(got, want) {
		t.Errorf("timeline %q", got)
	}
	for _, line := range got {
		if strings.Contains(line, secret) || strings.Contains(line, "1111") {
			t.Errorf("a line kept text or an ID: %s", line)
		}
	}
}

func TestStopEvidenceNamesWhichMessageASessionAnswered(t *testing.T) {
	tests := []struct{ name, text, want string }{
		{"a resume", lanes.ResumePrompt("Finish it"), "resume prompt"},
		{"the handoff instruction", lanes.HandoffInstruction(), "handoff instruction"},
		{"the nudge", lanes.Nudge, "nudge"},
		{"a restart", lanes.Restarted, "restart prompt"},
		{"anything else", "Your brief", "brief"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := promptKind(tt.text); got != tt.want {
				t.Errorf("kind %q", got)
			}
		})
	}
}

func TestStopEvidenceScrubsAWorkerReport(t *testing.T) {
	report := "lane x: /tmp/rs7abc/state/lanes/x: /home/person/.claude session 11111111-1111-4111-8111-111111111111"
	got := scrubbed(report, "/tmp/rs7abc", "/home/person")
	if strings.Contains(got, "rs7abc") || strings.Contains(got, "person") || strings.Contains(got, "1111") {
		t.Errorf("scrubbed %q", got)
	}
}

func TestStopEvidenceNamesAParkingWithoutItsSteps(t *testing.T) {
	const lane = "lane"
	asked := lanes.NewEffect(lanes.EffectInput, lane, 1, "", "handoff")
	asked.State = lanes.EffectCompleted
	nudged := lanes.NewEffect(lanes.EffectInput, lane, 1, "", "nudge")
	nudged.State = lanes.EffectCompleted
	stopped := lanes.NewEffect(lanes.EffectStop, lane, 1, "", "")
	stopped.State = lanes.EffectCompleted
	unfinished := lanes.NewEffect(lanes.EffectStop, lane, 1, "", "")
	unfinished.State = lanes.EffectStarted
	other := lanes.NewEffect(lanes.EffectStop, "another", 1, "", "")
	other.State = lanes.EffectCompleted
	later := lanes.NewEffect(lanes.EffectStop, lane, 2, "", "")
	later.State = lanes.EffectCompleted
	tests := []struct {
		name    string
		effects []lanes.Effect
		gaps    int
	}{
		{"a request and a stop", []lanes.Effect{asked, stopped}, 0},
		{"nothing", nil, 2},
		{"a nudge is no request", []lanes.Effect{nudged, stopped}, 1},
		{"a stop that never finished", []lanes.Effect{asked, unfinished}, 1},
		{"another lane's stop", []lanes.Effect{asked, other}, 1},
		{"a later generation's stop", []lanes.Effect{asked, later}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parkingGaps(tt.effects, lane, 1); len(got) != tt.gaps {
				t.Errorf("gaps %q, want %d", got, tt.gaps)
			}
		})
	}
}

func TestHookToolsNameEachCallByGenerationInOrder(t *testing.T) {
	call := func(generation int64, event, tool string, first, count int64) lanes.HookRecord {
		return lanes.HookRecord{HookEvent: lanes.HookEvent{Generation: generation, Event: event, Tool: tool}, FirstMs: first, Count: count}
	}
	records := []lanes.HookRecord{
		call(2, "PreToolUse", "mcp__ropeshift__lane_report", 30, 1),
		call(1, "PreToolUse", "mcp__ropeshift__decision_search", 20, 2),
		call(1, "PostToolUse", "mcp__ropeshift__decision_search", 21, 2),
		call(1, "PreToolUse", "mcp__ropeshift__question_search", 10, 1),
		call(1, "SessionStart", "", 5, 1),
		call(1, "PreToolUse", "", 15, 1),
	}
	want := []string{"g1 mcp__ropeshift__question_search", "g1 mcp__ropeshift__decision_search x2", "g2 mcp__ropeshift__lane_report"}
	if got := hookTools(records); !slices.Equal(got, want) {
		t.Errorf("hookTools = %q, want %q", got, want)
	}
	if got := hookTools(nil); len(got) != 0 {
		t.Errorf("no records name %q", got)
	}
}

func TestStopEvidenceStandsOnAnEmptyRecord(t *testing.T) {
	if got := journalStanding(lanes.Record{}, nil); len(got) != 1 {
		t.Errorf("an empty record reads %q", got)
	}
	got := ledgerStanding(ledger.LaneRow{}, nil, []string{"ledger: wake g1 open=true by="}, []questions.Question{{Status: questions.StatusAnswered}})
	if len(got) != 3 || !strings.Contains(got[2], "answered:1") {
		t.Errorf("a lane reads %q", got)
	}
}
