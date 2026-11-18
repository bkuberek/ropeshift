package ledger

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Three lanes, one with two stopped turns: the charged spend is the recorded deltas plus two turns at the mean known
// turn cost of the generation, and it says so (FR-SPEND-3).
func TestChargedSpendAddsStoppedTurnsAtTheMean(t *testing.T) {
	got := Charge([][]GenerationUsage{
		{{CostUSD: 0.30, KnownTurns: 3}},
		{{CostUSD: 0.20, KnownTurns: 2}},
		{{CostUSD: 0.40, KnownTurns: 4, UnknownTurns: 2}},
	})
	if !near(got.RecordedUSD, 0.9) || !near(got.EstimatedUSD, 0.2) || !near(got.ChargedUSD, 1.1) || got.EstimatedTurns != 2 || got.Unpriced != 0 {
		t.Errorf("charged %+v", got)
	}
	if label := got.Label(); label != "estimated, at list price, with 2 turns estimated by average" {
		t.Errorf("the label reads %q", label)
	}
	if label := (Charged{EstimatedTurns: 1}).Label(); !strings.HasSuffix(label, "with 1 turn estimated by average") {
		t.Errorf("the label of one turn reads %q", label)
	}
	if label := (Charged{}).Label(); !strings.HasSuffix(label, "with 0 turns estimated by average") {
		t.Errorf("the label of none reads %q", label)
	}
}

// A generation that never saw a known turn is charged at the mean of its lane, and a lane with no known turn at all
// is charged nothing for its stopped turns, which the figure counts as unpriced so a surface can say so.
func TestAGenerationWithNoKnownTurnTakesItsLanesMean(t *testing.T) {
	got := Charge([][]GenerationUsage{
		{{CostUSD: 0.5, KnownTurns: 5}, {UnknownTurns: 3}},
		{{UnknownTurns: 2}},
	})
	if !near(got.RecordedUSD, 0.5) || !near(got.EstimatedUSD, 0.3) || got.EstimatedTurns != 5 || got.Unpriced != 2 {
		t.Errorf("charged %+v", got)
	}
	if !near(got.ChargedUSD, 0.8) {
		t.Errorf("charged %v, want 0.8", got.ChargedUSD)
	}
}

// A generation is charged at least its floor, which the store keeps as the highest charge the generation reached: a known turn
// that costs little lowers the mean its stopped turns are charged at, and the charge with it, and the floor is what says the
// work order did not get cheaper. The part of the floor that no recorded cost covers is an estimate, and counts as one.
func TestAGenerationIsChargedAtLeastItsFloor(t *testing.T) {
	// Cost 30 over 3 known turns charges one stopped turn at 10: 40. A cheap known turn after it makes 30.5 over 4, which
	// charges the stopped turn at 7.625: 38.125, unless the floor holds the 40.
	cheaper := GenerationUsage{CostUSD: 30.5, KnownTurns: 4, UnknownTurns: 1}
	if got := Charge([][]GenerationUsage{{cheaper}}); !near(got.ChargedUSD, 38.125) {
		t.Fatalf("without a floor the charge reads %v, want 38.125", got.ChargedUSD)
	}
	cheaper.FloorUSD = 40
	got := Charge([][]GenerationUsage{{cheaper}})
	if !near(got.ChargedUSD, 40) || !near(got.RecordedUSD, 30.5) || !near(got.EstimatedUSD, 9.5) || got.EstimatedTurns != 1 {
		t.Errorf("a floor of 40 over a computed 38.125 charged %+v", got)
	}
	// A floor below what is computed changes nothing, and a floor on one generation holds that generation alone.
	low := Charge([][]GenerationUsage{{{CostUSD: 30, KnownTurns: 3, UnknownTurns: 1, FloorUSD: 12}, {CostUSD: 1, KnownTurns: 1}}})
	if !near(low.ChargedUSD, 41) {
		t.Errorf("a floor below the charge changed it to %v", low.ChargedUSD)
	}
}

// A work order with no lane, or lanes that spent nothing, is charged nothing.
func TestNothingSpentIsNothingCharged(t *testing.T) {
	if got := Charge(nil); got != (Charged{}) {
		t.Errorf("no lanes charged %+v", got)
	}
	if got := Charge([][]GenerationUsage{{{}}, {{}, {}}}); got != (Charged{}) {
		t.Errorf("lanes that did nothing charged %+v", got)
	}
}

// turn is one turn of a generated session: a known one reports the session's running total, a stopped one nothing.
type turn struct {
	stopped bool
	delta   float64
}

// meterOf runs a generation's turns through the cost meter as the worker does, starting from the session total the
// generation before it stopped at.
func meterOf(start float64, turns []turn) (GenerationUsage, float64) {
	total := start
	var known, unknown int64
	var cost float64
	for _, tn := range turns {
		if tn.stopped {
			unknown++
			continue
		}
		total += tn.delta
		known++
		cost += tn.delta
	}
	return GenerationUsage{CostUSD: cost, KnownTurns: known, UnknownTurns: unknown}, total
}

