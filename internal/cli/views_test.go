package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/top"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/exit/ptypair"
)

// TestEveryWebRouteHasACommand is the plan's rule that the UI has no endpoint the CLI lacks. The web
// listener serves a closed list, and each row of it names a command, which must exist.
func TestEveryWebRouteHasACommand(t *testing.T) {
	for _, route := range api.WebRoutes() {
		if !cli.HasCommand(route.Command) {
			t.Errorf("%s %s names %q, and no such command exists", route.Method, route.Pattern, route.Command)
		}
	}
}

// With no terminal top prints one frame, so a pipe or a log holds what a person would see.
func TestTopPrintsOneFrameWithNoTerminal(t *testing.T) {
	s := newSession(t)
	s.ready("wo")
	s.must("work", "cancel", "wo", "--reason", "Dropped")
	text := s.must("top")
	if !strings.Contains(text, "NEEDS YOU") || !strings.Contains(text, "Nothing needs you") || !strings.HasSuffix(text, "\n") {
		t.Errorf("frame %q", text)
	}
	if once := s.must("top", "--once"); !strings.Contains(once, "NEEDS YOU") {
		t.Errorf("--once %q", once)
	}
}

// widest is the width of a frame's widest line, and whether a line was clipped to fit.
func widest(frame string) (width int, clipped bool) {
	for line := range strings.Lines(frame) {
		line = strings.TrimRight(line, "\r\n")
		width = max(width, utf8.RuneCountInString(line))
		clipped = clipped || strings.HasSuffix(line, "~")
	}
	return width, clipped
}

// --width sets how wide a printed frame is, and a width that is no width is a usage error.
func TestTopPrintsAFrameAsWideAsAsked(t *testing.T) {
	s := newSession(t)
	if width, clipped := widest(s.must("top", "--once", "--width", "30")); width != 30 || !clipped {
		t.Errorf("--width 30 printed a frame %d wide, clipped %v", width, clipped)
	}
	if code, _, stderr := s.run("top", "--once", "--width", "-1"); code != cli.ExitUsage || !strings.Contains(stderr, "--width") {
		t.Errorf("--width -1: exit %d, stderr %q", code, stderr)
	}
}

