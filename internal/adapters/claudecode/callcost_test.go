package claudecode_test

import (
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// What one API call costs at list price, for the families the fixtures use: Sonnet is $3 in, $6 to write the cache for an
// hour, $0.30 to read it, and $15 out for a million tokens, and Opus 4.5 is $5, $10, $0.50 and $25.
func sonnetCall(input, cacheWrite, cacheRead, output float64) float64 {
	return (input*3 + cacheWrite*6 + cacheRead*0.30 + output*15) / 1e6
}

func opusCall(input, cacheWrite, cacheRead, output float64) float64 {
	return (input*5 + cacheWrite*10 + cacheRead*0.50 + output*25) / 1e6
}

// runningTotals are the cost events of a played session, in order, and the turn that ended it.
func runningTotals(t *testing.T, events []ports.DriverEvent) (totals []float64, ended *ports.TurnResult) {
	t.Helper()
	endedAt := -1
	for i, event := range events {
		switch event.Kind {
		case ports.EventCost:
			if endedAt >= 0 {
				t.Errorf("a running total came after the turn ended: %+v", event)
			}
			if event.Seq == 0 {
				t.Errorf("a running total carried no position: %+v", event)
			}
			totals = append(totals, event.RunningUSD)
		case ports.EventTurnEnded:
			endedAt = i
			ended = event.Turn
		}
	}
	if endedAt < 0 {
		t.Fatal("the turn never ended")
	}
	return totals, ended
}

func checkTotals(t *testing.T, got, want []float64) {
	t.Helper()
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
}

// A headless session reports its total cost only on the line that ends a turn, so a long turn would show no spend until it
// was over. The driver estimates it from the usage of each API call, at list price, and tells the worker as each call
// opens and as its output grows (FR-SPEND-5). The stream has the shapes a client run with partial messages writes: a
// message_start with the call's model and its tokens sent, a message_delta with its output so far, the call's assistant
// message with the same id, and a subagent's calls naming their parent. The assistant messages repeat what the stream events
// said and add nothing: a call counts once, whichever way it is reported. The turn's own total is lower than the estimate,
// and it is the one the turn ends with.
func TestALongTurnReportsWhatItsCallsCostAsTheyAreMade(t *testing.T) {
	driver, _, spec := session(t, "long-turn", nil)
	totals, turn := runningTotals(t, play(t, driver, spec))

	first, second := sonnetCall(3, 1500, 45000, 410), sonnetCall(5, 800, 47000, 2000)
	checkTotals(t, totals, []float64{
		sonnetCall(3, 1500, 45000, 2),
		first,
		first + sonnetCall(5, 800, 47000, 1),
		first + second,
		first + second + opusCall(10, 0, 5000, 1),
		first + second + opusCall(10, 0, 5000, 3000),
	})
	// What the client's own total says replaces what the calls were estimated at, which was higher.
	if turn == nil || turn.Cost.SessionTotalUSD != 0.12 || totals[len(totals)-1] <= 0.12 {
		t.Errorf("the turn ended with %+v after a running total of %v", turn, totals[len(totals)-1])
	}
}

// A web search is billed beside the tokens of the call that made it, at a hundredth of a dollar, from the count its usage
// carries. The count rides in the running estimate as the calls do: it grows with the stream event that shows it, it is not
// charged again by the assistant message that repeats it with the same message id, and a call reported by its assistant
// message alone brings its own.
func TestWebSearchesAreCountedOnceInTheRunningEstimate(t *testing.T) {
	driver, _, spec := session(t, "web-search-turn", nil)
	totals, turn := runningTotals(t, play(t, driver, spec))

	first := sonnetCall(20, 0, 30000, 500) + 2*0.01
	checkTotals(t, totals, []float64{
		sonnetCall(20, 0, 30000, 1),
		first,
		first + sonnetCall(25, 0, 31000, 300) + 1*0.01,
	})
	if turn == nil || turn.Cost.SessionTotalUSD != 0.05 {
		t.Errorf("the turn ended with %+v", turn)
	}
}

// A session that writes no stream events is known by its assistant messages alone: each message prices its call, a
// subagent's included, and a call the client writes once for each of its content blocks counts once, at the largest usage
// it showed. Without them a turn of subagents would show no spend until it was over.
func TestAssistantMessagesPriceTheTurnWhenNoStreamEventsArrive(t *testing.T) {
	driver, _, spec := session(t, "assistant-turn", nil)
	totals, turn := runningTotals(t, play(t, driver, spec))

	subagent := opusCall(9, 0, 6000, 2500)
	checkTotals(t, totals, []float64{
		sonnetCall(4, 1200, 40000, 600),
		sonnetCall(4, 1200, 40000, 600) + subagent,
		sonnetCall(4, 1200, 40000, 650) + subagent,
		sonnetCall(4, 1200, 40000, 650) + subagent + sonnetCall(5, 300, 41000, 800),
	})
	if turn == nil || turn.Cost.SessionTotalUSD != 0.09 {
		t.Errorf("the turn ended with %+v", turn)
	}
}
