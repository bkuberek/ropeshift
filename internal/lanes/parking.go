package lanes

import (
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
)

// ParkTrigger says why a lane parks.
type ParkTrigger string

// The triggers this build wires. The wake each stores comes from the design's table.
const (
	// ParkBlockingQuestion is a lane at rest that waited out `ask_wait`.
	ParkBlockingQuestion ParkTrigger = "blocking_question"
	// ParkFleetDrain is a lane a draining fleet stopped.
	ParkFleetDrain ParkTrigger = "fleet_drain"
	// ParkUnknownScreen is a tmux start no check could read.
	ParkUnknownScreen ParkTrigger = "unknown_screen"
	// ParkUsageLimit is a lane on a sign-in that reached its limit.
	ParkUsageLimit ParkTrigger = "usage_limit"
	// ParkSignIn is a lane on a sign-in that expired.
	ParkSignIn ParkTrigger = "sign_in_expired"
	// ParkPolicy is a lane a policy stopped, for a person to look at what changed under it.
	ParkPolicy ParkTrigger = "policy"
	// ParkWaitingForPerson is a planning lane at rest that waited out `ask_wait` for its person with
	// nobody attached to its pane.
	ParkWaitingForPerson ParkTrigger = "waiting_for_person"
	// ParkSpendLimit is a lane whose work order reached its hard spend limit (ADR-0037). It waits on a raise of the
	// limit, or on a person.
	ParkSpendLimit ParkTrigger = "spend_limit"
	// ParkSpendUnreadable is a lane whose work order's limit its worker could not read for longer than it will run
	// unheld. It waits on the same wake: the limit above what was spent, which holds as soon as the ledger can say, or a
	// person.
	ParkSpendUnreadable ParkTrigger = "spend_unreadable"
)

// ParkRequest asks a lane to park.
type ParkRequest struct {
	Trigger ParkTrigger
	// Expr is the wake it stores.
	Expr string
	// SkipNarrative leaves the agent's part out, because the agent cannot write it.
	SkipNarrative bool
	// PastCompaction lets a lane park from a compaction, for a hold with a bound of its own. It
	// leaves through the compaction's failure edge, and a session inside one writes nothing.
	PastCompaction bool
	// MidTurn says the lane parks while a turn runs, so its instruction goes to the turn on the path a session reads
	// between tool calls, and every tool call but the lane's own is denied until the session takes it (FR-SPEND-5).
	MidTurn bool
}

// ParkProgress is how far a lane's parking got, in the worker's journal. A stored wake decides
// what a restarted worker does: it finishes parking, whatever it would otherwise do next.
type ParkProgress struct {
	// Wake and Expr are what the ledger stores.
	Wake string `json:"wake"`
	Expr string `json:"expr"`
	// Trigger says why.
	Trigger ParkTrigger `json:"trigger"`
	// SkipNarrative says the agent is not asked for its part.
	SkipNarrative bool `json:"skip_narrative,omitempty"`
	// Generation is the one that parked.
	Generation int64 `json:"generation,omitempty"`
	// Seq is the journal position of the parked commit, so a refusal can be matched to it.
	Seq int64 `json:"seq,omitempty"`
	// Pending is what that generation was doing when it stopped.
	Pending string `json:"pending,omitempty"`
	// Repeat states the calls a decision covers, which the next session makes first. RunsItself says
	// that session is one a resume runs them in by itself, and nothing should tell it to.
	Repeat     string `json:"repeat,omitempty"`
	RunsItself bool   `json:"runs_itself,omitempty"`
	// Begun says the next generation exists, so nothing is left to finish.
	Begun bool `json:"begun,omitempty"`
	// Fetch names what a worker that never ran this lane must bring from a remote.
	Fetch []string `json:"fetch,omitempty"`
	// Usage is what the generation had spent when a hard spend limit stopped it, which a worker that takes over
	// reports as the least the generation spent. The turn in progress is in no journaled meter, and a park that
	// reported less than the figure that stopped it would read to the ledger as a limit raised (FR-SPEND-5).
	Usage *ledger.GenerationUsage `json:"usage,omitempty"`
}

// Open says a lane has parking left to finish.
func (p *ParkProgress) Open() bool { return p != nil && !p.Begun }

// Deferred says a lane parked on a call its session deferred. Its turn did not end as a turn ends:
// a native resume runs that call again by itself, and the turn goes on from it (RS11).
func (p *ParkProgress) Deferred() bool {
	return p != nil && p.Trigger == ParkApproval && p.SkipNarrative
}

// WakeID names the wake of one generation. A lane parks at most once in a generation, so a
// restart that begins parking again asks for the same wake, and the ledger stores it once.
func WakeID(lane string, generation int64) string {
	return fmt.Sprintf("%s/g%d", lane, generation)
}

// QuestionWake is what a lane waits on after a blocking question: every answer, or a person,
// because a dismissed question leaves the rest false.
func QuestionWake(questions []string) string {
	terms := make([]map[parking.Kind]string, 0, len(questions))
	for _, id := range questions {
		terms = append(terms, map[parking.Kind]string{parking.QuestionAnswered: id})
	}
	raw, err := json.Marshal(map[string]any{"any_of": []any{
		map[string]any{"all_of": terms},
		map[parking.Kind]bool{parking.HumanResume: true},
	}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// SpendLimitWake is what a lane waits on at its work order's hard spend limit: the limit rising above what the work
// order has been charged, or a person (FR-SPEND-5). The first is stored as data and the control plane reads it from the
// work order's spend, so a raise settles it in the transaction that changed the limit.
func SpendLimitWake(workOrder string) string {
	raw, err := json.Marshal(map[string]any{"any_of": []any{
		map[parking.Kind]string{parking.LimitRaised: workOrder},
		map[parking.Kind]bool{parking.HumanResume: true},
	}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// ProjectLimitWake is what a lane waits on when its project's daily or weekly limit parked it (FR-SPEND-6): the project's
// limit rising, which is the fact a raise settles, the end of the window that held the project back, which the control
// plane's tick reads as a timer, or a person. A raise of the project's limit and the day rolling over each fire the wake
// once, because a wake fires once and the other term is then moot. The lane's own work order is not in it: a lane that
// woke at the end of the day and found its work order over its limit parks again before its first turn, on that limit.
func ProjectLimitWake(untilMs int64) string {
	raw, err := json.Marshal(map[string]any{"any_of": []any{
		map[parking.Kind]string{parking.LimitRaised: parking.ProjectLimit},
		map[string]int64{"at": untilMs},
		map[parking.Kind]bool{parking.HumanResume: true},
	}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// HumanWake is what only a person can end.
func HumanWake() string {
	raw, err := json.Marshal(map[parking.Kind]bool{parking.HumanResume: true})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// ParkApproval is a lane whose request outlasted its wait, or one that deferred a call.
const ParkApproval ParkTrigger = "approval"

// ApprovalWake is what a lane waits on after a request: each one decided, or a person, because a
// member can look at a lane and let it go on without deciding.
func ApprovalWake(approvals []string) string {
	terms := make([]map[parking.Kind]string, 0, len(approvals))
	for _, id := range approvals {
		terms = append(terms, map[parking.Kind]string{parking.ApprovalDecided: id})
	}
	raw, err := json.Marshal(map[string]any{"any_of": []any{
		map[string]any{"all_of": terms},
		map[parking.Kind]bool{parking.HumanResume: true},
	}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}
