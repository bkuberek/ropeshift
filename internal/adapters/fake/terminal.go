package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"ropeshift.dev/ropeshift/internal/ports"
)

// PaneScript is what one scripted pane shows. Screens is the current screen, and Moves
// says which screen a key leads to from the screen it was pressed on.
type PaneScript struct {
	Screen string
	Moves  map[string]map[string]string
	// Refused counts the keys a screen ignores before it takes one, as a dialog that has
	// just opened does.
	Refused int
}

// Terminal holds scripted panes in memory. No process starts.
type Terminal struct {
	mu       sync.Mutex
	next     PaneScript
	panes    map[string]*fakePane
	captures int
	failRead error
}

type fakePane struct {
	spec    ports.PaneSpec
	script  PaneScript
	pid     int
	dead    bool
	exit    ports.ExitStatus
	keys    []string
	typed   []string
	signals []ports.Signal
}

// NewTerminal returns a terminal with no panes.
func NewTerminal() *Terminal { return &Terminal{panes: map[string]*fakePane{}} }

// Play sets what the next pane shows.
func (t *Terminal) Play(script PaneScript) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next = script
}

// FailReads makes every read incomplete, until it is given nil.
func (t *Terminal) FailReads(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failRead = err
}

// Exit ends a pane's process.
func (t *Terminal) Exit(name string, status ports.ExitStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		pane.dead, pane.exit = true, status
	}
}

// Show changes what a pane shows.
func (t *Terminal) Show(name, screen string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		pane.script.Screen = screen
	}
}

// KeysOf lists the named keys a pane took, in order.
func (t *Terminal) KeysOf(name string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		return slices.Clone(pane.keys)
	}
	return nil
}

// TypedInto lists the text a pane took, in order.
func (t *Terminal) TypedInto(name string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		return slices.Clone(pane.typed)
	}
	return nil
}

// SignalsTo lists the signals a pane took, in order.
func (t *Terminal) SignalsTo(name string) []ports.Signal {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		return slices.Clone(pane.signals)
	}
	return nil
}

// Opened lists the panes this terminal holds.
func (t *Terminal) Opened() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Sorted(maps.Keys(t.panes))
}

// Open starts the next script.
func (t *Terminal) Open(_ context.Context, spec ports.PaneSpec) (ports.Pane, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, held := t.panes[spec.Name]; held {
		return ports.Pane{}, fmt.Errorf("a pane named %s exists", spec.Name)
	}
	pane := &fakePane{spec: spec, script: t.next, pid: len(t.panes) + 1000}
	t.panes[spec.Name] = pane
	return ports.Pane{Name: spec.Name, PID: pane.pid}, nil
}

// Pane reads one pane.
func (t *Terminal) Pane(_ context.Context, name string) (ports.Pane, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failRead != nil {
		return ports.Pane{}, t.failRead
	}
	pane, ok := t.panes[name]
	if !ok {
		return ports.Pane{}, fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	return ports.Pane{Name: name, PID: pane.pid, Dead: pane.dead, Exit: pane.exit}, nil
}

// Capture returns the current screen.
func (t *Terminal) Capture(_ context.Context, name string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failRead != nil {
		return "", t.failRead
	}
	pane, ok := t.panes[name]
	if !ok {
		return "", fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	t.captures++
	return pane.script.Screen, nil
}

// Captures counts every read of a screen, on any pane.
func (t *Terminal) Captures() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.captures
}

// Keys presses named keys.
func (t *Terminal) Keys(_ context.Context, name string, keys ...string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	pane, ok := t.panes[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	for _, key := range keys {
		pane.keys = append(pane.keys, key)
		if pane.script.Refused > 0 {
			pane.script.Refused--
			continue
		}
		if next, ok := pane.script.Moves[pane.script.Screen][key]; ok {
			pane.script.Screen = next
		}
	}
	return nil
}

// Type enters literal text.
func (t *Terminal) Type(_ context.Context, name, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	pane, ok := t.panes[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	pane.typed = append(pane.typed, text)
	return nil
}

// Signal records a signal, and an interrupt, a termination, or a kill ends the process.
func (t *Terminal) Signal(_ context.Context, name string, signal ports.Signal) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	pane, ok := t.panes[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	pane.signals = append(pane.signals, signal)
	pane.dead, pane.exit = true, ports.ExitStatus{Signal: signal}
	return nil
}

// Close removes a pane.
func (t *Terminal) Close(_ context.Context, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.panes, name)
	return nil
}

var _ ports.Terminal = (*Terminal)(nil)
