package claudecode

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// takingTerminal is a pane whose session takes a submitted line as the CLI does: an Enter that it does not lose
// reaches the session's hooks at once, as a compaction's start for `/compact` and as a prompt for anything else.
type takingTerminal struct {
	*fake.Terminal
	mu      sync.Mutex
	session *paneSession
	// lose counts the Enters the session loses before it takes one, and loseAll loses every one.
	lose    int
	loseAll bool
	// typed and entered run as text is typed and as an Enter is pressed, before the session takes it.
	typed   func()
	entered func()
	last    string
}

func (t *takingTerminal) Type(ctx context.Context, name, text string) error {
	if err := t.Terminal.Type(ctx, name, text); err != nil {
		return err
	}
	t.mu.Lock()
	t.last = text
	typed := t.typed
	t.mu.Unlock()
	if typed != nil {
		typed()
	}
	return nil
}

func (t *takingTerminal) Keys(ctx context.Context, name string, keys ...string) error {
	if err := t.Terminal.Keys(ctx, name, keys...); err != nil {
		return err
	}
	if !slices.Contains(keys, keyEnter) {
		return nil
	}
	t.mu.Lock()
	entered, lost := t.entered, t.loseAll || t.lose > 0
	if t.lose > 0 {
		t.lose--
	}
	last, session := t.last, t.session
	t.mu.Unlock()
	if entered != nil {
		entered()
	}
	if lost || session == nil {
		return nil
	}
	// A hook is stamped by the clock the session reads.
	session.mu.Lock()
	atMs := session.now().UnixMilli()
	session.mu.Unlock()
	event := lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: atMs}
	if last == compactCommand {
		event = lanes.HookEvent{Event: hookPreCompact, Detail: compactRequested, AtMs: atMs}
	}
	session.takeHook(event)
	return nil
}

// takingPane is a session at rest in an open pane whose session takes what the terminal says, with a short grace and a
// short hold at the start of a turn.
func takingPane(t *testing.T, terminal *takingTerminal) *paneSession {
	t.Helper()
	terminal.Terminal = fake.NewTerminal()
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	name := paneName(spec)
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Options{Terminal: terminal}).newPaneSession(spec, name, ports.SessionHandle{ID: sessionHandleID(spec)}, lanes.CostMeter{})
	s.grace, s.notice = 50*time.Millisecond, 300*time.Millisecond
	terminal.mu.Lock()
	terminal.session = s
	terminal.mu.Unlock()
	s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: 110})
	return s
}

func countKeys(keys []string, key string) int {
	n := 0
	for _, pressed := range keys {
		if pressed == key {
			n++
		}
	}
	return n
}

// Typed input is delivered once the session's hook shows it took it, and a lost Enter is pressed again with the text
// never typed twice.
func TestTypedInputIsDeliveredOnceTheSessionTakesIt(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		lose    int
		enters  int
		compact bool
	}{
		{name: "a line taken at once", enters: 1},
		{name: "a line whose Enter was lost", lose: 1, enters: 2},
		{name: "a compaction taken at once", enters: 1, compact: true},
		{name: "a compaction whose Enter was lost", lose: 1, enters: 2, compact: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			terminal := &takingTerminal{lose: tt.lose}
			s := takingPane(t, terminal)
			var err error
			if tt.compact {
				err = s.compact(t.Context())
			} else {
				_, err = s.typeInput(t.Context(), ports.Input{Text: "a nudge"})
			}
			if err != nil {
				t.Fatalf("input the session took was not delivered: %v", err)
			}
			if got := countKeys(terminal.KeysOf(s.name), keyEnter); got != tt.enters {
				t.Errorf("pressed Enter %d times, want %d", got, tt.enters)
			}
			if typed := terminal.TypedInto(s.name); len(typed) != 1 {
				t.Errorf("typed %q, want the text once", typed)
			}
		})
	}
}

