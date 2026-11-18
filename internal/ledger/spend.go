package ledger

import (
	"encoding/json"
	"fmt"
)

// GenerationUsage is what one generation spent, as its worker last reported it (ADR-0037): the cost its turns
// reported, which is a sum of differences between the session's running totals, the turns that reported a cost,
// and the turns that did not, because a stopped turn reports nothing. A resumed session carries its running total
// on and a /clear or a handoff starts a new generation, so each generation's figures are its own.
type GenerationUsage struct {
	CostUSD      float64 `json:"cost_usd"`
	KnownTurns   int64   `json:"known_turns"`
	UnknownTurns int64   `json:"unknown_turns"`
}

// Charged is the spend a limit counts: the cost the generations recorded, plus each turn whose cost is unknown
// charged at the mean known turn cost of its generation, or of its lane when the generation has none. A limit that
// counted only the recorded cost would let a lane that is stopped again and again spend without bound.
type Charged struct {
	// RecordedUSD is the sum of the cost the turns reported.
	RecordedUSD float64 `json:"recorded_usd"`
	// EstimatedUSD is what the turns of unknown cost were charged at the mean, and EstimatedTurns how many there
	// were, including those that had no known turn to take a mean from.
	EstimatedUSD   float64 `json:"estimated_usd"`
	EstimatedTurns int64   `json:"estimated_turns"`
	// Unpriced counts the turns of unknown cost in a lane that never reported a known turn, which are charged
	// nothing because there is no mean to charge them at.
	Unpriced int64 `json:"unpriced,omitempty"`
	// ChargedUSD is the recorded cost and the estimated cost together.
	ChargedUSD float64 `json:"charged_usd"`
}

// Charge folds the generations of every lane that served a work order, across attempts, takeovers, and resumes.
// Each lane is its generations in order.
func Charge(lanes [][]GenerationUsage) Charged {
	var out Charged
	for _, generations := range lanes {
		var cost float64
		var known int64
		for _, g := range generations {
			cost += g.CostUSD
			known += g.KnownTurns
		}
		for _, g := range generations {
			out.RecordedUSD += g.CostUSD
			out.EstimatedTurns += g.UnknownTurns
			switch {
			case g.UnknownTurns == 0:
			case g.KnownTurns > 0:
				out.EstimatedUSD += float64(g.UnknownTurns) * g.CostUSD / float64(g.KnownTurns)
			case known > 0:
				out.EstimatedUSD += float64(g.UnknownTurns) * cost / float64(known)
			default:
				out.Unpriced += g.UnknownTurns
			}
		}
	}
	out.ChargedUSD = out.RecordedUSD + out.EstimatedUSD
	return out
}

// SubjectSpend is what the events about a work order's spend are about, kept apart from the work order's own so its
// replay never reads them.
const SubjectSpend = "spend"

// The events about spend. EventSpendSoftLimit says a work order's charged spend reached its soft limit, once per
// limit, and names the scope it is about.
const (
	EventSpendSoftLimit = "spend.soft_limit"
	// EventSpendLimited says a hard limit stopped a work order, which marks its delivery record `spend_limited`.
	EventSpendLimited = "spend.limited"
	// EventSpendLimitRaised says a member raised a work order's hard limit. EventSpendResumed says a member resumed
	// a lane the limit parked, which grants one more generation's allowance and so raises the limit too. Each
	// records the limit before and after (FR-SPEND-8).
	EventSpendLimitRaised = "spend.limit_raised"
	EventSpendResumed     = "spend.resumed"
)

// The acts that change a work order's hard limit.
const (
	SpendActRaise  = "raise"
	SpendActResume = "resume"
)

// SpendLimitChange is the data of EventSpendLimitRaised and EventSpendResumed: the work order, which act, the
// hard limit before and after it, and what the work order had been charged then. The limit in force is the larger
// of the one derived from its band and the latest After, so a raise never lowers a limit and a later change of
// policy cannot take it back.
type SpendLimitChange struct {
	WorkOrder string `json:"work_order"`
	Act       string `json:"act"`
	// BeforeUSD and AfterUSD are the hard limit in force before the act and the one it set.
	BeforeUSD float64 `json:"before_usd"`
	AfterUSD  float64 `json:"after_usd"`
	// ChargedUSD is what the work order had been charged when the act was taken.
	ChargedUSD float64 `json:"charged_usd"`
	// ByPercent and ToUSD are how a raise was asked for, and a resume asks for neither.
	ByPercent     float64 `json:"by_percent,omitempty"`
	ToUSD         float64 `json:"to_usd,omitempty"`
	PolicyVersion int64   `json:"policy_version"`
	// AllowanceUSD is what a resume granted: the one more generation the limit was lifted by, so the member sees what
	// "once" bought. AllowanceBasis says where that figure came from, and a raise sets neither.
	AllowanceUSD   float64 `json:"allowance_usd,omitempty"`
	AllowanceBasis string  `json:"allowance_basis,omitempty"`
}

