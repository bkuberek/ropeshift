package control

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A resume's allowance follows one chain: the mean charge of the work order's generations that recorded spend, else the
// work order's estimate, else nothing, which the caller refuses. It is never 0 and never beyond what the work or its
// estimate holds.
func TestAResumeAllowanceFollowsItsChain(t *testing.T) {
	estimated := &delivery.SpendBand{P50: 12, P85: 30, Outcomes: 8}
	tests := []struct {
		name        string
		charged     float64
		generations int
		band        *delivery.SpendBand
		allowance   float64
		basis       string
		ok          bool
	}{
		{"the mean of two generations", 32, 2, nil, 16, ledger.AllowanceMeanGeneration, true},
		{"one generation is the whole charge", 30, 1, estimated, 30, ledger.AllowanceMeanGeneration, true},
		{"recorded spend wins over an estimate", 9, 3, estimated, 3, ledger.AllowanceMeanGeneration, true},
		{"no generation recorded spend, so the estimate", 0, 0, estimated, 12, ledger.AllowanceEstimate, true},
		{"generations that were charged nothing, so the estimate", 0, 2, estimated, 12, ledger.AllowanceEstimate, true},
		{"no generation and no estimate", 0, 0, nil, 0, "", false},
		{"no generation and an estimate that costs nothing", 0, 0, &delivery.SpendBand{P50: 0, P85: 0, Outcomes: 8}, 0, "", false},
		{"charge with no generation to divide it by", 5, 0, nil, 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowance, basis, ok := chooseAllowance(tt.charged, tt.generations, tt.band)
			if ok != tt.ok || !near(allowance, tt.allowance) || basis != tt.basis {
				t.Errorf("allowance %v by %q, ok %v; want %v by %q, ok %v", allowance, basis, ok, tt.allowance, tt.basis, tt.ok)
			}
			if ok && allowance <= 0 {
				t.Errorf("a granted allowance of %v is no allowance", allowance)
			}
		})
	}
}

// fakeLanes holds the lanes of a work order and their generations, and nothing else a store holds.
type fakeLanes struct {
	ports.LaneReader
	rows        []ledger.LaneRow
	generations map[string][]ledger.Generation
}

func (l fakeLanes) Rows() ([]ledger.LaneRow, error) { return l.rows, nil }

func (l fakeLanes) Generations(lane string) ([]ledger.Generation, error) {
	return l.generations[lane], nil
}

func usageOfGeneration(usage ledger.GenerationUsage) ledger.Generation {
	return ledger.Generation{Usage: usage}
}

// What a work order's lanes recorded and its estimate decide, read as a resume reads them: a generation that recorded
// nothing is no generation, a work order with neither a record nor an estimate is refused with the way out, and a
// baselined band is the estimate before a current one.
func TestAResumeAllowanceIsReadFromTheLanesAndTheEstimate(t *testing.T) {
	order := workorders.WorkOrder{ID: "w", Kind: workorders.KindChange}
	spent := fakeLanes{
		rows: []ledger.LaneRow{{ID: "a", WorkOrder: "w"}, {ID: "b", WorkOrder: "w"}, {ID: "other", WorkOrder: "x"}},
		generations: map[string][]ledger.Generation{
			"a":     {usageOfGeneration(ledger.GenerationUsage{CostUSD: 20, KnownTurns: 2}), usageOfGeneration(ledger.GenerationUsage{})},
			"b":     {usageOfGeneration(ledger.GenerationUsage{CostUSD: 12, KnownTurns: 3})},
			"other": {usageOfGeneration(ledger.GenerationUsage{CostUSD: 900, KnownTurns: 9})},
		},
	}
	idle := fakeLanes{
		rows:        []ledger.LaneRow{{ID: "a", WorkOrder: "w"}},
		generations: map[string][]ledger.Generation{"a": {usageOfGeneration(ledger.GenerationUsage{})}},
	}
	baselined := fakeEstimates{baseline: map[string]delivery.Estimate{"w": band(8, 6, 15)}, current: map[string]delivery.Estimate{"w": band(8, 99, 200)}}
	tests := []struct {
		name      string
		lanes     fakeLanes
		estimates fakeEstimates
		charged   float64
		allowance float64
		basis     string
		refused   bool
	}{
		{"the mean of the generations that recorded spend", spent, fakeEstimates{}, 32, 16, ledger.AllowanceMeanGeneration, false},
		{"no spend and a baselined estimate", idle, baselined, 0, 6, ledger.AllowanceEstimate, false},
		{"no spend and a current estimate only", idle, fakeEstimates{current: map[string]delivery.Estimate{"w": band(8, 4, 10)}}, 0, 4, ledger.AllowanceEstimate, false},
		{"no spend and no estimate", idle, fakeEstimates{}, 0, 0, "", true},
		{"no spend and an estimate no history backs", idle, fakeEstimates{current: map[string]delivery.Estimate{"w": {Status: delivery.NoHistory}}}, 0, 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads := spendReads{lanes: tt.lanes, estimates: tt.estimates, orders: orderReads{orders: map[string]workorders.WorkOrder{"w": order}}}
			allowance, basis, err := reads.resumeAllowance(order, tt.charged)
			if tt.refused {
				if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "ropeshift spend raise") || !strings.Contains(err.Error(), "w") {
					t.Fatalf("a work order with nothing to size a generation by was answered %v, want a refusal that says to raise the limit", err)
				}
				return
			}
			if err != nil || !near(allowance, tt.allowance) || basis != tt.basis {
				t.Errorf("allowance %v by %q: %v; want %v by %q", allowance, basis, err, tt.allowance, tt.basis)
			}
		})
	}
}
