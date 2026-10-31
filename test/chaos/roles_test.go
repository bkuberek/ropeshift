//go:build chaos

package chaos

import (
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

var childRoles = map[string]func(*child) error{
	"note":              roleNote,
	"create-work-order": roleCreateWorkOrder,
}

func roleNote(c *child) error {
	return c.store.Write(c.ctx, c.scope, "note", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type:    "note.added",
			Actor:   c.actor,
			Subject: ledger.Subject{Kind: "note", ID: "note"},
		})
		return err
	})
}

func roleCreateWorkOrder(c *child) error {
	id := c.spec.WorkOrder
	if err := c.attempted("create", id, id, 0); err != nil {
		return err
	}
	_, err := c.ops.Create(c.ctx, c.scope, workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "from a child"}, c.actor)
	if err != nil {
		return err
	}
	return c.completed("create", id, id, 0, 0)
}