// Generated work orders of lanes, generations, resumed sessions, a /clear, and stopped turns: the fold agrees with
// charging each stopped turn at the mean of the known turns of its generation, or of its lane.
func TestChargedSpendFoldsGeneratedSequences(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for range 300 {
		var lanes [][]GenerationUsage
		var wantRecorded, wantEstimated float64
		var wantTurns, wantUnpriced int64
		for range 1 + rng.IntN(4) {
			var generations []GenerationUsage
			var perGeneration [][]turn
			session := 0.0
			for range 1 + rng.IntN(4) {
				var turns []turn
				for range rng.IntN(6) {
					turns = append(turns, turn{stopped: rng.IntN(3) == 0, delta: float64(1+rng.IntN(50)) / 100})
				}
				// A resumed session carries its running total on, and a /clear or a handoff starts it over.
				if rng.IntN(3) == 0 {
					session = 0
				}
				usage, end := meterOf(session, turns)
				session = end
				generations = append(generations, usage)
				perGeneration = append(perGeneration, turns)
			}
			var laneCost float64
			var laneKnown int64
			for _, g := range generations {
				laneCost += g.CostUSD
				laneKnown += g.KnownTurns
			}
			for i, turns := range perGeneration {
				mean := -1.0
				if g := generations[i]; g.KnownTurns > 0 {
					mean = g.CostUSD / float64(g.KnownTurns)
				} else if laneKnown > 0 {
					mean = laneCost / float64(laneKnown)
				}
				for _, tn := range turns {
					switch {
					case tn.stopped && mean >= 0:
						wantEstimated += mean
						wantTurns++
					case tn.stopped:
						wantTurns++
						wantUnpriced++
					default:
						wantRecorded += tn.delta
					}
				}
			}
			lanes = append(lanes, generations)
		}
		got := Charge(lanes)
		if !near(got.RecordedUSD, wantRecorded) || !near(got.EstimatedUSD, wantEstimated) || got.EstimatedTurns != wantTurns || got.Unpriced != wantUnpriced ||
			!near(got.ChargedUSD, wantRecorded+wantEstimated) {
			t.Fatalf("lanes %+v charged %+v, want recorded %v estimated %v over %d turns, %d unpriced", lanes, got, wantRecorded, wantEstimated, wantTurns, wantUnpriced)
		}
	}
}

// The limit a member's acts set is the highest one they set, whatever order the events come in, and nothing else
// the ledger says about a work order's spend moves it.
func TestARaisedLimitIsTheHighestOneAMemberSet(t *testing.T) {
	change := func(eventType string, after float64) Event {
		data, err := json.Marshal(SpendLimitChange{WorkOrder: "w", AfterUSD: after})
		if err != nil {
			t.Fatal(err)
		}
		return Event{Type: eventType, Data: data}
	}
	tests := []struct {
		name   string
		events []Event
		want   float64
	}{
		{"no event", nil, 0},
		{"a raise", []Event{change(EventSpendLimitRaised, 50)}, 50},
		{"a resume", []Event{change(EventSpendResumed, 48)}, 48},
		{"the highest of several, in any order", []Event{change(EventSpendLimitRaised, 75), change(EventSpendResumed, 48), change(EventSpendLimitRaised, 50)}, 75},
		{"other events about the spend", []Event{{Type: EventSpendSoftLimit, Data: []byte(`{"after_usd":900}`)}, {Type: EventSpendLimited}}, 0},
		{"a record that cannot be read", []Event{{Type: EventSpendLimitRaised, Data: []byte(`nope`)}, change(EventSpendLimitRaised, 20)}, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RaisedLimit(tt.events); !near(got, tt.want) {
				t.Errorf("the raised limit is %v, want %v", got, tt.want)
			}
		})
	}
}

// A lane has brought its work order to the hard limit when the other lanes' spend and its own reach it, and the
// client's own cap is what remains and a share of the limit beyond it.
func TestAnAllowanceSaysWhenALaneCrossedAndWhatBudgetItGets(t *testing.T) {
	allowance := SpendAllowance{HardUSD: 25, OthersUSD: 7, HeadroomShare: 0.1}
	for lane, crossed := range map[float64]bool{0: false, 17.99: false, 18: true, 30: true} {
		if got := allowance.Crossed(lane); got != crossed {
			t.Errorf("a lane charged %v: crossed %v, want %v", lane, got, crossed)
		}
	}
	for lane, budget := range map[float64]float64{0: 20.5, 10: 10.5, 18: 2.5, 30: 2.5} {
		if got := allowance.BudgetUSD(lane); !near(got, budget) {
			t.Errorf("a lane charged %v starts with a budget of %v, want %v", lane, got, budget)
		}
	}
	none := SpendAllowance{OthersUSD: 40, HeadroomShare: 0.1}
	if none.Crossed(1e9) || none.BudgetUSD(0) != 0 {
		t.Errorf("a work order with no hard limit was held to one: %+v", none)
	}
}
