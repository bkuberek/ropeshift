package delivery

import (
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/workorders"
)

// SpendBand is the cost band a spend limit reads (FR-SPEND-9): the band recorded at plan approval, so a limit does
// not move while the work runs, or the work order's current band when it has no baseline. It is the one place a
// claim-time estimate reaches a decision, and only through the limit's own derivation.
type SpendBand struct {
	P50, P85 float64
	// Outcomes is how many finished work orders the band was drawn from.
	Outcomes int
	// Open says the band's p85 is not a measurement, because it lay past the longest record.
	Open bool
	// Baselined says the band is the one recorded at plan approval.
	Baselined bool
}

// LimitBasis says where a work order's limits came from.
type LimitBasis string

// The bases of a limit.
const (
	// BasisBaselinedBand is the band recorded at plan approval.
	BasisBaselinedBand LimitBasis = "baselined_band"
	// BasisCurrentBand is the work order's current band, for work with no baseline.
	BasisCurrentBand LimitBasis = "current_band"
	// BasisNoHistory is the size's limit, for a band from too few outcomes or no estimate at all.
	BasisNoHistory LimitBasis = "no_history"
	// BasisReview is a share of the reviewed work order's hard limit.
	BasisReview LimitBasis = "review"
)

// SpendLimits are a work order's soft and hard limits in US dollars at list price. A soft limit warns and stops
// nothing, a hard limit stops, and 0 means no limit.
type SpendLimits struct {
	Soft  float64
	Hard  float64
	Basis LimitBasis
}

// Limited says the work order has a limit at all.
func (l SpendLimits) Limited() bool { return l.Soft > 0 || l.Hard > 0 }

// PastSoft says charged spend reached the soft limit.
func (l SpendLimits) PastSoft(spend float64) bool { return l.Soft > 0 && spend >= l.Soft }

// PastHard says charged spend reached the hard limit.
func (l SpendLimits) PastHard(spend float64) bool { return l.Hard > 0 && spend >= l.Hard }

// SpendInput is what a work order's limits are derived from. Kind and Size are its own. Band is its baselined band,
// or its current one, or nil when it has no estimate. ReviewedHard is the hard limit of the work order a review
// judges.
type SpendInput struct {
	Kind         string
	Size         string
	Band         *SpendBand
	ReviewedHard float64
}

// SpendLimitsOf derives a work order's limits under a spend policy (FR-SPEND-4). A `change` or `rework` with a
// trusted band has its soft limit at the soft quantile of the band and its hard limit at the multiplier times the
// band's p85. With a band from fewer than the trusted number of outcomes, an open band, or no estimate, its hard
// limit is the size's and its soft limit a share of that. A `review` has a share of the reviewed work order's hard
// limit, and a soft limit that is a share of its own. Every other kind has none.
func SpendLimitsOf(policy authority.SpendPolicy, in SpendInput) SpendLimits {
	switch workorders.Kind(in.Kind) {
	case workorders.KindChange, workorders.KindRework:
		if band := in.Band; band != nil && !band.Open && band.P85 > 0 && int64(band.Outcomes) >= policy.LowConfidenceOutcomes {
			soft, basis := band.P85, BasisCurrentBand
			if policy.SoftQuantile == "p50" {
				soft = band.P50
			}
			if band.Baselined {
				basis = BasisBaselinedBand
			}
			return SpendLimits{Soft: soft, Hard: policy.HardMultiplier * band.P85, Basis: basis}
		}
		hard := sizeLimit(policy, in.Size)
		return SpendLimits{Soft: policy.LowConfidenceSoftShare * hard, Hard: hard, Basis: BasisNoHistory}
	case workorders.KindReview:
		hard := policy.ReviewShare * in.ReviewedHard
		return SpendLimits{Soft: policy.ReviewSoftShare * hard, Hard: hard, Basis: BasisReview}
	}
	return SpendLimits{}
}

// sizeLimit is the hard limit of a work order with no band to trust. A size nobody declared is read as a medium one.
func sizeLimit(policy authority.SpendPolicy, size string) float64 {
	switch size {
	case "small":
		return policy.NoHistoryLimit.Small
	case "large":
		return policy.NoHistoryLimit.Large
	}
	return policy.NoHistoryLimit.Medium
}
