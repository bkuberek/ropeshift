package delivery

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// EstimateSet is the estimates made together at one ledger revision (ADR-0026, FR-EST-10): the open work orders
// of a plan version intake recorded, or one work order made outside a plan. Its digest covers what its
// estimates say, so a baseline names exactly what was shown. A set never changes, and a later set that
// estimates the same work order holds its current estimate from then on.
type EstimateSet struct {
	ID               string     `json:"id"`
	Plan             string     `json:"plan_id,omitempty"`
	PlanVersion      int64      `json:"plan_version,omitempty"`
	Rev              int64      `json:"ledger_rev"`
	EstimatorVersion int        `json:"estimator_version"`
	PolicyVersion    int64      `json:"policy_version"`
	Digest           string     `json:"digest"`
	CreatedAt        int64      `json:"created_at"`
	Estimates        []Estimate `json:"estimates"`
}

// Seal readies a set to keep: its estimates in work order order, each naming the set, when it was made, and that
// it is its work order's current estimate, and the digest of what they say. The estimates it was given do not
// change.
func (s EstimateSet) Seal() EstimateSet {
	s.Estimates = slices.Clone(s.Estimates)
	slices.SortStableFunc(s.Estimates, func(a, b Estimate) int { return cmp.Compare(a.WorkOrder, b.WorkOrder) })
	said := make([]Estimate, len(s.Estimates))
	for i := range s.Estimates {
		e := &s.Estimates[i]
		e.SetID, e.CreatedAt, e.Current = s.ID, s.CreatedAt, true
		said[i] = e.said()
	}
	// An estimate is plain data, so it always encodes.
	body, _ := json.Marshal(said)
	sum := sha256.Sum256(body)
	s.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return s
}

// said is what an estimate says of its work order, apart from its ID, its set, when it was made, and whether it is
// still current.
func (e Estimate) said() Estimate {
	e.ID, e.SetID, e.CreatedAt, e.Current = "", "", 0, false
	return e
}

// SaysTheSame says two estimates say the same of their work order, wherever, whenever, and at whatever revision
// they were made.
func (e Estimate) SaysTheSame(other Estimate) bool {
	a, b := e.said(), other.said()
	a.Rev, b.Rev = 0, 0
	// An estimate is plain data, so it always encodes, and two that encode alike say the same.
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
