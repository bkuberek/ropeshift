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