// Typed input the session never took is no input it holds. Its text may stand in the session's prompt, so nothing is
// typed after it until the session shows it took a line, and it says ErrAwaitingAnswer as well, because a session that
// takes no line may show a dialog no hook told of.
func TestTypedInputTheSessionNeverTookIsNotDelivered(t *testing.T) {
	t.Parallel()
	for _, compact := range []bool{false, true} {
		name := "a line"
		if compact {
			name = "a compaction"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			terminal := &takingTerminal{loseAll: true}
			s := takingPane(t, terminal)
			var err error
			if compact {
				err = s.compact(t.Context())
			} else {
				_, err = s.typeInput(t.Context(), ports.Input{Text: "a nudge"})
			}
			if !errors.Is(err, ports.ErrInputUnconfirmed) || !errors.Is(err, ports.ErrAwaitingAnswer) {
				t.Fatalf("input the session never took came back as %v, want ErrInputUnconfirmed", err)
			}
			if got := countKeys(terminal.KeysOf(s.name), keyEnter); got != 2 {
				t.Errorf("pressed Enter %d times, want 2", got)
			}
			refusesInput(t, s)
			if typed := terminal.TypedInto(s.name); len(typed) != 1 {
				t.Errorf("typed %q after a line the session never took", typed)
			}
			s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: time.Now().UnixMilli()})
			s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: time.Now().UnixMilli() + 1})
			terminal.mu.Lock()
			terminal.loseAll = false
			terminal.mu.Unlock()
			typesFreely(t, s)
		})
	}
}

// A dialog that a hook tells of between the text and its Enter, or while the session has not shown it took a line,
// gets no Enter.
func TestADialogBeforeTheSessionTookALineGetsNoEnter(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		enters int
		at     func(terminal *takingTerminal, dialog func())
	}{
		{"between the text and its Enter", 0, func(terminal *takingTerminal, dialog func()) { terminal.typed = dialog }},
		{"after a lost Enter", 1, func(terminal *takingTerminal, dialog func()) { terminal.entered = dialog }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			terminal := &takingTerminal{loseAll: true}
			s := takingPane(t, terminal)
			tt.at(terminal, func() {
				s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationPermission, AtMs: time.Now().UnixMilli()})
			})
			if _, err := s.typeInput(t.Context(), ports.Input{Text: "a nudge"}); !errors.Is(err, ports.ErrInputUnconfirmed) {
				t.Fatalf("input that met a dialog came back as %v, want ErrInputUnconfirmed", err)
			}
			if got := countKeys(terminal.KeysOf(s.name), keyEnter); got != tt.enters {
				t.Errorf("pressed Enter %d times, want %d: an Enter answers the dialog", got, tt.enters)
			}
		})
	}
}

// An Enter the session took begins a turn whose hooks can come late. A permission or an MCP server's dialog that turn
// opens in its first seconds is told only once its Notification arrives, after the grace, and it gets no second Enter,
// because an Enter answers it.
func TestADialogOfATurnTheFirstEnterMayHaveBegunGetsNoSecondEnter(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{lanes.NotificationPermission, lanes.NotificationElicitation} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			terminal := &takingTerminal{loseAll: true}
			s := takingPane(t, terminal)
			told := make(chan struct{})
			var first sync.Once
			terminal.entered = func() {
				first.Do(func() {
					time.AfterFunc(2*s.grace, func() {
						s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: kind, AtMs: time.Now().UnixMilli()})
						close(told)
					})
				})
			}
			_, err := s.typeInput(t.Context(), ports.Input{Text: "a nudge"})
			<-told
			if got := countKeys(terminal.KeysOf(s.name), keyEnter); got != 1 {
				t.Fatalf("pressed Enter %d times: a second Enter went where the first one's turn may show a dialog", got)
			}
			if !errors.Is(err, ports.ErrInputUnconfirmed) {
				t.Errorf("input that met a dialog came back as %v, want ErrInputUnconfirmed", err)
			}
		})
	}
}

