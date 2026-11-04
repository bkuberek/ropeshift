package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/measures"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The read models, one for each view of the design. Every surface reads these, so no surface decides
// what needs a person or what order it comes in (FR-UI-4).
const (
	ViewNeedsYou   = "needs-you"
	ViewRoadmap    = "roadmap"
	ViewPlans      = "plans"
	ViewDocuments  = "documents"
	ViewDecisions  = "decisions"
	ViewQuestions  = "questions"
	ViewInProgress = "in-progress"
	ViewOnHold     = "on-hold"
	ViewStuck      = "stuck"
	ViewCompleted  = "completed"
	ViewShopFloor  = "shop-floor"
	ViewOperations = "operations"
)

// Views lists every read model, in the design's order.
var Views = []string{
	ViewNeedsYou, ViewRoadmap, ViewPlans, ViewDocuments, ViewDecisions, ViewQuestions, ViewInProgress,
	ViewOnHold, ViewStuck, ViewCompleted, ViewShopFloor, ViewOperations,
}

// ViewPath is where a read model is served.
func ViewPath(name string) string { return "/v1/views/" + name }

// Stamp says when a read model was true, and which revision it read. A surface that heard of a later
// revision on the stream reads again.
type Stamp struct {
	AsOf     int64 `json:"as_of"`
	Revision int64 `json:"revision"`
}

// The kinds of thing that wait for a person.
const (
	NeedQuestions = "questions"
	NeedApproval  = "approval"
	NeedEffect    = "effect"
	NeedWork      = "work"
)

// Why work waits for a person. The first five are the design's stuck list.
const (
	ReasonLapsed     = "lapsed"
	ReasonBlocked    = "blocked"
	ReasonTripped    = "tripped"
	ReasonStalled    = "stalled"
	ReasonLongParked = "long_parked"
	// ReasonAcceptance is finished work that waits for a member's word.
	ReasonAcceptance = "acceptance"
	// ReasonResume is a parked lane that waits on a person and on nothing else.
	ReasonResume = "human_resume"
)

// What a member can do about waiting work. Each names one operation of this API.
const (
	ActReassign = "reassign"
	ActResolve  = "resolve"
	ActReplan   = "replan"
	ActReset    = "reset"
	ActRespawn  = "respawn"
	ActResume   = "resume"
	ActAccept   = "accept"
	ActRework   = "rework"
	ActCancel   = "cancel"
	// ActStartOrchestrator asks for a worker's orchestrator, which resets a breaker that stopped it
	// and replaces a session that stalled.
	ActStartOrchestrator = "start_orchestrator"
)

// WorkActions lists what a read model can offer, so every surface can be held to knowing each one.
var WorkActions = []string{
	ActReassign, ActResolve, ActReplan, ActReset, ActRespawn, ActResume, ActAccept, ActRework, ActCancel, ActStartOrchestrator,
}

// EffectView is an effect with what a person can say of it.
type EffectView struct {
	ledger.UnknownEffect
	Offers []ledger.EffectOutcome `json:"offers"`
}

// WorkNeed is work that waits for a person, or for no one yet and is going nowhere. An orchestrator has
// no work order and no lane of the kind a work order runs, and a work order may have no lane.
type WorkNeed struct {
	Reason       string                `json:"reason"`
	Detail       string                `json:"detail,omitempty"`
	WorkOrder    *workorders.WorkOrder `json:"work_order,omitempty"`
	Lane         *ledger.LaneView      `json:"lane,omitempty"`
	Orchestrator *ledger.Orchestrator  `json:"orchestrator,omitempty"`
	Actions      []string              `json:"actions"`
}

// NeedItem is one thing that waits for a person. Since is database time, and Kind says which field
// holds the rest.
type NeedItem struct {
	Kind     string               `json:"kind"`
	ID       string               `json:"id"`
	Since    int64                `json:"since"`
	Batch    *questions.Presented `json:"batch,omitempty"`
	Approval *authority.Approval  `json:"approval,omitempty"`
	Effect   *EffectView          `json:"effect,omitempty"`
	Work     *WorkNeed            `json:"work,omitempty"`
}

// NeedsYou is the default view: what a member can act on, the longest wait first (FR-UI-2).
type NeedsYou struct {
	Stamp
	Items []NeedItem `json:"items"`
}

// PlanProgress is a plan and how far its work got.
type PlanProgress struct {
	Plan   planning.Plan              `json:"plan"`
	Total  int                        `json:"total"`
	Done   int                        `json:"done"`
	States map[workorders.State]int64 `json:"states"`
}

// GoalProgress is a goal and its plans. A plan under no goal stands in a group with a nil Goal.
type GoalProgress struct {
	Goal  *planning.Goal `json:"goal,omitempty"`
	Plans []PlanProgress `json:"plans"`
}

// Roadmap is goals, plans, and their progress.
type Roadmap struct {
	Stamp
	Goals []GoalProgress `json:"goals"`
}

// ContractHistory is every version of one contract, oldest first.
type ContractHistory struct {
	ID       string              `json:"id"`
	Versions []planning.Contract `json:"versions"`
}

