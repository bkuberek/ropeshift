package measures

import (
	"math"
	"slices"
)

// The kinds of interruption (observability.md): a delivered batch of questions, an approval a person
// decided, and an action only a person could take.
const (
	InterruptionBatch    = "batch"
	InterruptionApproval = "approval"
	InterruptionForced   = "forced"
)

// Occasion is one time ropeshift required a person's attention.
type Occasion struct {
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	// Subject names what it was about, so one occasion recorded twice counts once.
	Subject string `json:"subject"`
}

// WorkOrderFacts is what the measures read of one work order.
type WorkOrderFacts struct {
	ID        string
	Kind      string
	State     string
	CreatedAt int64
	// ReadyAt is the first time it reached `ready`, and DoneAt when it reached `done`, or zero.
	ReadyAt  int64
	DoneAt   int64
	ReworkOf string
}

// QuestionFacts is what the measures read of one question.
type QuestionFacts struct {
	ID      string
	AskedAt int64
	// ClosedAt is when it was answered or dismissed, ClosedRung the rung it stood on, and ClosedByMember
	// says a person closed it, which is no autonomous decision whatever the rung.
	ClosedAt       int64
	ClosedRung     int64
	ClosedByMember bool
	// ReachedHumanAt is when it first reached a person, and Repeat says it was later found to have been
	// asked or decided before.
	ReachedHumanAt int64
	Repeat         bool
}

// DecisionFacts is what the measures read of one decision.
type DecisionFacts struct {
	ID         string
	RecordedAt int64
	// Autonomous says an orchestrator or a lane made it, or it is an assumption, which is what the
	// overturn rate holds to account.
	Autonomous bool
	Overturned bool
}

// Park is one parked period, from the lane beginning to park to its wake firing.
type Park struct {
	Trigger  string
	ParkedAt int64
	WokenAt  int64
}

// LaneCost is what one lane's agent reported, summed over its generations. LowerBound says some of
// it was never reported, because a turn a kill stopped reports nothing.
type LaneCost struct {
	WorkOrderKind string
	EndedAt       int64
	CostUSD       float64
	LowerBound    bool
}

// History is everything the measures read, taken from the ledger and nowhere else (FR-OBS-6).
type History struct {
	Interruptions []Occasion
	WorkOrders    []WorkOrderFacts
	Questions     []QuestionFacts
	Decisions     []DecisionFacts
	Parks         []Park
	Lanes         []LaneCost
	// Attachments counts lanes attached to a question another asked, by when: each is a question
	// the ladder's second rung kept from being asked again.
	Attachments []int64
	// Repositories and Calibrations are what review agreement reads: each repository's level in force, and
	// each member decision on work a review judged (FR-CAL-2).
	Repositories []RepositoryFacts
	Calibrations []CalibrationFacts
}

// Window is the span of time a report covers, from From up to but not including To.
type Window struct {
	Name string `json:"name"`
	From int64  `json:"from"`
	To   int64  `json:"to"`
}

func (w Window) holds(at int64) bool { return at != 0 && at >= w.From && at < w.To }

// Ratio is a count over a count. Value is absent when nothing was counted under it, because a rate
// of nothing is not zero.
type Ratio struct {
	Numerator   int64    `json:"numerator"`
	Denominator int64    `json:"denominator"`
	Value       *float64 `json:"value"`
}

func ratio(numerator, denominator int64) Ratio {
	r := Ratio{Numerator: numerator, Denominator: denominator}
	if denominator > 0 {
		v := float64(numerator) / float64(denominator)
		r.Value = &v
	}
	return r
}

// Durations is the median and 95th percentile of a set of durations in milliseconds.
type Durations struct {
	Count    int64  `json:"count"`
	MedianMs *int64 `json:"median_ms"`
	P95Ms    *int64 `json:"p95_ms"`
}

// percentile reads a sorted list by the nearest rank.
func percentile(sorted []int64, p float64) int64 {
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(rank, len(sorted)-1))]
}

