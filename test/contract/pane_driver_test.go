package contract

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/tmux"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/doubles"
)

// paneRig is the real driver, a real private tmux server, fakeclaude in the pane, and a
// stand-in for the worker socket that hands each hook to the driver.
type paneRig struct {
	t        *testing.T
	driver   *claudecode.Driver
	terminal *tmux.Terminal
	path     string
	spec     ports.SessionSpec
}

const paneTestToken = "pane-test-token"

func newPaneRig(t *testing.T, fixture string) *paneRig {
	t.Helper()
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	path := doubles.InstallFakeClaude(t, fixture)
	home := t.TempDir()
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{
		Env: []string{"PATH=" + path, "HOME=" + home}, Terminal: terminal, StopGrace: 2 * time.Second,
	})
	short := apitest.ShortDir(t)
	socket := filepath.Join(short, "w.sock")
	spool := filepath.Join(short, "spool")
	spec := baseSpec(t)
	spec.Env = []string{
		"PATH=" + path, "HOME=" + home, lanes.EnvWorkerSocket + "=" + socket, lanes.EnvSpool + "=" + spool,
		lanes.EnvLaneToken + "=" + paneTestToken, lanes.EnvLaneID + "=" + spec.Lane,
		lanes.EnvGeneration + "=" + strconv.FormatInt(spec.Generation, 10), lanes.EnvWorkOrder + "=" + spec.WorkOrder,
	}
	spec.RunMode, spec.ReadyTimeout = ports.RunModeInteractive, 20*time.Second
	spec.Binary, spec.TempDir = doubles.Ropeshift(t), filepath.Join(short, "t")
	if err := os.MkdirAll(spec.TempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &paneRig{t: t, driver: driver, terminal: terminal, path: path, spec: spec}
	r.serveHooks(socket, spool)
	return r
}

// serveHooks answers hooks the way a worker does. A hook that missed its ack spools, so
// this also reads the spool, or a loaded machine would lose an event no worker would.
func (r *paneRig) serveHooks(socket, spool string) {
	r.t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = listener.Close() })
	stop := make(chan struct{})
	r.t.Cleanup(func() { close(stop) })
	take := func(event lanes.HookEvent) {
		event.Lane, event.Generation = r.spec.Lane, r.spec.Generation
		r.driver.TakeHook(r.t.Context(), event)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request lanes.Request
				if json.Unmarshal(line, &request) != nil || request.Hook == nil {
					return
				}
				take(*request.Hook)
				_, _ = conn.Write([]byte(`{"ok":true}` + "\n"))
			}()
		}
	}()
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			taken := spool + ".taken"
			if os.Rename(spool, taken) != nil {
				continue
			}
			raw, err := os.ReadFile(taken)
			if err != nil {
				continue
			}
			for line := range strings.SplitSeq(string(raw), "\n") {
				var event lanes.HookEvent
				if json.Unmarshal([]byte(line), &event) == nil && event.Event != "" {
					take(event)
				}
			}
		}
	}()
}

func (r *paneRig) start() *driverSession {
	r.t.Helper()
	handle, err := r.driver.Start(r.t.Context(), r.spec)
	if err != nil {
		r.t.Fatal(err)
	}
	events, err := r.driver.Events(r.t.Context(), handle)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = r.driver.Stop(context.Background(), handle, ports.StopNow) })
	ctx, cancel := context.WithTimeout(r.t.Context(), 20*time.Second)
	r.t.Cleanup(cancel)
	return &driverSession{t: r.t, ctx: ctx, rig: DriverRig{Driver: r.driver}, handle: handle, events: events}
}

func (r *paneRig) log(name string) []string {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(r.path), name+".log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for line := range strings.Lines(string(raw)) {
		_, text, _ := strings.Cut(strings.TrimSpace(line), " ")
		out = append(out, text)
	}
	return out
}

func TestPaneStartAnswersTrustAndHandsOverTheBrief(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	s := r.start()
	bound := s.until(ports.EventSessionBound)
	if bound.SessionID == "" || bound.ModelResolved == "" {
		t.Errorf("bound %+v", bound)
	}
	if turn := s.until(ports.EventTurnEnded).Turn; turn.Status != ports.TurnCompleted {
		t.Errorf("turn %+v", turn)
	}
	if got := r.log("keys"); !slices.Equal(got, []string{"trust <down>", "trust <enter>"}) {
		t.Errorf("keys %v", got)
	}
	if messages := r.log("messages"); len(messages) != 1 {
		t.Errorf("messages %v", messages)
	}
	if bare := r.log("bare-enter"); len(bare) != 0 {
		t.Errorf("a bare Enter reached the dialog: %v", bare)
	}
}

