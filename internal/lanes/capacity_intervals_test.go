package lanes_test

import (
	"maps"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

func counted(n int64) *int64 { return &n }

// TestCapsReachedNamesEachCapTheRunningLanesStandAt holds the fact FR-OBS-14 records: the running lanes
// stand at a cap when they reach it or pass it, a cap of zero caps nothing, and a report that does not
// count its lanes counts its lane sessions.
func TestCapsReachedNamesEachCapTheRunningLanesStandAt(t *testing.T) {
	lane := ledger.SessionResources{Kind: ledger.SessionLane}
	orchestrator := ledger.SessionResources{Kind: ledger.SessionOrchestrator}
	for name, tt := range map[string]struct {
		report ledger.ResourceReport
		want   map[string]int64
	}{
		"under both caps": {
			ledger.ResourceReport{RunningLanes: counted(1), Caps: ledger.LaneCaps{MaxLanes: 3, MaxLanesPerSignIn: 2}}, map[string]int64{},
		},
		"at the worker's cap": {
			ledger.ResourceReport{RunningLanes: counted(3), Caps: ledger.LaneCaps{MaxLanes: 3}}, map[string]int64{ledger.CapMaxLanes: 3},
		},
		"past a lowered cap": {
			ledger.ResourceReport{RunningLanes: counted(4), Caps: ledger.LaneCaps{MaxLanes: 2}}, map[string]int64{ledger.CapMaxLanes: 2},
		},
		"at the sign-in's cap alone": {
			ledger.ResourceReport{RunningLanes: counted(2), Caps: ledger.LaneCaps{MaxLanes: 5, MaxLanesPerSignIn: 2}},
			map[string]int64{ledger.CapMaxLanesPerSignIn: 2},
		},
		"at both caps": {
			ledger.ResourceReport{RunningLanes: counted(2), Caps: ledger.LaneCaps{MaxLanes: 2, MaxLanesPerSignIn: 2}},
			map[string]int64{ledger.CapMaxLanes: 2, ledger.CapMaxLanesPerSignIn: 2},
		},
		"no cap": {
			ledger.ResourceReport{RunningLanes: counted(9), Caps: ledger.LaneCaps{MaxLanesSet: true}}, map[string]int64{},
		},
		"counted from the lane sessions": {
			ledger.ResourceReport{Sessions: []ledger.SessionResources{lane, orchestrator, lane}, Caps: ledger.LaneCaps{MaxLanes: 2}},
			map[string]int64{ledger.CapMaxLanes: 2},
		},
		"an orchestrator holds no lane's place": {
			ledger.ResourceReport{Sessions: []ledger.SessionResources{lane, orchestrator}, Caps: ledger.LaneCaps{MaxLanes: 2}}, map[string]int64{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := lanes.CapsReached(tt.report); !maps.Equal(got, tt.want) {
				t.Errorf("caps reached %v, want %v", got, tt.want)
			}
		})
	}
}

// TestStepCapacityMovesTheIntervalsByOneReport holds how FR-OBS-14's intervals begin, stand, and end, and
// that a worker that stops reporting ends its intervals at its last report.
func TestStepCapacityMovesTheIntervalsByOneReport(t *testing.T) {
	const fresh = 45_000
	report := func(running, maxLanes int64) ledger.ResourceReport {
		return ledger.ResourceReport{RunningLanes: counted(running), Caps: ledger.LaneCaps{MaxLanes: maxLanes}}
	}
	interval := func(value, epoch, started, seen, ended int64) ledger.CapacityInterval {
		return ledger.CapacityInterval{Worker: "w1", Cap: ledger.CapMaxLanes, Value: value, Epoch: epoch, StartedAt: started, SeenAt: seen, EndedAt: ended}
	}
	standing := interval(2, 1, 1_000, 16_000, 0)
	for name, tt := range map[string]struct {
		open   []ledger.CapacityInterval
		epoch  int64
		report ledger.ResourceReport
		at     int64
		want   lanes.CapacityStep
	}{
		"a cap reached begins an interval": {
			nil, 1, report(2, 2), 1_000, lanes.CapacityStep{Begun: []ledger.CapacityInterval{interval(2, 1, 1_000, 1_000, 0)}},
		},
		"under the cap begins none": {
			nil, 1, report(1, 2), 1_000, lanes.CapacityStep{},
		},
		"a cap still reached stands until the report": {
			[]ledger.CapacityInterval{standing}, 1, report(2, 2), 31_000, lanes.CapacityStep{Seen: []ledger.CapacityInterval{interval(2, 1, 1_000, 31_000, 0)}},
		},
		"a count below the cap ends it at the report": {
			[]ledger.CapacityInterval{standing}, 1, report(1, 2), 31_000, lanes.CapacityStep{Ended: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000, 31_000)}},
		},
		"a changed cap ends one and begins another": {
			[]ledger.CapacityInterval{standing}, 1, report(3, 3), 31_000, lanes.CapacityStep{
				Ended: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000, 31_000)},
				Begun: []ledger.CapacityInterval{interval(3, 1, 31_000, 31_000, 0)},
			},
		},
		"a new session ends it at the last report": {
			[]ledger.CapacityInterval{standing}, 2, report(2, 2), 31_000, lanes.CapacityStep{
				Ended: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000, 16_000)},
				Begun: []ledger.CapacityInterval{interval(2, 2, 31_000, 31_000, 0)},
			},
		},
		"a report within the freshness window keeps it standing": {
			[]ledger.CapacityInterval{standing}, 1, report(2, 2), 16_000 + fresh,
			lanes.CapacityStep{Seen: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000+fresh, 0)}},
		},
		"a report after a silence ends it at the last report": {
			[]ledger.CapacityInterval{standing}, 1, report(2, 2), 16_000 + fresh + 1, lanes.CapacityStep{
				Ended: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000, 16_000)},
				Begun: []ledger.CapacityInterval{interval(2, 1, 16_000+fresh+1, 16_000+fresh+1, 0)},
			},
		},
		"a changed cap dated before the last report begins where the last interval ended": {
			[]ledger.CapacityInterval{standing}, 1, report(3, 3), 12_000, lanes.CapacityStep{
				Ended: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000, 16_000)},
				Begun: []ledger.CapacityInterval{interval(3, 1, 16_000, 16_000, 0)},
			},
		},
		"a report dated before the last one moves nothing back": {
			[]ledger.CapacityInterval{standing}, 1, report(1, 2), 12_000, lanes.CapacityStep{Ended: []ledger.CapacityInterval{interval(2, 1, 1_000, 16_000, 16_000)}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := lanes.StepCapacity(tt.open, "w1", tt.epoch, tt.report, tt.at, fresh)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("step\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// TestACapacityIntervalEndsAtItsLastReportWhileItStands is what a reader takes for an interval's end: its
// end once it ended, and its last report while it stands.
func TestACapacityIntervalEndsAtItsLastReportWhileItStands(t *testing.T) {
	if got := (ledger.CapacityInterval{StartedAt: 1, SeenAt: 5}).End(); got != 5 {
		t.Errorf("a standing interval ends at %d", got)
	}
	if got := (ledger.CapacityInterval{StartedAt: 1, SeenAt: 5, EndedAt: 7}).End(); got != 7 {
		t.Errorf("an ended interval ends at %d", got)
	}
}
