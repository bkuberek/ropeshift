package workorders

// Action is something a member does to a work order, as a view offers it. Each is a call of the API that a
// command does the same as, and the state machine says when it is allowed.
type Action string

// The actions a view can offer for a work order.
const (
	// ActionClaim claims ready work for a worker.
	ActionClaim Action = "claim"
	// ActionStartLane asks the worker that holds a claim to start its lane.
	ActionStartLane Action = "start_lane"
	// ActionRetry returns failed work to ready, adding an attempt when none is left.
	ActionRetry Action = "retry"
	// ActionRelease gives a claim back, which returns the work to ready.
	ActionRelease Action = "release"
	// ActionSendBack returns work in contract review to planning, because its contract must change.
	ActionSendBack Action = "send_back"
	// ActionReassign ends a lapsed holder's claim.
	ActionReassign Action = "reassign"
	// ActionResolve returns blocked work to ready.
	ActionResolve Action = "resolve"
	// ActionReplan returns blocked work to planning.
	ActionReplan Action = "replan"
	// ActionCancel ends work for good.
	ActionCancel Action = "cancel"
	// ActionArchiveLane asks the worker that ran a lane to remove its worktree.
	ActionArchiveLane Action = "archive_lane"
)

// Actions lists every action, in the order a view offers them.
func Actions() []Action {
	return []Action{
		ActionClaim, ActionStartLane, ActionRetry, ActionRelease, ActionSendBack, ActionReassign, ActionResolve, ActionReplan, ActionCancel,
		ActionArchiveLane,
	}
}

// Facts are what a work order's state does not say, and an action turns on.
type Facts struct {
	// Dispatched says the work's claim already asked for a lane, so a second start would be refused.
	Dispatched bool
	// Removable says a lane of the work order ended and left a worktree a member may ask to remove.
	Removable bool
}

// triggers are the edges of the machine an action takes. An action with an edge is allowed where the table
// has it, so no action stands on a rule the table does not hold. Archiving a lane is no edge, and turns on
// the lane instead.
var triggers = map[Action]Trigger{
	ActionClaim:     TriggerClaimGranted,
	ActionStartLane: TriggerLaneStarted,
	ActionRetry:     TriggerRetried,
	ActionRelease:   TriggerClaimReleased,
	ActionSendBack:  TriggerContractChange,
	ActionReassign:  TriggerTakeover,
	ActionResolve:   TriggerResolved,
	ActionReplan:    TriggerReplan,
	ActionCancel:    TriggerCanceled,
}

// Allowed lists the actions a work order in a state allows, in the order Actions gives them. It reads the
// state machine, so a view that offers what it lists offers nothing the use cases would refuse for the state.
// Whether a call goes through also turns on who calls and on the work's own record, which the use case checks.
func Allowed(state State, facts Facts) []Action {
	var out []Action
	for _, action := range Actions() {
		switch action {
		case ActionArchiveLane:
			if facts.Removable {
				out = append(out, action)
			}
			continue
		case ActionStartLane:
			if facts.Dispatched {
				continue
			}
		}
		if _, ok := Next(state, triggers[action]); ok {
			out = append(out, action)
		}
	}
	return out
}

// LaneRuns says a lane's worktree is in use: the lane has not ended, and the attempt it started under is the
// work order's current one, so nothing has stopped it. Nobody can remove the worktree of a lane that runs.
func (w WorkOrder) LaneRuns(startedFence int64, ended bool) bool {
	return !ended && w.Fence == startedFence
}