// SessionStart fires with no prompt, so a worker must not read it as a brief that arrived:
// the first event of a session in a pane comes with its first prompt.
func TestPaneSessionStartIsNoEvidenceOfABrief(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	r.spec.Brief = ""
	s := r.start()
	select {
	case event := <-s.events:
		t.Errorf("an event before any prompt: %+v", event)
	case <-time.After(400 * time.Millisecond):
	}
	s.send2(t)
	if bound := s.until(ports.EventSessionBound); bound.SessionID == "" {
		t.Errorf("bound %+v", bound)
	}
}

func TestPaneSessionRunsTheUnmodifiedBinaryWithItsExactEnvironment(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	s := r.start()
	s.until(ports.EventTurnEnded)
	seen := doubles.SeenBy(t, doubles.BinDir(r.path))
	var session *doubles.Seen
	for i := range seen {
		if len(seen[i].Args) != 0 && seen[i].Args[0] == "--permission-mode" {
			session = &seen[i]
		}
	}
	if session == nil {
		t.Fatal("no session ran")
	}
	if slices.Contains(session.Args, "-p") {
		t.Errorf("an interactive session took -p: %v", session.Args)
	}
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		if _, found := session.Env[name]; found {
			t.Errorf("%s reached a lane, which could then reach the server that holds the others", name)
		}
	}
	if session.Env[lanes.EnvLaneToken] != paneTestToken {
		t.Errorf("the lane token did not reach the session")
	}
	if session.Env["TERM"] == "" {
		t.Error("a pane's session has no TERM")
	}
	if _, err := os.Stat(filepath.Join(r.spec.TempDir, "pane-launch-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the launch survived its read: %v", err)
	}
}

func TestPaneContextComesFromTheStatusLine(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	s := r.start()
	if got := s.until(ports.EventUsage).ContextTokens; got != 1003 {
		t.Errorf("context %d", got)
	}
	first := s.until(ports.EventTurnEnded).Turn
	if first.Cost.SessionTotalUSD != 0.1 {
		t.Errorf("first turn cost %+v", first.Cost)
	}
	s.send2(t)
	// The second turn's line reads zero, as one does after a compaction, which is no
	// context: the driver must keep the last known value and raise no usage event.
	s.until(ports.EventTurnEnded)
	observed, err := r.driver.Observe(t.Context(), s.handle)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ContextTokens != 1003 {
		t.Errorf("a zero on the status line moved the context to %d", observed.ContextTokens)
	}
}

