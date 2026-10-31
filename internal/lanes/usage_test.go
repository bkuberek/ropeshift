package lanes

import "testing"

func TestContextCountsEveryTokenSent(t *testing.T) {
	usage := Usage{InputTokens: 3, CacheCreationTokens: 100, CacheReadTokens: 900, OutputTokens: 50}
	if got := usage.Context(); got != 1003 {
		t.Errorf("context %d", got)
	}
}

func TestCostIsADifference(t *testing.T) {
	tests := []struct {
		name    string
		start   float64
		totals  []float64
		want    float64
		session float64
	}{
		{"a first generation", 0, []float64{0.1, 0.25}, 0.25, 0.25},
		{"a generation that resumes a session", 1, []float64{1.1, 1.25}, 0.25, 1.25},
		{"a total that does not rise", 0.5, []float64{0.5}, 0, 0.5},
		{"a total that falls adds nothing", 0.5, []float64{0.25}, 0, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meter := CostMeter{SessionTotalUSD: tt.start}
			for _, total := range tt.totals {
				meter.ObserveTurn(total)
			}
			if !near(meter.CostUSD, tt.want) || !near(meter.SessionTotalUSD, tt.session) {
				t.Errorf("cost %v, session %v", meter.CostUSD, meter.SessionTotalUSD)
			}
		})
	}
}

func TestAStoppedTurnIsAnUnknownCost(t *testing.T) {
	var meter CostMeter
	meter.ObserveTurn(0.1)
	meter.ObserveStopped()
	meter.ObserveTurn(0.3)
	if meter.UnknownTurns != 1 || !near(meter.CostUSD, 0.3) {
		t.Errorf("%+v", meter)
	}
}

func near(a, b float64) bool {
	diff := a - b
	return diff < 1e-9 && diff > -1e-9
}
