//go:build chaos

package chaos

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
)

// digestOnce is the oracle of a generation's digest (FR-SLOG-2). Whichever side of the kill the digest was on,
// the ledger holds the one event that says it was kept, and the digest of the generation the lane ran.
func digestOnce(w *world, spec childSpec) error {
	if err := countEvents(w, ledger.EventGenerationDigested, spec.Lane, 1); err != nil {
		return err
	}
	var kept ledger.GenerationDigest
	err := w.store.Read(context.Background(), w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		kept, err = tx.Lanes().Digest(ledger.DigestOwnerLane, spec.Lane, 1)
		return err
	})
	if errors.Is(err, ports.ErrNotFound) {
		return contract.Violatedf("the ledger holds no digest of lane %s", spec.Lane)
	}
	if err != nil {
		return err
	}
	if kept.Generation != 1 || len(kept.Body) == 0 {
		return contract.Violatedf("the digest kept is %+v", kept)
	}
	return nil
}

// workerDigestCase kills a real worker between a generation's end and the record of its digest. The next start
// finds the digest ended and unrecorded and records it, and a digest that never reached the kill is built from
// the same generation, so the ledger holds one either way.
func workerDigestCase() crashCase {
	c := workerLaneCase()
	c.name, c.op, c.steps = "record a digest", "worker.digest", []string{"ended"}
	laneSettled := c.settled
	c.settled = func(w *world, spec childSpec) error {
		if err := laneSettled(w, spec); err != nil {
			return err
		}
		return digestOnce(w, spec)
	}
	return c
}