// send2 sends a second input, whose receipt names the socket.
func (s *driverSession) send2(t *testing.T) {
	t.Helper()
	receipt, err := s.rig.Driver.Send(s.ctx, s.handle, ports.Input{Text: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Path != ports.SendSocket {
		t.Errorf("receipt %+v", receipt)
	}
}

func TestPaneStatusLineComposesWithAPersonsCommand(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	dir := filepath.Join(r.spec.Dir, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"statusLine":{"type":"command","command":"printf person-line # a comment"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	s := r.start()
	if got := s.until(ports.EventUsage).ContextTokens; got != 1003 {
		t.Errorf("the worker's copy: %d", got)
	}
	s.until(ports.EventTurnEnded)
	if lines := r.log("status-line"); !slices.Contains(lines, "person-line") {
		t.Errorf("the person's line is gone: %v", lines)
	}
}

func TestPaneCompactGoesByKeys(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	s := r.start()
	s.until(ports.EventTurnEnded)
	if err := r.driver.Compact(t.Context(), s.handle); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if keys := r.log("keys"); slices.Contains(keys, "prompt <enter>") {
			typed := strings.Join(keys[2:], "")
			if !strings.Contains(strings.ReplaceAll(typed, "prompt ", ""), "/compact") {
				t.Errorf("keys %v", keys)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("no compaction: %v", r.log("keys"))
}

func TestPaneEndingMidTurnIsACrash(t *testing.T) {
	r := newPaneRig(t, "pane-crash")
	s := r.start()
	end := s.ended()
	if !end.Crashed || end.Exit.Code != 137 {
		t.Errorf("end %+v", end)
	}
}

func TestPaneInterruptEndsTheProcess(t *testing.T) {
	r := newPaneRig(t, "pane-hold")
	s := r.start()
	s.until(ports.EventInputAdmitted)
	if err := r.driver.Stop(t.Context(), s.handle, ports.StopInterrupt); err != nil {
		t.Fatal(err)
	}
	if end := s.ended(); end.Crashed {
		t.Errorf("a stop read as a crash: %+v", end)
	}
	observed, err := r.driver.Observe(t.Context(), s.handle)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Liveness != ports.LivenessDead {
		t.Errorf("liveness %s", observed.Liveness)
	}
}

func TestPaneUnknownScreenStopsAndKeepsTheScreen(t *testing.T) {
	r := newPaneRig(t, "pane-stuck")
	r.spec.ReadyTimeout = 600 * time.Millisecond
	_, err := r.driver.Start(t.Context(), r.spec)
	if !errors.Is(err, ports.ErrUnknownScreen) {
		t.Fatalf("start: %v", err)
	}
	if keys := r.log("keys"); len(keys) != 0 {
		t.Errorf("a key reached an unknown screen: %v", keys)
	}
	raw, err := os.ReadFile(filepath.Join(r.spec.ConfigDir, "screen-1.txt"))
	if err != nil || !strings.Contains(string(raw), "never shown") {
		t.Errorf("the screen was not kept: %q, %v", raw, err)
	}
	if _, err := r.terminal.Pane(t.Context(), "lane-1-g1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the pane stayed: %v", err)
	}
}

func TestPaneResumeThatNeverReadiesFails(t *testing.T) {
	r := newPaneRig(t, "pane-stuck")
	r.spec.ReadyTimeout = 600 * time.Millisecond
	_, err := r.driver.Resume(t.Context(), r.spec, ports.ResumeRef{SessionID: "s"})
	if !errors.Is(err, ports.ErrResumeNotReady) {
		t.Fatalf("resume: %v", err)
	}
	if _, err := r.terminal.Pane(t.Context(), "lane-1-g1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the process was not stopped: %v", err)
	}
}

func TestPaneReadyScreenWithNoHookNamesNoSocket(t *testing.T) {
	r := newPaneRig(t, "pane-no-hook")
	r.spec.ReadyTimeout = 800 * time.Millisecond
	_, err := r.driver.Start(t.Context(), r.spec)
	if !errors.Is(err, ports.ErrUnknownScreen) {
		t.Fatalf("start: %v", err)
	}
	if messages := r.log("messages"); len(messages) != 0 {
		t.Errorf("a message with no socket: %v", messages)
	}
}

func TestPaneSessionIsFoundAndAdoptedByANewDriver(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	s := r.start()
	s.until(ports.EventTurnEnded)
	next := claudecode.New(osexec.Runner{}, claudecode.Options{Env: r.spec.Env, Terminal: r.terminal})
	spec := ports.SessionSpec{
		Lane: r.spec.Lane, Generation: r.spec.Generation, ConfigDir: r.spec.ConfigDir, TempDir: r.spec.TempDir, Dir: r.spec.Dir,
		Env: r.spec.Env, Binary: r.spec.Binary,
	}
	located, err := next.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !located.Found || !located.Readable || located.Liveness != lanes.LivenessAlive {
		t.Fatalf("located %+v", located)
	}
	records := []lanes.HookRecord{{HookEvent: lanes.HookEvent{
		Lane: spec.Lane, Generation: spec.Generation, Event: "SessionStart", SessionID: "s", Socket: socketOf(t, r),
	}, LastMs: 1}}
	if err := next.Adopt(t.Context(), located.Handle, ports.AdoptFrom{Spec: spec, Hooks: records}); err != nil {
		t.Fatal(err)
	}
	receipt, err := next.Send(t.Context(), located.Handle, ports.Input{Text: "after the restart"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Path != ports.SendSocket {
		t.Errorf("receipt %+v", receipt)
	}
	if err := next.Stop(t.Context(), located.Handle, ports.StopNow); err != nil {
		t.Fatal(err)
	}
}

func socketOf(t *testing.T, r *paneRig) string {
	t.Helper()
	// A session's socket is named for its process, and a driver's receipt address starts with r.
	found, err := filepath.Glob(filepath.Join(r.spec.TempDir, "cc-socks", "[0-9]*.sock"))
	if err != nil || len(found) != 1 {
		t.Fatalf("sockets %v, %v", found, err)
	}
	return found[0]
}

func TestADriverWithATerminalDeclaresBothRunModes(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	got, err := r.driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.RunModes, []string{ports.RunModeHeadless, ports.RunModeInteractive}) {
		t.Errorf("run modes %v", got.RunModes)
	}
	alone := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + r.path}})
	report, err := alone.Preflight(t.Context(), r.spec)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(report.Problems, "no terminal for an interactive session") {
		t.Errorf("problems %v", report.Problems)
	}
}
