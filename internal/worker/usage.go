package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
)

// recordUsage tells the ledger what the generation now running has spent: the cost its turns reported, the turns
// that reported one, and the turns that could not, because a stopped turn reports nothing (ADR-0037). The ledger raises
// what it holds of the generation to each figure and never lowers one, so a generation that reports again at its end
// leaves the highest word. A lane whose lease is gone has no word the ledger would take.
//
// A lane a limit parked reports the figure that stopped it first, as it stood, and then what it has spent since. The
// highest of each figure is not the figure that stopped it: a known turn that cost little after the stop raises the number
// of known turns, which lowers the mean a turn of unknown cost is charged at, and the highest of each figure then charges
// less than the stop did. The ledger keeps the highest charge it was told of, so the stop's own report is what holds the
// work order at its limit.
func (r *laneRun) recordUsage(ctx context.Context) {
	if r.fenced.Load() {
		return
	}
	if parked := r.parkingOf(); parked != nil && parked.Usage != nil && parked.Generation == r.currentGeneration() {
		r.reportUsage(ctx, *parked.Usage)
	}
	r.reportUsage(ctx, r.reportedUsage())
}

func (r *laneRun) reportUsage(ctx context.Context, usage ledger.GenerationUsage) {
	payload, err := json.Marshal(usage)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if _, err := r.w.Record(ctx, r.w.event(ledger.UploadGenerationUsage, r.record(), payload)); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}
