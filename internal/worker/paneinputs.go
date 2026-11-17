package worker

import (
	"sync"
	"time"
)

// PaneInputs keeps when a member last typed into each lane's pane through the web pane. The web pane
// reaches a pane through the worker's terminal and attaches no client to it, so this is how the worker
// learns that a person is at a pane no terminal is attached to (ADR-0018). It keeps the newest input of
// each lane, and its zero value is ready to use.
type PaneInputs struct {
	mu   sync.Mutex
	last map[string]paneInput
}

// paneInput is the newest input into a lane's pane, with the generation whose pane took it.
type paneInput struct {
	generation int64
	at         time.Time
}

// Typed records that a member typed into a generation's pane at a time. Nil records nothing.
func (p *PaneInputs) Typed(lane string, generation int64, at time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil {
		p.last = map[string]paneInput{}
	}
	p.last[lane] = paneInput{generation: generation, at: at}
}

// TypedSince says a member typed into a generation's pane at a time or after it. Nil holds no input.
func (p *PaneInputs) TypedSince(lane string, generation int64, from time.Time) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	input, ok := p.last[lane]
	return ok && input.generation == generation && !input.at.Before(from)
}
