package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Notifier tells members something needs them, on one channel. Delivery is at least once: a crash can
// repeat a notice, and Delivery and Attempt let a receiver see that. An error says what went wrong in
// words that hold no address and no secret, because it reaches a log and the ledger.
type Notifier interface {
	// Channel names this channel in the ledger's record of a delivery.
	Channel() string
	// Notify hands one notice over, and returns once the channel took it or refused.
	Notify(ctx context.Context, notice ledger.Notice) error
}
