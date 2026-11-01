package lanes

import "fmt"

// EffectState says what a worker knows about one external effect.
type EffectState string

// The states of an effect. A crash leaves one started, and the next start cannot know
// whether it happened, so it becomes unknown until something observes it.
const (
	EffectStarted   EffectState = "started"
	EffectCompleted EffectState = "completed"
	EffectFailed    EffectState = "failed"
	EffectUnknown   EffectState = "unknown_after_crash"
)

// EffectKind names what an effect touches outside the worker.
type EffectKind string

// The external effects of a lane.
const (
	EffectWorktree EffectKind = "worktree"
	EffectSession  EffectKind = "session"
	EffectInput    EffectKind = "input"
	EffectStop     EffectKind = "stop"
	// EffectGuard is one pass of a stop's guard, which pushes and writes refs.
	EffectGuard EffectKind = "guard"
	// EffectRemoval takes a worktree away.
	EffectRemoval EffectKind = "removal"
	// EffectHook runs a profile's hook.
	EffectHook EffectKind = "hook"
)

// Observable says a restart can look for this effect. Nothing shows whether a session
// took an input before a crash, so an input stays unknown until a person decides, and the
// same goes for a hook, because only its author knows what it does.
func (k EffectKind) Observable() bool { return k != EffectInput && k != EffectHook }

// Repeats says this effect may run again from a state it already reached. A stop is safe
// to repeat once it failed, a guard always is, because each pass reads the worktree first,
// and a removal is too, because a worktree already gone is done. A removal a restart gave
// up on must run again, or a lane would read as archived with its worktree standing.
func (k EffectKind) Repeats(state EffectState) bool {
	switch k {
	case EffectStop:
		return state == EffectFailed
	case EffectGuard, EffectRemoval:
		return true
	}
	return false
}

// Effect is one bracket in the journal. Key is unique for good, so one effect can be
// performed only once, and a retry of the same effect keeps its key.
type Effect struct {
	Key        string
	Kind       EffectKind
	Lane       string
	Generation int64
	// Target is what an observation looks at, such as a worktree's path.
	Target   string
	State    EffectState
	Attempts int64
	Detail   string
}

// NewEffect names one effect of a generation. Label separates effects of one kind.
func NewEffect(kind EffectKind, lane string, generation int64, target, label string) Effect {
	key := fmt.Sprintf("%s/%d/%s", lane, generation, kind)
	if label != "" {
		key += "/" + label
	}
	return Effect{Key: key, Kind: kind, Lane: lane, Generation: generation, Target: target}
}
