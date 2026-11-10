package cli

import "time"

// FollowTiming makes `lane attach` poll and give up quickly, and returns how to put the real timing back.
func FollowTiming(poll, wait time.Duration) (restore func()) {
	previousPoll, previousWait := followPoll, followWait
	followPoll, followWait = poll, wait
	return func() { followPoll, followWait = previousPoll, previousWait }
}

// PaneTiming makes `plan start` poll and give up quickly for its lane's pane, and returns how to put
// the real timing back.
func PaneTiming(poll, wait time.Duration) (restore func()) {
	previousPoll, previousWait := panePoll, paneWait
	panePoll, paneWait = poll, wait
	return func() { panePoll, paneWait = previousPoll, previousWait }
}

// WorkOrderText and LaneDetail are what `work show` and `lane show` print.
var (
	WorkOrderText = workOrderText
	LaneDetail    = laneDetail
)