// Where a resume's allowance comes from, in the order it is tried (FR-SPEND-7).
const (
	// AllowanceMeanGeneration is the mean charge of the work order's generations that recorded spend.
	AllowanceMeanGeneration = "mean_generation"
	// AllowanceEstimate is the work order's estimate, its p50 cost, for a work order with no generation that
	// recorded spend. No estimate divides a work order into generations, so the whole p50 stands for one: a generation
	// is part of the work and never costs more than the estimate expects of all of it.
	AllowanceEstimate = "estimate"
)

// SpendLimitError says an act that starts more spending was asked of work at its hard limit, and what the member can do.
// It is a refusal, and it changes nothing.
type SpendLimitError struct {
	WorkOrder  string
	HardUSD    float64
	ChargedUSD float64
}

func (e *SpendLimitError) Error() string {
	return fmt.Sprintf("work order %s is at its hard spend limit of $%.2f, with $%.2f charged: raise it with ropeshift spend raise",
		e.WorkOrder, e.HardUSD, e.ChargedUSD)
}

// SpendAllowance is what a worker holds a lane's work order to, and it is the worker's own read: the work order's hard
// limit, what its other lanes have been charged, and the share of the limit a headless generation's budget flag has
// beyond what remains. No lane, brief, orchestrator, or pack receives one (FR-SPEND-9). A lane's own spend is not in
// it, because the worker holds that itself and it is current in the worker as no read can be.
type SpendAllowance struct {
	// HardUSD is the hard limit in force.
	HardUSD float64 `json:"hard_usd"`
	// OthersUSD is what the work order's other lanes are charged, which are the lanes before this one.
	OthersUSD float64 `json:"others_usd,omitempty"`
	// HeadroomShare is `budget_flag_headroom_share`.
	HeadroomShare float64 `json:"headroom_share,omitempty"`
}

// Crossed says a lane that has been charged laneUSD has brought its work order to its hard limit.
func (a SpendAllowance) Crossed(laneUSD float64) bool {
	return a.HardUSD > 0 && a.OthersUSD+laneUSD >= a.HardUSD
}

// BudgetUSD is the budget a headless generation starts with for its client's own cap: what remains of the hard limit
// after the lane's spend, and the headroom's share of the limit beyond it. The client counts only its own estimate and
// only its own process, so this is a backstop for a worker that is slow to stop it, and never the limit (ADR-0037). It
// is 0 when the work order has no limit.
func (a SpendAllowance) BudgetUSD(laneUSD float64) float64 {
	if a.HardUSD <= 0 {
		return 0
	}
	return max(a.HardUSD-a.OthersUSD-laneUSD, 0) + a.HeadroomShare*a.HardUSD
}

// RaisedLimit is the hard limit a member's acts set on a work order, read from the events about its spend, and 0
// when none did.
func RaisedLimit(events []Event) float64 {
	var raised float64
	for _, event := range events {
		if event.Type != EventSpendLimitRaised && event.Type != EventSpendResumed {
			continue
		}
		var change SpendLimitChange
		if err := json.Unmarshal(event.Data, &change); err == nil {
			raised = max(raised, change.AfterUSD)
		}
	}
	return raised
}

// The conditions a work order's spend can carry.
const (
	// ConditionOverEstimate says charged spend reached the soft limit. It warns and stops nothing.
	ConditionOverEstimate = "over_estimate"
	// ConditionSpendLimit says charged spend reached the hard limit.
	ConditionSpendLimit = "spend_limit"
)

// WorkOrderSpend is one work order's charged spend beside its limits, as a member reads it. A lane, an
// orchestrator, and a brief never receive one (FR-SPEND-9). A limit of 0 is no limit.
type WorkOrderSpend struct {
	WorkOrder string  `json:"work_order"`
	Kind      string  `json:"kind"`
	Charged   Charged `json:"charged"`
	// Label says what the figure is: an estimate at list price, with how many turns were estimated by average.
	Label     string  `json:"label"`
	SoftLimit float64 `json:"soft_limit"`
	// HardLimit is the limit in force: the one the band derives, or the one a member raised it to when that is
	// larger. RaisedTo says a member raised it, and to what, and is 0 for a limit nobody raised.
	HardLimit float64 `json:"hard_limit"`
	RaisedTo  float64 `json:"raised_to,omitempty"`
	// Basis says where the limits came from: the baselined band, the current band, the size's limit with no
	// history, or a share of the reviewed work order's.
	Basis string `json:"basis,omitempty"`
	// Remaining is the hard limit less charged spend, and never below zero. It is zero with no hard limit.
	Remaining  float64  `json:"remaining"`
	Conditions []string `json:"conditions,omitempty"`
	// PolicyVersion is the version of the project's spend policy the limits were derived under.
	PolicyVersion int64 `json:"policy_version"`
}

// Label is how every surface that shows charged spend words it: an estimate at list price, and how many turns
// were estimated by average (FR-SPEND-3).
func (c Charged) Label() string {
	turns := "turns"
	if c.EstimatedTurns == 1 {
		turns = "turn"
	}
	return fmt.Sprintf("estimated, at list price, with %d %s estimated by average", c.EstimatedTurns, turns)
}
