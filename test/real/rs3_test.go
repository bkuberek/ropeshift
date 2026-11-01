//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs3Lane = "rs3-lane"
	// paneTurns charges the worst case a pane can reach: no flag caps a pane's turns, so
	// the brief, one nudge, and each tool call between them count.
	paneTurns = 8
	// paneToolLimit ends a pane that keeps calling tools past that charge.
	paneToolLimit = 5
	// personLine is what a person's own status line prints in the scratch repository.
	personLine  = "rs3-person-line"
	rs3Contract = `+++
kind = "contract"
id = "rs3"
title = "Run in a terminal"

[[criteria]]
id = "c1"
text = "The lane reports its outcome"

[[work_orders]]
id = "rs3-order"
title = "Run in a terminal"
size = "small"
+++

This work order tests a lane in a terminal, and it has no other work. Change no file.
Run the Bash command echo rs3-marker once. Then call the lane_report tool once, with
outcome completed. Then end your turn with the single word done.
`
	rs3Profile = `run_mode = "tmux"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0

[context.roles.lane]
warn_tokens = 1
`
	// rs3Settings is a person's project settings: a status line of their own, and a hook
	// that answers the one permission prompt this suite raises, after a pause long enough
	// for the prompt to show. Both live in the scratch repository.
	rs3Settings = `{
  "statusLine": {"type": "command", "command": "printf ` + personLine + `"},
  "hooks": {"PermissionRequest": [{"hooks": [{"type": "command", "timeout": 30,
    "command": "sleep 4; printf '{\"hookSpecificOutput\":{\"hookEventName\":\"PermissionRequest\",\"decision\":{\"behavior\":\"allow\"}}}'"}]}]}
}
`
)

// tmuxBinary finds tmux on the session's PATH, and this suite reads panes with it only on
// the worker's private socket.
func (s *suite) tmuxCommand(args ...string) *exec.Cmd {
	s.t.Helper()
	binary, err := exec.LookPath("tmux")
	if err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(binary, append([]string{"-u", "-f", os.DevNull, "-S", s.paths.TerminalSocket()}, args...)...)
	cmd.Env = s.environment()
	return cmd
}

// capture reads a pane without touching it. This suite sends no key to any pane.
func (s *suite) capture(pane string) string {
	s.t.Helper()
	out, err := s.tmuxCommand("capture-pane", "-p", "-t", "="+pane+":").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// killTerminal ends the worker's private server and nothing else.
func (s *suite) killTerminal() {
	s.t.Helper()
	_ = s.tmuxCommand("kill-server").Run()
	_ = os.Remove(s.paths.TerminalSocket())
}

// paneStop names a stop condition on a screen.
var paneStop = regexp.MustCompile(`(?i)login expired|not logged in|please run /login|invalid api key|usage limit reached|rate limit reached|out of extra usage`)

// knownWords survive redact, because each one is a word the check depends on, and none is
// an identity.
var knownWords = []string{
	"Accessing workspace:", "Yes, I trust this folder", "No, exit", "Enter to select", "Esc to exit", "for shortcuts",
	"auto mode", "Security guide",
}

var wordCharacter = regexp.MustCompile(`[\pL\pN@._/-]`)

// redact keeps a line's shape and its known words, so a screen can correct a fixture with
// no path, name, or account in the evidence.
func redact(line string) string {
	for _, word := range knownWords {
		if strings.Contains(line, word) {
			return line[:strings.Index(line, word)] + word
		}
	}
	return wordCharacter.ReplaceAllString(line, "x")
}

// screenLog keeps one redacted example of each screen a pane showed.
type screenLog struct {
	seen   map[string]bool
	shapes []string
}

func (l *screenLog) note(name, text string) {
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	if l.seen[name] {
		return
	}
	l.seen[name] = true
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, redact(strings.TrimRight(line, " ")))
		}
	}
	l.shapes = append(l.shapes, name+":\n"+strings.Join(lines, "\n"))
}

func (l *screenLog) names() []string {
	names := make([]string, 0, len(l.seen))
	for name := range l.seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// watchPane reads one pane and applies the stop conditions to it.
func (s *suite) watchPane(stop func(), pane string, screens *screenLog) string {
	s.t.Helper()
	text := s.capture(pane)
	if text == "" {
		return ""
	}
	if paneStop.MatchString(text) {
		s.stopWith(stop, "a stop pattern on the pane")
	}
	switch {
	case strings.Contains(text, "Accessing workspace:"):
		screens.note("trust", text)
	case strings.Contains(text, "Do you want to proceed"):
		screens.note("permission", text)
	default:
		screens.note("ready-or-turn", text)
	}
	return text
}

// hookCounts reads what a lane's hooks left in the journal, by event name.
func (s *suite) hookRecords(lane string) []lanes.HookRecord {
	s.t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(s.paths.StateDir, api.JournalFileName))
	if err != nil {
		s.t.Fatal(err)
	}
	defer journal.Close()
	records, err := journal.HookEvents(s.t.Context(), lane)
	if err != nil {
		s.t.Fatal(err)
	}
	return records
}

