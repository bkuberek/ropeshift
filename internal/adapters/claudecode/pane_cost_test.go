package claudecode

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// A pane's status line is where an interactive session says what it has spent before its turn ends, and a total that
// grew is the usage report a hard limit is held to in the middle of a turn (FR-SPEND-5). A total that did not grow, or
// fell, says nothing new.
func TestAStatusLineThatShowsMoreSpendIsAUsageReport(t *testing.T) {
	d := &Driver{}
	s := d.newPaneSession(ports.SessionSpec{TempDir: t.TempDir()}, "lane-1-1", ports.SessionHandle{}, lanes.CostMeter{SessionTotalUSD: 2})
	costs := func() []float64 {
		var out []float64
		for _, event := range s.queue {
			if event.Kind == ports.EventCost {
				out = append(out, event.RunningUSD)
			}
		}
		return out
	}
	status := func(cost float64) {
		s.takeStatus(lanes.HookEvent{Status: &lanes.StatusReport{CostUSD: cost, ContextKnown: true, ContextTokens: 100}})
	}
	status(1.5)
	if got := costs(); len(got) != 0 {
		t.Errorf("a total below the session's own reported %v", got)
	}
	status(3.5)
	status(3.5)
	status(3.0)
	status(7.25)
	got := costs()
	if len(got) != 2 || got[0] != 3.5 || got[1] != 7.25 {
		t.Errorf("the status lines reported %v, want the two totals that grew", got)
	}
	if s.totalUSD != 7.25 {
		t.Errorf("the session's total reads %v", s.totalUSD)
	}
}
