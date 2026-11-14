package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"ropeshift.com/ropeshift/internal/workorders"
)

// dayMs is a day in milliseconds.
const dayMs = 24 * 60 * 60 * 1000

// Pooled strips a project's delivery records to what the installation's pool holds (ADR-0026, FR-EST-13): only
// work that reached `done`, which is all the estimator draws on, with its kind, size, and capability class, the
// four durations, the cost and whether it was a lower bound, which marks a cost band `at_least`, and the day it
// ended, by UTC, which orders the most recent first. A pooled record names no work order, repository, agent,
// model, plan, or milestone, so it says nothing of whose work it was, and holds no tokens, waits, or change size.
// Records that ended the same day order by what else they hold, so the estimator's draw stays the same.
func Pooled(records []Record) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if r.Outcome != workorders.Done {
			continue
		}
		out = append(out, Record{
			Keys:    Keys{Kind: r.Kind, Size: r.Size, CapabilityClass: r.CapabilityClass},
			Outcome: workorders.Done, Ended: r.Ended - r.Ended%dayMs, ElapsedMs: r.ElapsedMs, DeliveryMs: r.DeliveryMs, LaneMs: r.LaneMs,
			PersonMs: r.PersonMs, Usage: Usage{CostUSD: r.Usage.CostUSD, CostLowerBound: r.Usage.CostLowerBound},
		})
	}
	return out
}

// PoolDigest is the digest of what a project shares with the pool, in the order it shares it, so the project
// rewrites its share only when that changed. Sharing nothing has a digest too, apart from not sharing.
func PoolDigest(pooled []Record) string {
	if pooled == nil {
		pooled = []Record{}
	}
	// A pooled record is plain data, so it always encodes.
	body, _ := json.Marshal(pooled)
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
