// Package notify holds the channels a member hears through. Each carries a ledger.Notice and nothing
// else, and docs/notifications.md states what that is. This is the one package that opens a connection
// off this machine, and it does so only for a webhook a workspace published.
package notify

import (
	"context"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// The channels, as the ledger's record of a delivery names them.
const (
	ChannelWeb     = "web"
	ChannelDesktop = "desktop"
	ChannelWebhook = "webhook"
)

// Web tells a member through the page they have open. Taking a notice is all it does: the ledger event
// that records the delivery reaches every open page on the stream, and the page reads Needs you again.
type Web struct{}

// Channel names this channel.
func (Web) Channel() string { return ChannelWeb }

// Notify takes a notice, and the event the tick records is the delivery.
func (Web) Notify(ctx context.Context, _ ledger.Notice) error { return ctx.Err() }

var _ ports.Notifier = Web{}
