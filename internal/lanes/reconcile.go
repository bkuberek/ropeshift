package lanes

// Liveness has three values, because an incomplete read is not a negative answer.
type Liveness string

// The values of Liveness. Dead needs positive evidence.
const (
	LivenessAlive   Liveness = "alive"
	LivenessDead    Liveness = "dead"
	LivenessUnknown Liveness = "unknown"
)

// Finding is what one observation of an effect found.
type Finding string

// The findings. FoundUnknown is an incomplete read, which never counts as nothing.
const (
	Found        Finding = "found"
	FoundNothing Finding = "nothing"
	FoundUnknown Finding = "unknown"
)

// ObservedEffect is an effect a crash left unknown, and what looking for it found.
type ObservedEffect struct {
	Effect  Effect
	Finding Finding
}

// Session is what a driver found of a lane's session from an earlier process.
type Session struct {
	// Found says a session of this generation exists or existed.
	Found    bool
	Liveness Liveness
	// Readable says its stream can be read from where this worker stopped, which holds
	// for a session that ended while the worker was away.
	Readable bool
}

// Actual is one lane as a pass found it.
type Actual struct {
	Record Record
	// Held says a run in this process owns the lane, so a pass leaves it alone.
	Held bool
	// Session is what an observation found. A held or ended lane needs none.
	Session Session
	// Effects are this lane's effects, as the journal holds them.
	Effects []Effect
	// ParkRefused says the ledger fenced this lane's parked commit, so no wake can reach it.
	ParkRefused bool
}

// World is everything one reconcile pass knows.
type World struct {
	// Wanted names the lanes desired state runs here that have no record.
	Wanted []string
	Lanes  []Actual
	// Unknown holds the effects a crash left unknown, each with what was found.
	Unknown []ObservedEffect
	// Allocations names the lanes holding resources.
	Allocations []string
	// Archives are the lanes a member asked to take away.
	Archives []string
}

// ActionKind names what a pass does.
type ActionKind string

// The actions of a pass.
const (
	// ActionStart runs a lane desired state names.
	ActionStart ActionKind = "start"
	// ActionConfirm records that an unknown effect happened.
	ActionConfirm ActionKind = "confirm"
	// ActionRetry clears an unknown effect that did not happen, for a run to repeat.
	ActionRetry ActionKind = "retry"
	// ActionAbandon records that an unknown effect of an ended lane did not happen.
	ActionAbandon ActionKind = "abandon"
	// ActionContinue runs a lane from where a crash stopped it, before its session.
	ActionContinue ActionKind = "continue"
	// ActionAdopt reads a session this process did not start.
	ActionAdopt ActionKind = "adopt"
	// ActionHandoff finishes a handoff whose session went with the worker that followed it.
	ActionHandoff ActionKind = "handoff"
	// ActionPark finishes parking whose session went with the worker that followed it.
	ActionPark ActionKind = "park"
	// ActionEnd accounts for a lane whose session is gone.
	ActionEnd ActionKind = "end"
	// ActionRelease gives back what an ended lane held.
	ActionRelease ActionKind = "release"
	// ActionArchive takes away a worktree a member asked to remove.
	ActionArchive ActionKind = "archive"
)

// Action is one step of a pass.
type Action struct {
	Kind ActionKind
	Lane string
	// Effect is the key of the effect it settles.
	Effect string
}

// Plan computes a pass from what it observed. It is pure, so running it again on a
// world nothing changed gives the same answer, and it never acts on `unknown`: an
// incomplete read waits for a complete one, because spawning is not idempotent. Each
// lane is decided alone, so one stuck lane stops no other.
func Plan(world World) []Action {
	var actions []Action
	for _, lane := range world.Wanted {
		actions = append(actions, Action{Kind: ActionStart, Lane: lane})
	}
	ended := map[string]bool{}
	for _, lane := range world.Lanes {
		ended[lane.Record.ID] = lane.Record.State == StateEnded
	}
	held := map[string]bool{}
	for _, observed := range world.Unknown {
		if !observed.Effect.Kind.Observable() {
			continue
		}
		lane := observed.Effect.Lane
		switch observed.Finding {
		case Found:
			actions = append(actions, Action{Kind: ActionConfirm, Lane: lane, Effect: observed.Effect.Key})
		case FoundNothing:
			kind := ActionRetry
			if ended[lane] {
				kind = ActionAbandon
			}
			actions = append(actions, Action{Kind: kind, Lane: lane, Effect: observed.Effect.Key})
		default:
			held[lane] = true
		}
	}
	for _, lane := range world.Lanes {
		if !lane.Held && lane.ParkRefused && lane.Record.State == StateParked {
			actions = append(actions, Action{Kind: ActionEnd, Lane: lane.Record.ID})
			continue
		}
		if lane.Held || lane.Record.State != StateRunning || held[lane.Record.ID] {
			continue
		}
		if action, ok := planLane(lane); ok {
			actions = append(actions, action)
		}
	}
	return append(append(actions, releases(world)...), archives(world)...)
}

// releases finds resources no live lane stands behind. They live in the journal, so a
// crash at any point after an allocation leaves them here for the next pass.
func releases(world World) []Action {
	live := map[string]bool{}
	for _, lane := range world.Lanes {
		live[lane.Record.ID] = lane.Held || lane.Record.State == StateRunning
	}
	var actions []Action
	for _, lane := range world.Allocations {
		if !live[lane] {
			actions = append(actions, Action{Kind: ActionRelease, Lane: lane})
		}
	}
	return actions
}

// archives keeps to ended lanes: a running lane's worktree is in use, and an archived one
// is already gone.
func archives(world World) []Action {
	ended := map[string]bool{}
	for _, lane := range world.Lanes {
		ended[lane.Record.ID] = !lane.Held && lane.Record.State == StateEnded
	}
	var actions []Action
	for _, lane := range world.Archives {
		if ended[lane] {
			actions = append(actions, Action{Kind: ActionArchive, Lane: lane})
		}
	}
	return actions
}

// planLane decides one lane no run owns. Parking comes first, because a stored wake decides
// what a restarted worker does. A handoff comes before a lane's end, because a session lost
// during one is its failure edge, and that edge leads to a next generation.
func planLane(lane Actual) (Action, bool) {
	id := lane.Record.ID
	gone := !lane.Session.Found || lane.Session.Liveness == LivenessDead
	switch {
	case lane.Session.Readable:
		return Action{Kind: ActionAdopt, Lane: id}, true
	case gone && lane.Record.Parking.Open():
		return Action{Kind: ActionPark, Lane: id}, true
	case gone && lane.Record.Handoff.Open():
		return Action{Kind: ActionHandoff, Lane: id}, true
	case lane.Session.Found && lane.Session.Liveness == LivenessDead:
		return Action{Kind: ActionEnd, Lane: id}, true
	case lane.Session.Found:
		return Action{}, false
	case spawned(lane.Effects, lane.Record.Generation):
		return Action{Kind: ActionEnd, Lane: id}, true
	}
	return Action{Kind: ActionContinue, Lane: id}, true
}

// spawned says this generation's session effect finished, so a session is gone and not
// unstarted. An earlier generation's says nothing about this one.
func spawned(effects []Effect, generation int64) bool {
	for _, effect := range effects {
		if effect.Kind != EffectSession || effect.Generation != generation {
			continue
		}
		return effect.State == EffectCompleted || effect.State == EffectFailed
	}
	return false
}