// PlanReview is one review of a plan, and what it left open.
type PlanReview struct {
	WorkOrder workorders.WorkOrder `json:"work_order"`
	Open      []questions.Question `json:"open_questions"`
}

// PlanDetail is a plan, its contract versions, and its review results.
type PlanDetail struct {
	Plan       planning.Plan          `json:"plan"`
	Goal       *planning.Goal         `json:"goal,omitempty"`
	Contracts  []ContractHistory      `json:"contracts"`
	Reviews    []PlanReview           `json:"reviews"`
	WorkOrders []workorders.WorkOrder `json:"work_orders"`
}

// Plans is the plans and contracts view. Unplanned is work no plan holds, which stands nowhere else
// until it starts.
type Plans struct {
	Stamp
	Plans     []PlanDetail           `json:"plans"`
	Unplanned []workorders.WorkOrder `json:"unplanned"`
}

// Documents is requirements, specs, and designs. Bodies stay out, and a reader asks for one at a time.
type Documents struct {
	Stamp
	Documents    []Document             `json:"documents"`
	Requirements []planning.Requirement `json:"requirements"`
}

// DecisionLine is a decision and its lineage: the chain it belongs to, oldest first, and what took its
// place.
type DecisionLine struct {
	questions.Decision
	Lineage      []string `json:"lineage"`
	SupersededBy string   `json:"superseded_by,omitempty"`
}

// Decisions is every decision, its status, rationale, and lineage.
type Decisions struct {
	Stamp
	Decisions []DecisionLine `json:"decisions"`
}

// Questions is the open questions, and the categories that filter them.
type Questions struct {
	Stamp
	Categories []string             `json:"categories"`
	Questions  []questions.Question `json:"questions"`
}

// WorkLine is a work order, the lane on it, and when it reached the state it stands in.
type WorkLine struct {
	WorkOrder workorders.WorkOrder `json:"work_order"`
	Lane      *ledger.LaneView     `json:"lane,omitempty"`
	Since     int64                `json:"since"`
}

// InProgress is claimed and running work.
type InProgress struct {
	Stamp
	Items []WorkLine `json:"items"`
}

// WakeView is what a parked lane waits for, as its wake states it.
type WakeView struct {
	ID         string `json:"id"`
	Expression string `json:"expression"`
	NextEvalAt int64  `json:"next_eval_at,omitempty"`
}

// HeldLane is a parked lane and what it waits for.
type HeldLane struct {
	WorkOrder workorders.WorkOrder `json:"work_order"`
	Lane      *ledger.LaneView     `json:"lane,omitempty"`
	Wakes     []WakeView           `json:"wakes"`
	Since     int64                `json:"since"`
}

// OnHold is parked lanes and what each waits for.
type OnHold struct {
	Stamp
	Items []HeldLane `json:"items"`
}

// StuckItem is one thing that is going nowhere, and since when.
type StuckItem struct {
	WorkNeed
	ID    string `json:"id"`
	Since int64  `json:"since"`
}

// Stuck is what stopped: `lapsed`, `blocked`, `tripped`, `stalled`, and long-parked.
type Stuck struct {
	Stamp
	Items []StuckItem `json:"items"`
}

// CompletedWork is an outcome with its artifacts and check results, when it came, and what the lane
// said of it.
type CompletedWork struct {
	WorkOrder workorders.WorkOrder      `json:"work_order"`
	Standing  workorders.AcceptanceView `json:"standing"`
	Since     int64                     `json:"since"`
	Reason    string                    `json:"reason,omitempty"`
}

// Completed is work that ended, by any way a work order can end, the newest end first. Work that
// waits for acceptance stands here too.
type Completed struct {
	Stamp
	Items []CompletedWork `json:"items"`
}

// WorkerLine is a worker, how fresh its word is, and the one session it runs.
type WorkerLine struct {
	Worker       ledger.Worker        `json:"worker"`
	Freshness    string               `json:"freshness"`
	Orchestrator *ledger.Orchestrator `json:"orchestrator,omitempty"`
}

// ShopFloor is workers, orchestrators, lanes, and their freshness.
type ShopFloor struct {
	Stamp
	Workers []WorkerLine      `json:"workers"`
	Lanes   []ledger.LaneView `json:"lanes"`
}

// Operations is fleet health beside the product measures of the default window, which come from the
// ledger alone.
type Operations struct {
	Stamp
	Fleet      Fleet                      `json:"fleet"`
	WorkOrders map[workorders.State]int64 `json:"work_orders"`
	Lanes      map[string]int64           `json:"lanes"`
	Workers    int64                      `json:"workers"`
	Fresh      int64                      `json:"workers_fresh"`
	Waiting    int64                      `json:"waiting_for_a_person"`
	Sources    []planning.SourceBinding   `json:"sources"`
	Measures   measures.Report            `json:"measures"`
}

// ReadView reads one read model into out. An empty category does not filter.
func (c *Client) ReadView(ctx context.Context, name, category string, out any) error {
	path := ViewPath(name)
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	return c.Do(ctx, http.MethodGet, path, nil, out)
}
