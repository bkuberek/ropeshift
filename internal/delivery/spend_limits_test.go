package delivery

import (
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
)

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func bandOf(p50, p85 float64, outcomes int, baselined bool) *SpendBand {
	return &SpendBand{P50: p50, P85: p85, Outcomes: outcomes, Baselined: baselined}
}

func policyWith(t *testing.T, changes map[string]string) authority.SpendPolicy {
	t.Helper()
	policy := authority.DefaultSpendPolicy()
	for key, value := range changes {
		var err error
		if policy, err = policy.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return policy
}

// A work order with a trusted band has its soft limit at the soft quantile and its hard limit at the multiplier times
// the band's p85, and the band it reads is the baseline when it has one.
func TestLimitsFollowTheBand(t *testing.T) {
	tests := []struct {
		name         string
		policy       map[string]string
		band         *SpendBand
		soft, hard   float64
		basis        LimitBasis
		reviewedHard float64
	}{
		{"a baselined band", nil, bandOf(4, 10, 8, true), 10, 20, BasisBaselinedBand, 0},
		{"the current band when nothing was baselined", nil, bandOf(4, 10, 8, false), 10, 20, BasisCurrentBand, 0},
		{"a soft limit at the p50", map[string]string{"soft_quantile": "p50"}, bandOf(4, 10, 8, true), 4, 20, BasisBaselinedBand, 0},
		{"a hard multiplier of three", map[string]string{"hard_multiplier": "3"}, bandOf(4, 10, 8, true), 10, 30, BasisBaselinedBand, 0},
		{"exactly the outcomes that make a band trusted", nil, bandOf(4, 10, 6, true), 10, 20, BasisBaselinedBand, 0},
		{"a band of five outcomes uses the size's limit", nil, bandOf(4, 10, 5, true), 12.5, 25, BasisNoHistory, 0},
		{"no estimate uses the size's limit", nil, nil, 12.5, 25, BasisNoHistory, 0},
		{"a band open above its p50 is no band", nil, &SpendBand{P50: 4, P85: 4, Outcomes: 9, Open: true}, 12.5, 25, BasisNoHistory, 0},
		{"a band that costs nothing is no band", nil, bandOf(0, 0, 9, true), 12.5, 25, BasisNoHistory, 0},
		{"a lower trust threshold", map[string]string{"low_confidence_outcomes": "3"}, bandOf(4, 10, 3, true), 10, 20, BasisBaselinedBand, 0},
		{"a lower soft share when no band is trusted", map[string]string{"low_confidence_soft_share": "0.25"}, nil, 6.25, 25, BasisNoHistory, 0},
		{"a size limit of its own", map[string]string{"no_history_limit.medium": "40"}, nil, 20, 40, BasisNoHistory, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SpendLimitsOf(policyWith(t, tt.policy), SpendInput{Kind: "change", Size: "medium", Band: tt.band})
			if !near(got.Soft, tt.soft) || !near(got.Hard, tt.hard) || got.Basis != tt.basis {
				t.Errorf("limits %+v, want soft %v hard %v by %s", got, tt.soft, tt.hard, tt.basis)
			}
		})
	}
}

// With no band to trust, the hard limit is the size's, and a size nobody declared is a medium one.
func TestTheSizeDecidesTheLimitWithNoBand(t *testing.T) {
	policy := authority.DefaultSpendPolicy()
	for size, hard := range map[string]float64{"small": 10, "medium": 25, "large": 60, "unknown": 25, "": 25} {
		for _, kind := range []string{"change", "rework"} {
			if got := SpendLimitsOf(policy, SpendInput{Kind: kind, Size: size}); !near(got.Hard, hard) || !near(got.Soft, hard/2) {
				t.Errorf("a %s %s work order has limits %+v, want hard %v", size, kind, got, hard)
			}
		}
	}
}

// A review's hard limit is a share of the reviewed work order's hard limit, and its soft limit a share of its own.
func TestAReviewHasAShareOfTheReviewedLimit(t *testing.T) {
	policy := authority.DefaultSpendPolicy()
	got := SpendLimitsOf(policy, SpendInput{Kind: "review", ReviewedHard: 20})
	if !near(got.Hard, 10) || !near(got.Soft, 5) || got.Basis != BasisReview {
		t.Errorf("a review of work limited at 20 has %+v", got)
	}
	wide := policyWith(t, map[string]string{"review_share": "0.25", "review_soft_share": "0.8"})
	if got := SpendLimitsOf(wide, SpendInput{Kind: "review", ReviewedHard: 20}); !near(got.Hard, 5) || !near(got.Soft, 4) {
		t.Errorf("a review at a quarter has %+v", got)
	}
	if got := SpendLimitsOf(policy, SpendInput{Kind: "review"}); got.Hard != 0 || got.Soft != 0 {
		t.Errorf("a review of work with no limit has %+v", got)
	}
}

// An amount of 0 is no limit, and kinds of work that no limit covers have none.
func TestZeroIsNoLimitAndOtherKindsHaveNone(t *testing.T) {
	policy := policyWith(t, map[string]string{"no_history_limit.medium": "0"})
	if got := SpendLimitsOf(policy, SpendInput{Kind: "change", Size: "medium"}); got.Hard != 0 || got.Soft != 0 || got.Limited() {
		t.Errorf("a size limit of 0 gives %+v", got)
	}
	if !(SpendLimits{Hard: 1}).Limited() {
		t.Error("a hard limit of 1 is no limit")
	}
	for _, kind := range []string{"plan", "adopt", "contract_review", ""} {
		if got := SpendLimitsOf(authority.DefaultSpendPolicy(), SpendInput{Kind: kind, Size: "large"}); got.Limited() {
			t.Errorf("a %q work order has limits %+v", kind, got)
		}
	}
}

// The soft and the hard limit tell over an estimate from a stop.
func TestSpendIsPastItsLimits(t *testing.T) {
	limits := SpendLimits{Soft: 5, Hard: 10}
	for spend, want := range map[float64][2]bool{4.99: {false, false}, 5: {true, false}, 9.99: {true, false}, 10: {true, true}} {
		if soft, hard := limits.PastSoft(spend), limits.PastHard(spend); soft != want[0] || hard != want[1] {
			t.Errorf("spend %v is past soft %v and hard %v, want %v", spend, soft, hard, want)
		}
	}
	if (SpendLimits{}).PastSoft(1e9) || (SpendLimits{}).PastHard(1e9) {
		t.Error("no limit was passed")
	}
}
