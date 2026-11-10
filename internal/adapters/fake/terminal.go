package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/ports"
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
	screens  int
	failRead error
	// attached lists every pane a client attached to, in the order they attached.
	attached []string
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
	// clients are the attached clients, each ended by closing its channel.
	clients []chan struct{}
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

// Attached lists the panes a client attached to, in the order they attached, including those that
// have since detached.
func (t *Terminal) Attached() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.attached)
}

// Detach ends every client attached to a pane, as a person pressing the detach keys would, and leaves
// the pane running.
func (t *Terminal) Detach(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		pane.detachAll()
	}
}

func (p *fakePane) detachAll() {
	for _, client := range p.clients {
		close(client)
	}
	p.clients = nil
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

// Screen returns the current screen as a person sees it. It is not a capture, because nothing decides
// on it.
func (t *Terminal) Screen(_ context.Context, name string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failRead != nil {
		return "", t.failRead
	}
	pane, ok := t.panes[name]
	if !ok {
		return "", fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	t.screens++
	return pane.script.Screen, nil
}

// Screens counts every read of a screen for a person, on any pane.
func (t *Terminal) Screens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.screens
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

// Clients counts the clients attached to a pane.
func (t *Terminal) Clients(_ context.Context, name string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failRead != nil {
		return 0, t.failRead
	}
	pane, ok := t.panes[name]
	if !ok {
		return 0, fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	return len(pane.clients), nil
}

// Attach holds a client on a pane until Detach, the end of the context, or the close of the pane.
func (t *Terminal) Attach(ctx context.Context, name string, _ ports.Console) error {
	t.mu.Lock()
	pane, ok := t.panes[name]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf("%s: %w", name, ports.ErrNotFound)
	}
	client := make(chan struct{})
	pane.clients = append(pane.clients, client)
	t.attached = append(t.attached, name)
	t.mu.Unlock()
	select {
	case <-client:
		return nil
	case <-ctx.Done():
		t.mu.Lock()
		defer t.mu.Unlock()
		if pane, ok := t.panes[name]; ok {
			pane.clients = slices.DeleteFunc(pane.clients, func(c chan struct{}) bool { return c == client })
		}
		return ctx.Err()
	}
}

// Close removes a pane, and ends the clients attached to it.
func (t *Terminal) Close(_ context.Context, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[name]; ok {
		pane.detachAll()
	}
	delete(t.panes, name)
	return nil
}

var _ ports.Terminal = (*Terminal)(nil)