func eventCount(records []lanes.HookRecord, event string) int64 {
	var count int64
	for _, record := range records {
		if record.Event == event {
			count += record.Count
		}
	}
	return count
}

func (s *suite) laneView(lane string) ledger.LaneView {
	s.t.Helper()
	var view ledger.LaneView
	out, err := s.tryCLI("lane", "show", lane, "--json")
	if err != nil {
		return view
	}
	if err := json.Unmarshal(out, &view); err != nil {
		s.t.Fatal(err)
	}
	return view
}

// dispatch takes the one work order of a fresh daemon through to a lane.
func (s *suite) dispatch(d *daemon, lane string) string {
	s.t.Helper()
	id := s.waitForWorkOrder(d.stop)
	s.cli("work", "review", id)
	var workers api.WorkerList
	s.poll(d.stop, "the worker's session", func() bool {
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			s.t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	s.cli("work", "claim", id, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", id, "--lane", lane)
	return id
}

// TestRS3 runs a real session in a pane, through the production driver and its start
// check. Only a real session can show the known start screens on the installed version,
// the status line, Notification, and the socket path from SessionStart.
func TestRS3(t *testing.T) {
	s := start(t, "rs3")
	t.Cleanup(s.killTerminal)
	s.build()
	s.probe()

	repo := gittest.NewIn(t, s.scratch)
	repo.Commit(".ropeshift/profile.toml", rs3Profile)
	repo.Commit(".claude/settings.json", rs3Settings)
	repo.Commit("contract.md", rs3Contract)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", rs3Lane)
	if err := s.spend.charge("lane", paneTurns); err != nil {
		t.Fatal(err)
	}

	running := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(running.stop)
	id := s.dispatch(running, rs3Lane)

	const pane = rs3Lane + "-g1"
	screens := &screenLog{}
	settled := map[workorders.State]bool{
		workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true,
	}
	var row workorders.WorkOrder
	personShown, nearBudget := false, false
	s.poll(running.stop, "the lane's outcome", func() bool {
		if text := s.watchPane(running.stop, pane, screens); strings.Contains(text, personLine) {
			personShown = true
		}
		if slices.Contains(s.laneView(rs3Lane).Conditions, string(lanes.NearBudget)) {
			nearBudget = true
		}
		if tools := eventCount(s.hookRecords(rs3Lane), "PreToolUse"); tools > paneToolLimit {
			s.stopWith(running.stop, "%d tool calls passed the limit of %d", tools, paneToolLimit)
		}
		row = s.workOrder(id)
		return settled[row.State]
	})
	running.stop()

	records := s.hookRecords(rs3Lane)
	socket := ""
	for _, record := range records {
		if record.Event == "SessionStart" && record.Socket != "" {
			socket = record.Socket
		}
	}
	for _, shape := range screens.shapes {
		t.Logf("rs3: screen %s", shape)
	}
	notifications := []string{}
	for _, record := range records {
		if record.Event == "Notification" {
			notifications = append(notifications, record.Detail)
		}
	}
	t.Logf("rs3: screens=%v session_start=%d prompts=%d stops=%d tools=%d status_lines=%d notifications=%v",
		screens.names(), eventCount(records, "SessionStart"), eventCount(records, "UserPromptSubmit"),
		eventCount(records, "Stop"), eventCount(records, "PreToolUse"), eventCount(records, lanes.EventStatusLine), notifications)
	t.Logf("rs3: socket_from_hook=%v socket_in_private_dir=%v person_line=%v near_budget=%v state=%s",
		socket != "", strings.HasPrefix(socket, s.paths.RunDir), personShown, nearBudget, row.State)
	if row.State != workorders.Acceptance {
		t.Fatalf("the lane ended in %s\n%s", row.State, s.laneTail(running))
	}
	if socket == "" {
		t.Error("no SessionStart hook named a socket")
	}
	if !nearBudget {
		t.Error("no context reached the worker from the status line")
	}
	if s.standIn == "" && !personShown {
		t.Error("a person's status line did not show")
	}
	if eventCount(records, lanes.EventStatusLine) == 0 {
		t.Error("the status line never ran")
	}
}

// laneTail is the daemon's own account of a failure, with its paths in place, for a
// scratch directory this run is about to remove.
func (s *suite) laneTail(d *daemon) string {
	var out bytes.Buffer
	out.WriteString(d.stderr.String())
	screen, err := os.ReadFile(filepath.Join(s.paths.StateDir, "lanes", rs3Lane, "screen-1.txt"))
	if err == nil {
		for line := range strings.SplitSeq(string(screen), "\n") {
			fmt.Fprintln(&out, redact(line))
		}
	}
	return out.String()
}
