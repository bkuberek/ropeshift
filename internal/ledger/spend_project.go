package ledger

import (
	"encoding/json"
	"fmt"
	"time"
)

// SubjectProjectSpend is what the events about a project's spend limits are about, kept apart from a work order's
// so the replay of neither reads the other's.
const SubjectProjectSpend = "project_spend"

// SpendScopeWork is the tally of what the lanes of a project's work orders were charged, which is what a project's
// daily and weekly limits count (ADR-0037). The assistant has a soft daily limit of its own and is tallied apart,
// because stopping it stops the question ladder.
const SpendScopeWork = "work"

// SpendScopeAssistant is the tally of what the project's assistant was charged, which its soft daily warning reads.
const SpendScopeAssistant = "assistant"

// EventSpendProjectLimitRaised says a member raised a project's daily or weekly limit for the window it was made in.
// It records the limit before and after (FR-SPEND-8), as a work order's raise does.
const EventSpendProjectLimitRaised = "spend.project_limit_raised"

// ProjectLimits are the project keys a daily or weekly limit reads: the two ceilings, with 0 for none, and the share
// of each where a soft notice sits.
type ProjectLimits struct {
	Daily  float64
	Weekly float64
	Warn   float64
}

// ProjectWindowSpend is what a project was charged in one window beside the limits of that window, as a member reads
// it. A limit of 0 is no limit. Spend is a project's own and no lane, orchestrator, or brief receives it (FR-SPEND-9).
type ProjectWindowSpend struct {
	Window string `json:"window"`
	// SpentUSD is what the project's lanes were charged since the window began.
	SpentUSD  float64 `json:"spent_usd"`
	SoftLimit float64 `json:"soft_limit"`
	// HardLimit is the limit in force: the policy's, or the one a member raised the window to when that is larger.
	// RaisedTo says a member raised it, and is 0 for a limit nobody raised.
	HardLimit float64 `json:"hard_limit"`
	RaisedTo  float64 `json:"raised_to,omitempty"`
	Remaining float64 `json:"remaining"`
	// StartsAt and EndsAt bound the window in milliseconds on the ledger's clock. Work held back by the limit may go on
	// at EndsAt.
	StartsAt int64 `json:"starts_at"`
	EndsAt   int64 `json:"ends_at"`
}

// Past says the window's spend reached its hard limit, which holds the project's work back until the window ends or a
// member raises the limit.
func (w ProjectWindowSpend) Past() bool { return w.HardLimit > 0 && w.SpentUSD >= w.HardLimit }

// PastSoft says the window's spend reached its soft limit, which warns and stops nothing.
func (w ProjectWindowSpend) PastSoft() bool { return w.SoftLimit > 0 && w.SpentUSD >= w.SoftLimit }

// Window reads one window: what was spent in the one the moment falls in, the policy's limit, and the highest limit a
// member raised it to. A raise lifts a limit that is set and never lowers one, so a window with no limit has none to
// raise.
func (l ProjectLimits) Window(name string, now int64, spent, limit, raised float64) ProjectWindowSpend {
	w := ProjectWindowSpend{Window: name, SpentUSD: spent, StartsAt: WindowStart(name, now), EndsAt: WindowEnd(name, now)}
	if limit <= 0 {
		return w
	}
	w.HardLimit = limit
	if raised > limit {
		w.HardLimit, w.RaisedTo = raised, raised
	}
	w.SoftLimit = l.Warn * w.HardLimit
	w.Remaining = max(w.HardLimit-spent, 0)
	return w
}

// ProjectSpend is where a project's spend stands in the day and the week the moment falls in.
type ProjectSpend struct {
	Day  ProjectWindowSpend `json:"day"`
	Week ProjectWindowSpend `json:"week"`
}

// Spend reads both windows from what each was charged and the limits a member raised them to.
func (l ProjectLimits) Spend(now int64, day, week, raisedDay, raisedWeek float64) ProjectSpend {
	return ProjectSpend{
		Day:  l.Window(WindowDay, now, day, l.Daily, raisedDay),
		Week: l.Window(WindowWeek, now, week, l.Weekly, raisedWeek),
	}
}

