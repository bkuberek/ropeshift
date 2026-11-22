package worker

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// A pane's end names the hook that ended it and a headless result names none, and Stops and ends don't always pair:
// in neither does one end answer with what another turn's Stop left.
func TestATurnsEndReadsTheBackgroundWorkOfItsOwnStop(t *testing.T) {
	t.Parallel()
	const pane, headless = ports.RunModeInteractive, ports.RunModeHeadless
	stop := func(atMs int64, tasks int) func(*laneState) {
		return func(s *laneState) { s.noteStop(tasks, time.UnixMilli(atMs), atMs) }
	}
	prompt := func(atMs int64) func(*laneState) {
		return func(s *laneState) { s.notePrompt(atMs) }
	}
	failure := func(atMs int64) func(*laneState) {
		return func(s *laneState) { s.noteStopFailure(atMs) }
	}
	replayed := func(lastMs int64) func(*laneState) {
		return func(s *laneState) {
			s.noteStop(0, time.UnixMilli(lastMs), 0)
			s.noteReplayedStop(lastMs)
		}
	}
	generation := func(s *laneState) { s.newGeneration(time.UnixMilli(0), 0) }
	type hooks = []func(*laneState)
	for _, tt := range []struct {
		name  string
		mode  string
		heard hooks
		ends  []int64
		left  []bool
	}{
		{"each pane end has its Stop", pane, hooks{stop(20, 2), prompt(30), stop(40, 1)}, []int64{20, 40}, []bool{true, true}},
		{"a pane ends at each Stop a hook blocked", pane, hooks{stop(20, 1), stop(30, 1)}, []int64{20, 30}, []bool{true, true}},
		{"a Stop whose end the pane dropped", pane, hooks{stop(20, 1), prompt(30), stop(40, 0)}, []int64{40}, []bool{false}},
		{"a compaction's end", pane, hooks{stop(20, 1), prompt(30), stop(40, 1)}, []int64{20, 35, 40}, []bool{true, false, true}},
		{"a failure's end", pane, hooks{stop(20, 1), prompt(30), failure(40)}, []int64{20, 40}, []bool{true, false}},
		{"each result has its Stop", headless, hooks{stop(20, 2), prompt(30), stop(40, 0)}, []int64{7, 9}, []bool{true, false}},
		{"one result after a Stop a hook blocked", headless, hooks{stop(20, 1), stop(30, 0)}, []int64{7, 9}, []bool{false, false}},
		{"a Stop heard after the next prompt", headless, hooks{prompt(30), stop(20, 1), stop(40, 0)}, []int64{7, 9}, []bool{true, false}},
		{"a late prompt follows its own Stop", headless, hooks{stop(20, 1), stop(40, 0), prompt(30)}, []int64{7, 9}, []bool{true, false}},
		{"a repeated Stop", headless, hooks{stop(20, 1), prompt(30), stop(40, 0), stop(20, 1)}, []int64{7, 9, 11}, []bool{true, false, false}},
		{"a result with no Stop", headless, hooks{stop(20, 1), prompt(30), failure(40)}, []int64{7, 9}, []bool{true, false}},
		{"a repeat of a replayed Stop", headless, hooks{replayed(20), stop(20, 1)}, []int64{7}, []bool{false}},
		{"a new generation", headless, hooks{stop(20, 1), generation}, []int64{7}, []bool{false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			run := &laneRun{state: &laneState{}, startedMode: tt.mode}
			for _, hear := range tt.heard {
				hear(run.state)
			}
			var got []bool
			for _, seq := range tt.ends {
				got = append(got, run.endLeftWork(seq))
			}
			if !slices.Equal(got, tt.left) {
				t.Errorf("ends %v left work %v, want %v", tt.ends, got, tt.left)
			}
		})
	}
}
