package lanes

import (
	"encoding/json"
	"fmt"

	"ropeshift.dev/ropeshift/internal/parking"
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
)

// ParkRequest asks a lane to park.
type ParkRequest struct {
	Trigger ParkTrigger
	// Expr is the wake it stores.
	Expr string
	// SkipNarrative leaves the agent's part out, because the agent cannot write it.
	SkipNarrative bool
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
	// Begun says the next generation exists, so nothing is left to finish.
	Begun bool `json:"begun,omitempty"`
	// Fetch names what a worker that never ran this lane must bring from a remote.
	Fetch []string `json:"fetch,omitempty"`
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
