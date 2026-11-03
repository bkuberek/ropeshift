package fake

import (
	"context"
	"sync"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Notifier keeps what it is told.
type Notifier struct {
	mu      sync.Mutex
	name    string
	notices []ledger.Notice
	fail    error
}

// NewNotifier returns a channel of this name.
func NewNotifier(name string) *Notifier { return &Notifier{name: name} }

// Fail refuses every notice, until it is given nil.
func (n *Notifier) Fail(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fail = err
}

// Notices lists what it was told, in order.
func (n *Notifier) Notices() []ledger.Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ledger.Notice{}, n.notices...)
}

// Channel names this channel.
func (n *Notifier) Channel() string { return n.name }

// Notify keeps one notice, under the rule a real channel holds: a context that ended sends nothing.
func (n *Notifier) Notify(ctx context.Context, notice ledger.Notice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fail != nil {
		return n.fail
	}
	n.notices = append(n.notices, notice)
	return nil
}

var _ ports.Notifier = (*Notifier)(nil)
