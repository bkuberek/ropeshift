package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// recordUsage tells the ledger what the generation now running has spent: the cost its turns reported, the turns
// that reported one, and the turns that could not, because a stopped turn reports nothing (ADR-0037). It replaces what
// the generation last reported, so a generation that reports again at its end leaves its last word. A lane whose lease
// is gone has no word the ledger would take.
func (r *laneRun) recordUsage(ctx context.Context) {
	if r.fenced.Load() {
		return
	}
	payload, err := json.Marshal(ledger.GenerationUsage{CostUSD: r.cost.CostUSD, KnownTurns: r.cost.KnownTurns, UnknownTurns: r.cost.UnknownTurns})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if _, err := r.w.Record(ctx, r.w.event(ledger.UploadGenerationUsage, r.record(), payload)); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}