// A frame printed to a terminal is as wide as the terminal, not a fixed width.
func TestTopFillsTheTerminalItPrintsTo(t *testing.T) {
	s := newSession(t)
	typed, held, err := ptypair.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Close()
	if err := unix.IoctlSetWinsize(int(typed.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 40, Row: 24}); err != nil {
		t.Fatal(err)
	}
	read := make(chan string, 1)
	go func() {
		var out strings.Builder
		buffer := make([]byte, 4096)
		for {
			n, err := typed.Read(buffer)
			out.Write(buffer[:n])
			if err != nil || strings.Contains(out.String(), "q quit") {
				read <- out.String()
				return
			}
		}
	}()
	var stderr strings.Builder
	code := cli.Run([]string{"top", "--once"}, held, &stderr, cli.Options{Socket: s.daemon.Socket, Session: s.daemon.Session})
	_ = held.Close()
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	select {
	case frame := <-read:
		if width, clipped := widest(frame); width != 40 || !clipped {
			t.Errorf("a terminal 40 columns wide got a frame %d wide, clipped %v:\n%s", width, clipped, frame)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the frame never reached the terminal")
	}
}

func TestTopReadsAsOneDocument(t *testing.T) {
	s := newSession(t)
	var snapshot top.Snapshot
	if err := json.Unmarshal([]byte(s.must("top", "--json")), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Needs.Revision == 0 || snapshot.Floor.Revision == 0 {
		t.Errorf("snapshot %+v", snapshot)
	}
}

func TestTopExitsWhenNothingAnswers(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nothing")
	if code, _, stderr := run(t, socket, "top"); code != cli.ExitUnreachable {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// Each thing the TUI tells an operator to run has to exist.
func TestEveryCommandTopNamesExists(t *testing.T) {
	for _, action := range api.WorkActions {
		item := api.NeedItem{ID: "x", Work: &api.WorkNeed{Orchestrator: &ledger.Orchestrator{WorkerID: "w"}}}
		command, ok := top.CommandFor(action, item)
		if !ok {
			t.Fatalf("%s names no command", action)
		}
		words := strings.Fields(strings.TrimPrefix(command, "ropeshift "))
		if !cli.HasCommand(words[0] + " " + words[1]) {
			t.Errorf("%s names %q", action, command)
		}
	}
}

func TestEveryViewReadsFromTheCommandLine(t *testing.T) {
	s := newSession(t)
	for _, name := range api.Views {
		t.Run(name, func(t *testing.T) {
			var stamp api.Stamp
			if err := json.Unmarshal([]byte(s.must("view", name, "--json")), &stamp); err != nil {
				t.Fatal(err)
			}
			if stamp.AsOf == 0 {
				t.Errorf("stamp %+v", stamp)
			}
		})
	}
}

func TestAViewReadsAsTextToo(t *testing.T) {
	s := newSession(t)
	if text := s.must("view", api.ViewNeedsYou); !strings.Contains(text, "items") {
		t.Errorf("view %q", text)
	}
}

func TestAViewFiltersByCategory(t *testing.T) {
	s := newSession(t)
	s.must("question", "record", "--text", "May it stay?", "--category", "product")
	var view api.Questions
	if err := json.Unmarshal([]byte(s.must("view", api.ViewQuestions, "--category", "no-such-category", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Questions) != 0 {
		t.Errorf("questions %+v", view.Questions)
	}
}

func TestAViewNobodyServesExitsAsUsage(t *testing.T) {
	s := newSession(t)
	if code, _, _ := s.run("view", "no-such-view"); code != cli.ExitUsage {
		t.Errorf("exit %d", code)
	}
}

func TestEffectCommands(t *testing.T) {
	s := newSession(t)
	w := apitest.NewWorld(t, s.daemon)
	lane := w.Running("wo-effect", "Add the retry budget")
	raised := lane.Effect("lane-wo-effect/1/input/nudge", "input", 1)

	if text := s.must("effect", "list"); !strings.Contains(text, raised.Key) || !strings.Contains(text, "waiting") {
		t.Errorf("list %q", text)
	}
	var row ledger.UnknownEffect
	if err := json.Unmarshal([]byte(s.must("effect", "show", raised.ID, "--json")), &row); err != nil {
		t.Fatal(err)
	}
	if row.Key != raised.Key {
		t.Errorf("row %+v", row)
	}
	if code, _, _ := s.run("effect", "decide", raised.ID); code != cli.ExitUsage {
		t.Errorf("no outcome: exit %d", code)
	}
	if code, _, _ := s.run("effect", "decide", raised.ID, "--outcome", "maybe"); code != cli.ExitUsage {
		t.Errorf("no such outcome: exit %d", code)
	}
	if code, _, _ := s.run("effect", "decide", "no-such-effect", "--outcome", "happened"); code != cli.ExitNotFound {
		t.Errorf("no such effect: exit %d", code)
	}
	s.must("effect", "decide", raised.ID, "--outcome", "retry", "--reason", "It never arrived")
	if text := s.must("effect", "show", raised.ID); !strings.Contains(text, "retry") {
		t.Errorf("show %q", text)
	}
	if code, _, _ := s.run("effect", "decide", raised.ID, "--outcome", "happened"); code != cli.ExitConflict {
		t.Errorf("a second word: exit %d", code)
	}
}

func TestEventsFollowTheLedgerFromARevision(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	text := s.must("events", "--after", "0", "--limit", "2", "--json")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines: %q", len(lines), text)
	}
	var revisions []int64
	for _, line := range lines {
		var event api.StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, event.Rev)
	}
	if !slices.Equal(revisions, []int64{1, 2}) {
		t.Errorf("revisions %v", revisions)
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	s := newSession(t)
	s.must("sign-out")
	if code, _, stderr := s.run("work", "create", "--title", "After the session"); code != cli.ExitForbidden {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// TestSignOutNeedsOnlyTheToken holds that whoever holds a session can end it: a terminal that
// would answer is never asked, and a session that is not live is nothing to end, not a reason to
// sign in again.
func TestSignOutNeedsOnlyTheToken(t *testing.T) {
	s := newSession(t)
	token, err := s.daemon.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: authority.LoginTTLMs})
	if err != nil {
		t.Fatal(err)
	}
	terminal := &asked{passphrase: apitest.Passphrase}
	if code, stdout, stderr := s.runAs(token.Token, terminal.read, "sign-out"); code != cli.ExitOK || stdout != "signed out\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := s.runAs(token.Token, nil, "category", "add", "pricing"); code != cli.ExitForbidden {
		t.Fatalf("the ended session: exit %d, %s", code, stderr)
	}
	code, _, stderr := s.runAs(token.Token, terminal.read, "sign-out")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "no session to end") || strings.Contains(stderr, "passphrase") {
		t.Errorf("an ended session: exit %d, %s", code, stderr)
	}
	stale, err := s.daemon.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	code, _, stderr = s.runAs(stale.Token, nil, "sign-out")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "no session to end") || strings.Contains(stderr, "passphrase") {
		t.Errorf("an expired session: exit %d, %s", code, stderr)
	}
	code, _, stderr = s.runAs("", terminal.read, "sign-out")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "no session to end") {
		t.Errorf("no session: exit %d, %s", code, stderr)
	}
	if len(terminal.prompts) != 0 {
		t.Errorf("sign-out asked %v", terminal.prompts)
	}
}