func durations(values []int64) Durations {
	out := Durations{Count: int64(len(values))}
	if len(values) == 0 {
		return out
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	median, p95 := percentile(sorted, 0.5), percentile(sorted, 0.95)
	out.MedianMs, out.P95Ms = &median, &p95
	return out
}

// Cost is what lanes of one kind of work cost, against the work of that kind that reached `done`.
type Cost struct {
	TotalUSD float64  `json:"total_usd"`
	Done     int64    `json:"done"`
	PerDone  *float64 `json:"per_done_usd"`
	// LowerBound says some lane of this kind spent what it never reported.
	LowerBound bool `json:"lower_bound"`
}

// Report is every product measure for one window (FR-OBS-6). The north star is read beside the
// overturn rate and rework, which is how a report shows it (FR-OBS-7).
type Report struct {
	Window Window `json:"window"`
	// Interruptions per completed work order is the north star.
	Interruptions             int64            `json:"interruptions"`
	InterruptionsByKind       map[string]int64 `json:"interruptions_by_kind"`
	Completed                 int64            `json:"completed"`
	InterruptionsPerCompleted Ratio            `json:"interruptions_per_completed"`
	OverturnRate              Ratio            `json:"overturn_rate"`
	// Rework is rework work orders created over work orders completed.
	Rework                 Ratio                `json:"rework"`
	AutonomousDecisionRate Ratio                `json:"autonomous_decision_rate"`
	RepeatedQuestionRate   Ratio                `json:"repeated_question_rate"`
	PreventedRepeats       int64                `json:"prevented_repeats"`
	ParkTime               map[string]Durations `json:"park_time"`
	ReadyToDone            Durations            `json:"ready_to_done"`
	IntakeToDone           Durations            `json:"intake_to_done"`
	WorkInProgress         int64                `json:"work_in_progress"`
	CostPerOutcome         map[string]Cost      `json:"cost_per_outcome"`
	// ReviewAgreement reads over the last 30 days and over all time as of the window's end, whatever span the
	// window covers, because agreement is evidence that grows and a week of it says little.
	ReviewAgreement ReviewAgreement `json:"review_agreement"`
}

// inProgress are the states that count as work in progress.
var inProgress = []string{"claimed", "running", "parked"}

// Compute reads every measure of one window from a history. Work in progress is a count of now,
// and every other measure counts what happened inside the window.
func Compute(h History, w Window) Report {
	r := Report{
		Window: w, InterruptionsByKind: map[string]int64{}, ParkTime: map[string]Durations{}, CostPerOutcome: map[string]Cost{},
	}
	seen := map[[2]string]bool{}
	for _, o := range h.Interruptions {
		key := [2]string{o.Kind, o.Subject}
		if !w.holds(o.At) || (o.Subject != "" && seen[key]) {
			continue
		}
		seen[key] = true
		r.Interruptions++
		r.InterruptionsByKind[o.Kind]++
	}
	var readyToDone, intakeToDone []int64
	done := map[string]int64{}
	var reworked int64
	for _, order := range h.WorkOrders {
		if slices.Contains(inProgress, order.State) {
			r.WorkInProgress++
		}
		if order.ReworkOf != "" && w.holds(order.CreatedAt) {
			reworked++
		}
		if !w.holds(order.DoneAt) {
			continue
		}
		r.Completed++
		done[order.Kind]++
		intakeToDone = append(intakeToDone, order.DoneAt-order.CreatedAt)
		if order.ReadyAt != 0 {
			readyToDone = append(readyToDone, order.DoneAt-order.ReadyAt)
		}
	}
	r.InterruptionsPerCompleted = ratio(r.Interruptions, r.Completed)
	r.Rework = ratio(reworked, r.Completed)
	r.ReadyToDone, r.IntakeToDone = durations(readyToDone), durations(intakeToDone)

	var closed, autonomous, reached, repeated int64
	for _, q := range h.Questions {
		if w.holds(q.ClosedAt) {
			closed++
			if q.ClosedRung >= 1 && q.ClosedRung <= 4 && !q.ClosedByMember {
				autonomous++
			}
		}
		if w.holds(q.ReachedHumanAt) {
			reached++
			if q.Repeat {
				repeated++
			}
		}
	}
	r.AutonomousDecisionRate = ratio(autonomous, closed)
	r.RepeatedQuestionRate = ratio(repeated, reached)

	var made, overturned int64
	for _, d := range h.Decisions {
		if d.Autonomous && w.holds(d.RecordedAt) {
			made++
			if d.Overturned {
				overturned++
			}
		}
	}
	r.OverturnRate = ratio(overturned, made)

	for _, at := range h.Attachments {
		if w.holds(at) {
			r.PreventedRepeats++
		}
	}

	parks := map[string][]int64{}
	for _, p := range h.Parks {
		if w.holds(p.WokenAt) && p.ParkedAt != 0 {
			parks[p.Trigger] = append(parks[p.Trigger], p.WokenAt-p.ParkedAt)
		}
	}
	for trigger, values := range parks {
		r.ParkTime[trigger] = durations(values)
	}

	for _, lane := range h.Lanes {
		if !w.holds(lane.EndedAt) {
			continue
		}
		cost := r.CostPerOutcome[lane.WorkOrderKind]
		cost.TotalUSD += lane.CostUSD
		cost.LowerBound = cost.LowerBound || lane.LowerBound
		r.CostPerOutcome[lane.WorkOrderKind] = cost
	}
	for kind, n := range done {
		cost := r.CostPerOutcome[kind]
		cost.Done = n
		r.CostPerOutcome[kind] = cost
	}
	r.ReviewAgreement = ReviewAgreementAt(h.Repositories, h.Calibrations, w.To)
	for kind, cost := range r.CostPerOutcome {
		if cost.Done > 0 {
			v := cost.TotalUSD / float64(cost.Done)
			cost.PerDone = &v
		}
		r.CostPerOutcome[kind] = cost
	}
	return r
}