// A lost Enter is pressed again only once a turn the first one may have begun is past its first seconds, as any key
// after a prompt is.
func TestASecondEnterWaitsOutTheStartOfATurnTheFirstMayHaveBegun(t *testing.T) {
	t.Parallel()
	terminal := &takingTerminal{lose: 1}
	s := takingPane(t, terminal)
	var pressed []time.Time
	terminal.entered = func() { pressed = append(pressed, time.Now()) }
	if _, err := s.typeInput(t.Context(), ports.Input{Text: "a nudge"}); err != nil {
		t.Fatalf("a line whose second Enter the session took was not delivered: %v", err)
	}
	if len(pressed) != 2 {
		t.Fatalf("pressed Enter %d times, want 2", len(pressed))
	}
	// The hold reads whole milliseconds, so the gap may fall short of it by part of one at each end.
	if gap := pressed[1].Sub(pressed[0]); gap < s.notice-2*time.Millisecond {
		t.Errorf("the second Enter went %v after the first, inside the %v a turn's start holds the keys", gap, s.notice)
	}
}

// A prompt's or a compaction's hook that reaches the pane after the grace, stamped at the first Enter, shows the
// session took the line: the second Enter does not move the bar, and nothing holds the keys once the turn ends.
func TestALateHookOfTheFirstEnterShowsTheSessionTookTheLine(t *testing.T) {
	t.Parallel()
	for _, compact := range []bool{false, true} {
		name := "a line"
		if compact {
			name = "a compaction"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			terminal := &takingTerminal{loseAll: true}
			s := takingPane(t, terminal)
			var enters int
			var firstMs int64
			terminal.entered = func() {
				s.mu.Lock()
				now := s.now().UnixMilli()
				s.mu.Unlock()
				enters++
				if enters == 1 {
					firstMs = now
					return
				}
				event := lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: firstMs}
				if compact {
					event = lanes.HookEvent{Event: hookPreCompact, Detail: compactRequested, AtMs: firstMs}
				}
				s.takeHook(event)
			}
			var err error
			if compact {
				err = s.compact(t.Context())
			} else {
				_, err = s.typeInput(t.Context(), ports.Input{Text: "a nudge"})
			}
			if err != nil {
				t.Fatalf("a line the session took, whose hook came late, was not delivered: %v", err)
			}
			s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: time.Now().UnixMilli() + 1})
			s.mu.Lock()
			held := s.keysHeld()
			s.mu.Unlock()
			if held || s.observe(t.Context()).AwaitsPerson {
				t.Fatalf("keys held %v, awaits a person %v after the turn ended", held, s.observe(t.Context()).AwaitsPerson)
			}
		})
	}
}

// Text typed with its Enter held stands in the prompt until the session takes a line entered after it was typed: an
// older prompt that arrives late does not lift the hold, and one at or after the typing does.
func TestAnOlderPromptDoesNotLiftTheHoldOfTextTypedWithItsEnterHeld(t *testing.T) {
	t.Parallel()
	terminal := &takingTerminal{loseAll: true}
	s := takingPane(t, terminal)
	var typedAt int64
	terminal.typed = func() {
		s.mu.Lock()
		typedAt = s.now().UnixMilli()
		s.mu.Unlock()
		s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationPermission, AtMs: typedAt})
	}
	before := time.Now().UnixMilli()
	if _, err := s.typeInput(t.Context(), ports.Input{Text: "a nudge"}); !errors.Is(err, ports.ErrInputUnconfirmed) {
		t.Fatalf("input whose Enter was held came back as %v, want ErrInputUnconfirmed", err)
	}
	unsent := func() int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.unsentMs
	}
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: before - 1})
	if unsent() == 0 {
		t.Fatal("a prompt older than the typed text lifted its hold")
	}
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: typedAt + 1})
	if unsent() != 0 {
		t.Fatal("a prompt after the text was typed did not lift its hold")
	}
}
