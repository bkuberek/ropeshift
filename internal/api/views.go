package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
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
	// ViewSearch is the project's records a person finds by ID or title, which the command palette reads.
	ViewSearch = "search"
)

// Views lists every read model, in the design's order.
var Views = []string{
	ViewNeedsYou, ViewRoadmap, ViewPlans, ViewDocuments, ViewDecisions, ViewQuestions, ViewInProgress,
	ViewOnHold, ViewStuck, ViewCompleted, ViewShopFloor, ViewOperations, ViewSearch,
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
	// NeedPlanApproval is a plan version that waits for a member to approve it (FR-PLAN-12).
	NeedPlanApproval = "plan_approval"
	// NeedContractReview is work whose contract waits for a member's review, because no plan approval
	// stands behind it.
	NeedContractReview = "contract_review"
	// NeedConfirm is a confirmation card a delegate prepared, which runs only when a member confirms it.
	NeedConfirm = "confirm"
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
// no work order and no lane of the kind a work order runs, and a work order may have no lane. Wakes is
// what a parked lane waits for, so a person reads what would move it before resuming it by hand.
type WorkNeed struct {
	Reason       string                `json:"reason"`
	Detail       string                `json:"detail,omitempty"`
	WorkOrder    *workorders.WorkOrder `json:"work_order,omitempty"`
	Lane         *ledger.LaneView      `json:"lane,omitempty"`
	Orchestrator *ledger.Orchestrator  `json:"orchestrator,omitempty"`
	Wakes        []WakeView            `json:"wakes,omitempty"`
	// Standing is where finished work stands when it waits for a member: its checks, and the verdict the review
	// gave for each criterion with its reasons and citations.
	Standing *workorders.AcceptanceView `json:"standing,omitempty"`
	Actions  []string                   `json:"actions"`
}

// PlanApprovalNeed is a plan version that waits for a member: what it is, who asked, what it holds,
// how it differs from the last version a member approved, and the answers it drafts, which approval
// decides. A new version that changes nothing approved and adds no work shows an empty difference.
type PlanApprovalNeed struct {
	Approval    authority.Approval       `json:"approval"`
	Title       string                   `json:"title"`
	Version     int64                    `json:"version"`
	RequestedBy string                   `json:"requested_by"`
	Counts      map[string]int           `json:"counts"`
	Decisions   int                      `json:"decisions"`
	Drafts      []planning.DraftDecision `json:"drafts"`
	Difference  planning.Difference      `json:"difference"`
	Summary     string                   `json:"summary"`
	// Adoption groups a plan that adopts an existing project, with every file its branch changed (ADR-0023).
	Adoption *planning.Adoption `json:"adoption,omitempty"`
}

// ContractReviewNeed is work in `contract_review` that no plan approval covers, so a member passes its
// review by hand. Plan is the plan its contract belongs to, where a person reads the contract.
type ContractReviewNeed struct {
	WorkOrder workorders.WorkOrder `json:"work_order"`
	Contract  string               `json:"contract"`
	Version   int64                `json:"version"`
	Plan      string               `json:"plan"`
}

// NeedItem is one thing that waits for a person. Since is database time, and Kind says which field
// holds the rest. Project names its project in the combined Needs you.
type NeedItem struct {
	Project        *ProjectLabel        `json:"project,omitempty"`
	Kind           string               `json:"kind"`
	ID             string               `json:"id"`
	Since          int64                `json:"since"`
	Batch          *questions.Presented `json:"batch,omitempty"`
	Approval       *authority.Approval  `json:"approval,omitempty"`
	Effect         *EffectView          `json:"effect,omitempty"`
	Work           *WorkNeed            `json:"work,omitempty"`
	PlanApproval   *PlanApprovalNeed    `json:"plan_approval,omitempty"`
	ContractReview *ContractReviewNeed  `json:"contract_review,omitempty"`
	Card           *CardNeed            `json:"card,omitempty"`
}

// CardNeed is a confirmation card that waits for a member: the request, the operation it runs, and the
// delegate that prepared it, by its credential's label and the member it acts for. A credential whose
// record was removed reads as its ID.
type CardNeed struct {
	Approval authority.Approval   `json:"approval"`
	Detail   authority.CardDetail `json:"detail"`
	Label    string               `json:"label"`
	Member   string               `json:"member"`
}

// NeedsYou is the default view: what a member can act on, the longest wait first (FR-UI-2). The combined
// view lists every active project's, and Projects says where each project's ledger stood.
type NeedsYou struct {
	Stamp
	Items    []NeedItem     `json:"items"`
	Projects []NeedsProject `json:"projects,omitempty"`
}

// NeedsYouPath is the combined Needs you: what waits on a member in every active project (FR-PROJ-8).
const NeedsYouPath = "/v1/needs-you"

// ProjectLabel names the project an item of the combined Needs you belongs to.
type ProjectLabel struct {
	ID          ledger.WorkspaceID `json:"id"`
	Name        string             `json:"name"`
	DisplayName string             `json:"display_name,omitempty"`
}

// NeedsProject is one project's part of the combined Needs you: the revision its ledger stood at, and how
// many of its items wait.
type NeedsProject struct {
	ProjectLabel
	Revision int64 `json:"revision"`
	Waiting  int   `json:"waiting"`
}

// NeedsYou reads the combined Needs you of every active project.
func (c *Client) NeedsYou(ctx context.Context) (NeedsYou, error) {
	var out NeedsYou
	err := c.Do(ctx, http.MethodGet, NeedsYouPath, nil, &out)
	return out, err
}

// The states of a milestone, from its work orders and the milestones it depends on. A milestone is done
// when it holds work and all of it is done, and it is in progress once any of its work went past
// `ready`. Otherwise it waits when a milestone it depends on is not done, and it has not started when
// none is.
const (
	MilestoneNotStarted = "not_started"
	MilestoneWaiting    = "waiting"
	MilestoneInProgress = "in_progress"
	MilestoneDone       = "done"
	// MilestoneExisting is a milestone the code already delivers (ADR-0023), which nothing waits for.
	MilestoneExisting = "existing"
)

// MilestoneProgress is one milestone of a plan and how far its work got. DependsOn lists the IDs of the
// milestones it waits for, and Total and Done count the work orders of the contracts in it.
type MilestoneProgress struct {
	ID           string                   `json:"id"`
	Title        string                   `json:"title"`
	Priority     int64                    `json:"priority"`
	DependsOn    []string                 `json:"depends_on"`
	ExitCriteria []planning.ExitCriterion `json:"exit_criteria"`
	Total        int                      `json:"total"`
	Done         int                      `json:"done"`
	State        string                   `json:"state"`
	// Existing says the code already delivers the milestone.
	Existing bool `json:"existing,omitempty"`
}

// CriticalPath is the heaviest chain of work that is left, in the order it runs. Work that is done or
// canceled is not left, and failed work is, because it has to be redone. Weight is what is left of it: a small work order counts 1, a medium one 2, and a large one 3. The ledger computes it
// from the dependencies with no model.
type CriticalPath struct {
	WorkOrders []string `json:"work_orders"`
	Weight     int      `json:"weight"`
}

// PlannedWork is one work order of a plan as the roadmap places it: what it is called, where it stands,
// how large it is, and the milestone its contract belongs to.
type PlannedWork struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	State     workorders.State `json:"state"`
	Size      string           `json:"size,omitempty"`
	Milestone string           `json:"milestone,omitempty"`
}

