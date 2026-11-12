package apitest

import (
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// WorkIn names the work order a world holds in a state.
func WorkIn(state workorders.State) string { return "wo-" + string(state) }

// LaneIn names the lane a world holds in a state.
func LaneIn(state lanes.ActualState) string { return "lane-" + laneWork(state) }

func laneWork(state lanes.ActualState) string { return "wl-" + string(state) }

// The values a lane reports beside its state.
var (
	EveryLiveness = []lanes.Liveness{lanes.LivenessAlive, lanes.LivenessDead, lanes.LivenessUnknown}
	EveryReach    = []lanes.Reachability{lanes.Reachable, lanes.Impaired, lanes.Unreachable, lanes.ReachUnknown}
)

// WorkWalks takes a work order to each state of the table, through the path that makes it. A state
// with no walk fails the fixture, which is what holds a new state to the views.
var WorkWalks = map[workorders.State]func(*World, string){
	workorders.Intake:         func(w *World, id string) { w.Created(id, "Waiting for intake") },
	workorders.Planning:       (*World).sentBack,
	workorders.ContractReview: func(w *World, id string) { w.Recorded(id, "Waiting for a contract review") },
	workorders.Ready:          func(w *World, id string) { w.Ready(id, "Ready for a worker") },
	workorders.Claimed:        (*World).claimed,
	workorders.Running:        func(w *World, id string) { w.Running(id, "A lane works on this") },
	workorders.Parked:         func(w *World, id string) { w.Running(id, "Parked until a person says go").Park(lanes.HumanWake()) },
	workorders.Lapsed:         func(w *World, id string) { w.Running(id, "Its worker went quiet") },
	workorders.Acceptance: func(w *World, id string) {
		w.Running(id, "Finished and waiting for a word").End(workorders.OutcomeCompleted, "")
	},
	workorders.Blocked: func(w *World, id string) {
		w.Running(id, "Stopped by a lane").End(workorders.OutcomeBlocked, "no_outcome")
	},
	workorders.Integrating: (*World).integrating,
	workorders.Done:        (*World).accepted,
	workorders.Failed:      func(w *World, id string) { w.Running(id, "Out of attempts").End(workorders.OutcomeFailed, "crashed") },
	workorders.Canceled: func(w *World, id string) {
		w.Recorded(id, "Called off")
		w.Cancel(id)
	},
}

func (w *World) sentBack(id string) {
	w.t.Helper()
	w.Recorded(id, "Sent back to planning")
	if err := control.NewReviews(w.Daemon.Store).SendBack(w.ctx, Scope, id, "The contract must change", worldMember); err != nil {
		w.t.Fatal(err)
	}
}

func (w *World) claimed(id string) {
	w.t.Helper()
	w.Ready(id, "Claimed with no lane yet")
	request := workorders.ClaimRequest{WorkOrder: id, ClaimID: "claim-" + id, Worker: WorldWorker, TTLMs: LeaseTTL}
	if _, err := control.NewLeases(w.Daemon.Store).Claim(w.ctx, Scope, request, worldMember); err != nil {
		w.t.Fatal(err)
	}
}

func (w *World) accepted(id string) {
	w.t.Helper()
	w.Running(id, "Accepted by a member").End(workorders.OutcomeCompleted, "")
	if _, err := w.Daemon.Client.Accept(w.ctx, id); err != nil {
		w.t.Fatal(err)
	}
}

// MergingRepository is a world's repository where ropeshift merges, at person_approves.
const MergingRepository = "repository-merges"

// integrating is work a member accepted in a repository where ropeshift merges, whose merge the worker that
// ran its lane has been asked for and has not reported.
func (w *World) integrating(id string) {
	w.t.Helper()
	w.Repository(MergingRepository, ledger.PersonApproves)
	work := control.LedgerWork{
		Order:     workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "Accepted and waiting to merge", MaxAttempts: 1, RepositoryID: MergingRepository},
		Statement: "Merge what was accepted.", Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}
	if _, err := control.NewWorkOrders(w.Daemon.Store).Record(w.ctx, Scope, work, worldMember); err != nil {
		w.t.Fatal(err)
	}
	if _, err := control.NewReviews(w.Daemon.Store).Pass(w.ctx, Scope, id, worldMember); err != nil {
		w.t.Fatal(err)
	}
	w.Run(id).End(workorders.OutcomeCompleted, "")
	if _, err := w.Daemon.Client.Accept(w.ctx, id); err != nil {
		w.t.Fatal(err)
	}
}

// laneIn lays out one lane in a state. A worker reports a state, except the two the ledger records for
// itself, and each lane reports another of the values that stand beside its state.
func (w *World) laneIn(state lanes.ActualState, turn int) {
	w.t.Helper()
	lane := w.Running(laneWork(state), "A lane in "+string(state))
	switch state {
	case lanes.Ended:
		lane.End(workorders.OutcomeCompleted, "")
	case lanes.Parked:
		lane.Park(lanes.HumanWake())
	default:
		reach := string(EveryReach[turn%len(EveryReach)])
		conditions := []string{string(lanes.EveryCondition[turn%len(lanes.EveryCondition)])}
		// Only a tripped lane says its day stopped it.
		if state == lanes.Tripped {
			conditions = append(conditions, string(lanes.DayCapped))
		}
		lane.Status(ledger.LaneStatus{
			ActualState: string(state), Liveness: string(EveryLiveness[turn%len(EveryLiveness)]), ModelReach: reach, LedgerReach: reach,
			Conditions: conditions,
		})
	}
}

// EveryState lays out one work order in each state of the table and one lane in each state of its own,
// with half of the lanes gone quiet, so both kinds of freshness stand beside each other. A sweep ends
// every lease past its deadline, so the one state a sweep makes comes first.
func EveryState(w *World, clock *Clock) {
	w.t.Helper()
	WorkWalks[workorders.Lapsed](w, WorkIn(workorders.Lapsed))
	clock.Advance(time.Duration(LeaseTTL+1) * time.Millisecond)
	w.Lapse()
	for _, state := range workorders.States() {
		if state == workorders.Lapsed {
			continue
		}
		WorkWalks[state](w, WorkIn(state))
		clock.Advance(time.Second)
	}
	for turn, state := range lanes.ActualStates {
		w.laneIn(state, turn)
		if turn == len(lanes.ActualStates)/2 {
			clock.Advance(time.Duration(4*HeartbeatMs) * time.Millisecond)
		}
	}
}
