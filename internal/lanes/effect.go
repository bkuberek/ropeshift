package lanes

import (
	"fmt"
	"strings"
)

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
	// EffectPush pushes a lane's branch to its repository's remote for a pull request, which belongs to
	// no lane: the worker runs it after the lane ended (ADR-0020).
	EffectPush EffectKind = "push"
	// EffectPullRequest opens a pull request, or finds the one open.
	EffectPullRequest EffectKind = "pull_request"
	// EffectComment posts a comment on a pull request.
	EffectComment EffectKind = "comment"
)

// Forge says this effect is a forge request's. Each is safe to run again after a crash, because a push
// never forces, an open finds the pull request already open, and a comment carries a key the forge is
// read for first, so the worker that runs them runs one again rather than asking a person.
func (k EffectKind) Forge() bool {
	return k == EffectPush || k == EffectPullRequest || k == EffectComment
}

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

// The words a person's decision leaves on an effect. A run reads DetailSaidHappened, because an
// effect that completed on a person's word has nothing else to say it did.
const (
	DetailSaidHappened    = "a member said it happened"
	DetailSaidNotHappened = "a member said it did not happen"
)

// DetailNotDelivered begins the detail of an input that failed because no session held it. Nothing
// read that input, so it did not happen, and what fails any other way may have reached a session.
const DetailNotDelivered = "no session took it"

// EffectVerdict is what a person's word does to one attempt at an effect. Retry sends it back to
// started for a run to repeat, and State and Detail settle it otherwise.
type EffectVerdict struct {
	// ID is the ledger's name for the decision, which makes applying it once a fact of the journal.
	ID      string
	Key     string
	Attempt int64
	Retry   bool
	State   EffectState
	Detail  string
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

// Undelivered says this input failed for want of a session, so it can be delivered for the first time
// by a later attempt. A record that failed is settled for good, so that attempt keeps a key of its own.
func (e Effect) Undelivered() bool {
	return e.Kind == EffectInput && e.State == EffectFailed && strings.HasPrefix(e.Detail, DetailNotDelivered)
}

// NewEffect names one effect of a generation. Label separates effects of one kind.
func NewEffect(kind EffectKind, lane string, generation int64, target, label string) Effect {
	key := fmt.Sprintf("%s/%d/%s", lane, generation, kind)
	if label != "" {
		key += "/" + label
	}
	return Effect{Key: key, Kind: kind, Lane: lane, Generation: generation, Target: target}
}