// PlanProgress is a plan and how far its work got. Milestones come in priority order. Layers group every
// work order of the plan by how much must come before it, so the work of one layer can run at the same
// time. Work names each work order the layers and the critical path hold.
type PlanProgress struct {
	Plan         planning.Plan              `json:"plan"`
	Total        int                        `json:"total"`
	Done         int                        `json:"done"`
	States       map[workorders.State]int64 `json:"states"`
	Milestones   []MilestoneProgress        `json:"milestones"`
	Layers       [][]string                 `json:"layers"`
	CriticalPath CriticalPath               `json:"critical_path"`
	Work         []PlannedWork              `json:"work"`
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

// ContractHistory is every version of one contract, oldest first. Key is the ID the contract's file
// gave it, which is how another contract's `uses` names it.
type ContractHistory struct {
	ID       string              `json:"id"`
	Key      string              `json:"key,omitempty"`
	Versions []planning.Contract `json:"versions"`
}

// PlanReview is one review of a plan, and what it left open.
type PlanReview struct {
	WorkOrder workorders.WorkOrder `json:"work_order"`
	Open      []questions.Question `json:"open_questions"`
}

// PlanDetail is a plan, its contract versions, and its review results. Milestones are the plan's
// milestones in priority order, which a contract names by ID, and PendingVersion is the plan version
// whose approval waits for a member, or 0 when none does.
type PlanDetail struct {
	Plan           planning.Plan          `json:"plan"`
	Goal           *planning.Goal         `json:"goal,omitempty"`
	Contracts      []ContractHistory      `json:"contracts"`
	Reviews        []PlanReview           `json:"reviews"`
	WorkOrders     []workorders.WorkOrder `json:"work_orders"`
	Milestones     []MilestoneProgress    `json:"milestones"`
	PendingVersion int64                  `json:"pending_version,omitempty"`
}

// Plans is the plans and contracts view. Unplanned is work no plan holds, which stands nowhere else
// until it starts.
type Plans struct {
	Stamp
	Plans     []PlanDetail           `json:"plans"`
	Unplanned []workorders.WorkOrder `json:"unplanned"`
}

// Documents is requirements, specs, and designs: the files of every source as they stand, each
// requirement's newest version, and for each plan the documents its files recorded with every version
// and how its requirements trace to work. Bodies stay out, and a reader asks for one at a time.
type Documents struct {
	Stamp
	Documents    []Document             `json:"documents"`
	Requirements []planning.Requirement `json:"requirements"`
	Plans        []PlanDocuments        `json:"plans"`
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

// FloorLane is a lane and the title of the work it runs, because a lane's ID says nothing to a person.
type FloorLane struct {
	ledger.LaneView
	WorkOrderTitle string `json:"work_order_title,omitempty"`
}

// ShopFloor is workers, orchestrators, lanes, and their freshness.
type ShopFloor struct {
	Stamp
	Workers []WorkerLine `json:"workers"`
	Lanes   []FloorLane  `json:"lanes"`
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
	// Forges is what each worker last said of its `gh` sign-in for each repository that needs the forge,
	// which work waits on when it cannot run (ADR-0020).
	Forges   []ForgeLine     `json:"forges,omitempty"`
	Measures measures.Report `json:"measures"`
}

// ForgeLine is one worker's forge sign-in for one repository, with the name the project calls it.
type ForgeLine struct {
	ledger.ForgeHealth
	RepositoryName string `json:"repository_name,omitempty"`
}

// ReadView reads one read model into out. An empty category does not filter.
func (c *ProjectClient) ReadView(ctx context.Context, name, category string, out any) error {
	path := ViewPath(name)
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// The kinds of record the search finds, in the order a list of them reads.
const (
	SearchWorkOrder   = "work_order"
	SearchLane        = "lane"
	SearchPlan        = "plan"
	SearchContract    = "contract"
	SearchRequirement = "requirement"
	SearchDecision    = "decision"
	SearchQuestion    = "question"
	SearchDocument    = "document"
	SearchRepository  = "repository"
)

// SearchKinds lists every kind the search finds, in order.
var SearchKinds = []string{
	SearchWorkOrder, SearchLane, SearchPlan, SearchContract, SearchRequirement, SearchDecision, SearchQuestion,
	SearchDocument, SearchRepository,
}

// SearchLimit is how many records a search with words returns when it names no limit. A search with no
// words lists every record, which is what a command line reads.
const SearchLimit = 20

// SearchRecord is one record a search found: its kind, its ID, what it is called, and the state it stands in.
type SearchRecord struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	State string `json:"state,omitempty"`
}

// Search is the records of one project whose ID or title holds every word of a query, in any order and
// any case: an exact ID first, then an ID that begins with the words, then words that begin a word of the
// title, then the rest. More says a limit cut the list.
type Search struct {
	Stamp
	Query   string         `json:"query,omitempty"`
	Records []SearchRecord `json:"records"`
	More    bool           `json:"more,omitempty"`
}

// Search reads the search read model. The query names q, the words; category, one kind; and limit.
func (c *ProjectClient) Search(ctx context.Context, query url.Values) (Search, error) {
	path := ViewPath(ViewSearch)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var out Search
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// WorkDetail is a work order with how its review stands: the reviews it has had, the verdicts that speak for
// its current attempt, and what the work waits on when its verdicts hold it for a member.
type WorkDetail struct {
	workorders.WorkOrder
	// JudgedBy is named apart from the work order's own `reviews`, which a review sets to the work it judges.
	JudgedBy []workorders.ReviewLine `json:"judged_by,omitempty"`
	Verdicts []workorders.Verdict    `json:"verdicts,omitempty"`
	Why      string                  `json:"why,omitempty"`
	// PullRequest is the pull request the work lands through, when its repository reviews through pull
	// requests.
	PullRequest *ledger.PullRequest `json:"pull_request,omitempty"`
	// Integrations are the approvals of its reviewed commits for merge, and the merges that followed them.
	Integrations []ledger.IntegrationRecord `json:"integrations,omitempty"`
}