// Limited says the project has a daily or weekly limit, so a lane of it is one a limit may stop and its worker keeps
// the ledger current with what the lane spends.
func (s ProjectSpend) Limited() bool { return s.Day.HardLimit > 0 || s.Week.HardLimit > 0 }

// ProjectHold says a project is past a daily or weekly limit: no work order of it may be claimed or have a lane
// started, and its running lanes park, until the window ends or a member raises the limit (FR-SPEND-6).
type ProjectHold struct {
	// Window is the window that holds the project the longest.
	Window string `json:"window"`
	// UntilMs is when that window ends, on the ledger's clock.
	UntilMs int64 `json:"until_ms"`
}

// Hold is nil for a project under both limits. A project past both is held until the later window ends, and a lane
// that wakes at the earlier is held again by the later at its next start.
func (s ProjectSpend) Hold() *ProjectHold {
	var hold *ProjectHold
	for _, w := range []ProjectWindowSpend{s.Day, s.Week} {
		if w.Past() && (hold == nil || w.EndsAt > hold.UntilMs) {
			hold = &ProjectHold{Window: w.Window, UntilMs: w.EndsAt}
		}
	}
	return hold
}

// ProjectLimitError says an act that starts work was asked of a project past a daily or weekly limit, and what the
// member can do. It is a refusal, and it changes nothing.
type ProjectLimitError struct {
	Window   string
	HardUSD  float64
	SpentUSD float64
	UntilMs  int64
}

func (e *ProjectLimitError) Error() string {
	name := "daily"
	if e.Window == WindowWeek {
		name = "weekly"
	}
	return fmt.Sprintf("the project is at its %s spend limit of $%.2f, with $%.2f charged: work goes on when the %s ends at %s, or raise the limit with ropeshift spend raise --window %s",
		name, e.HardUSD, e.SpentUSD, e.Window, time.UnixMilli(e.UntilMs).UTC().Format(time.RFC3339), e.Window)
}

// ProjectLimitErrorOf is the refusal for the window that holds a project, from what it was charged in it.
func ProjectLimitErrorOf(spend ProjectSpend, hold ProjectHold) *ProjectLimitError {
	window := spend.Day
	if hold.Window == WindowWeek {
		window = spend.Week
	}
	return &ProjectLimitError{Window: hold.Window, HardUSD: window.HardLimit, SpentUSD: window.SpentUSD, UntilMs: hold.UntilMs}
}

// ProjectLimitChange is the data of EventSpendProjectLimitRaised: which window, the window it was made in, the limit
// before and after, and what the project had been charged in it. A raise lifts the window it was made in only, so the
// next day or week is back to the policy's limit.
type ProjectLimitChange struct {
	Window      string  `json:"window"`
	WindowStart int64   `json:"window_start"`
	BeforeUSD   float64 `json:"before_usd"`
	AfterUSD    float64 `json:"after_usd"`
	SpentUSD    float64 `json:"spent_usd"`
	// ByPercent and ToUSD are how the raise was asked for.
	ByPercent     float64 `json:"by_percent,omitempty"`
	ToUSD         float64 `json:"to_usd,omitempty"`
	PolicyVersion int64   `json:"policy_version"`
}

// RaisedProjectLimit is the highest limit a member raised a window to in the window the moment falls in, and 0 when
// none did. A raise made in an earlier day or week is not read, which is how it ends with its window.
func RaisedProjectLimit(events []Event, window string, now int64) float64 {
	start := WindowStart(window, now)
	var raised float64
	for _, event := range events {
		if event.Type != EventSpendProjectLimitRaised {
			continue
		}
		var change ProjectLimitChange
		if err := json.Unmarshal(event.Data, &change); err == nil && change.Window == window && change.WindowStart == start {
			raised = max(raised, change.AfterUSD)
		}
	}
	return raised
}
