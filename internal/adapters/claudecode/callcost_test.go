package claudecode_test

import (
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// A headless session reports its total cost only on the line that ends a turn, so a long turn would show no spend until it
// was over. The driver estimates it from the usage of each API call, at list price, and tells the worker as each call
// opens and as its output grows (FR-SPEND-5). The stream below has the shapes a client run with partial messages writes:
// a message_start with the call's model and its tokens sent, a message_delta with its output so far, and a subagent's calls
// naming their parent. The turn's own total is lower than the estimate, and it is the one the turn ends with.
func TestALongTurnReportsWhatItsCallsCostAsTheyAreMade(t *testing.T) {
	driver, _, spec := session(t, "long-turn", nil)
	events := play(t, driver, spec)

	// Sonnet is $3 in, $3.75 to write a cache, $0.30 to read one and $15 out, for a million tokens, and Opus is $5, $6.25,
	// $0.50 and $25. The tokens of each call are in the fixture.
	want := []float64{
		(3*3 + 1500*3.75 + 45000*0.30 + 2*15) / 1e6,
		(3*3 + 1500*3.75 + 45000*0.30 + 410*15) / 1e6,
		(3*3+1500*3.75+45000*0.30+410*15)/1e6 + (5*3+800*3.75+47000*0.30+1*15)/1e6,
		(3*3+1500*3.75+45000*0.30+410*15)/1e6 + (5*3+800*3.75+47000*0.30+2000*15)/1e6,
		(3*3+1500*3.75+45000*0.30+410*15)/1e6 + (5*3+800*3.75+47000*0.30+2000*15)/1e6 + (10*5+5000*0.50+1*25)/1e6,
		(3*3+1500*3.75+45000*0.30+410*15)/1e6 + (5*3+800*3.75+47000*0.30+2000*15)/1e6 + (10*5+5000*0.50+3000*25)/1e6,
	}
	var got []float64
	ended := -1
	for i, event := range events {
		switch event.Kind {
		case ports.EventCost:
			if ended >= 0 {
				t.Errorf("a running total came after the turn ended: %+v", event)
			}
			if event.Seq == 0 {
				t.Errorf("a running total carried no position: %+v", event)
			}
			got = append(got, event.RunningUSD)
		case ports.EventTurnEnded:
			ended = i
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d running totals, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("running total %d is %v, want %v", i, got[i], want[i])
		}
		if i > 0 && got[i] <= got[i-1] {
			t.Errorf("running total %d fell from %v to %v", i, got[i-1], got[i])
		}
	}
	if ended < 0 {
		t.Fatal("the turn never ended")
	}
	// What the client's own total says replaces what the calls were estimated at, which was higher.
	turn := events[ended].Turn
	if turn == nil || turn.Cost.SessionTotalUSD != 0.12 || got[len(got)-1] <= 0.12 {
		t.Errorf("the turn ended with %+v after a running total of %v", turn, got[len(got)-1])
	}
}
