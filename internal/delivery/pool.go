package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Pooled strips a project's delivery records to what the installation's pool holds (ADR-0026, FR-EST-13): the
// kind, size, and capability class, the four durations, and the cost, with the outcome, the end, and whether the
// cost was a lower bound, which the estimator reads to draw only `done` work, the most recent first, and to mark
// a cost band `at_least`. A pooled record names no work order, repository, agent, model, plan, or milestone, so it
// says nothing of whose work it was, and holds no tokens, waits, or change size.
func Pooled(records []Record) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		out = append(out, Record{
			Keys:    Keys{Kind: r.Kind, Size: r.Size, CapabilityClass: r.CapabilityClass},
			Outcome: r.Outcome, Ended: r.Ended, ElapsedMs: r.ElapsedMs, DeliveryMs: r.DeliveryMs, LaneMs: r.LaneMs, PersonMs: r.PersonMs,
			Usage: Usage{CostUSD: r.Usage.CostUSD, CostLowerBound: r.Usage.CostLowerBound},
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
