package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/lanes"
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
	// NeedPlanRevision is a plan whose approved version decisions have moved past: one item for the plan, with the
	// decisions since and the finished work they reached, which waits for a member to confirm the version or
	// revise it (FR-PLAN-11).
	NeedPlanRevision = "plan_revision"
	// NeedCI is CI that asks a person about a work order's pull request: a head that has not finished, a run no
	// runner picked up, or a result no agent can act on (ADR-0037). It starts no agent.
	NeedCI = "ci"
	// NeedSpend is a work order a hard spend limit parked: one item for the work order however many lanes served it,
	// which counts as one interruption (ADR-0037, FR-SPEND-7).
	NeedSpend = "spend"
	// NeedRepositoryCreation is a repository an approved plan version creates whose creation was refused, failed, or was
	// interrupted, or that runs now: one item for each, which a member retries or skips (ADR-0039).
	NeedRepositoryCreation = "repository_creation"
)

// Where a repository creation stands, as its Needs you item says.
const (
	RepositoryCreationRefused     = "refused"
	RepositoryCreationFailed      = "failed"
	RepositoryCreationInterrupted = "interrupted"
	RepositoryCreationRunning     = "running"
)

// What a member can do about a repository creation. Retrying and skipping are operations of this API, and each names the
// command that does the same: `plan retry` and `plan skip`.
const (
	ActRepositoryRetry = "repository_retry"
	ActRepositorySkip  = "repository_skip"
)

// RepositoryCreationActions lists what a read model can offer about a repository creation, so every surface can be held
// to knowing each one.
var RepositoryCreationActions = []string{ActRepositoryRetry, ActRepositorySkip}

// What a member can do about a spend stop. Raising, resuming, blocking, and sending to replanning are operations of this
// API, and each names the command that does the same: `spend raise`, `spend resume`, `spend block`, and `spend replan`.
const (
	ActSpendRaise  = "spend_raise"
	ActSpendResume = "spend_resume"
	ActSpendBlock  = "spend_block"
	ActSpendReplan = "spend_replan"
)

// SpendActions lists what a read model can offer about a spend stop, so every surface can be held to knowing each one.
var SpendActions = []string{ActSpendRaise, ActSpendResume, ActSpendBlock, ActSpendReplan}

// What a member can do about CI that asks for them. Wait longer and rerun are operations of this API, and opening the
// run is the forge's own page, which an item offers when the forge gave an address a link can hold.
const (
	ActCIWait  = "ci_wait"
	ActCIRerun = "ci_rerun"
	ActCIOpen  = "ci_open_run"
)

// CIActions lists what a read model can offer about CI, so every surface can be held to knowing each one.
var CIActions = []string{ActCIWait, ActCIRerun, ActCIOpen}

// Why work waits for a person. The first five are the design's stuck list.
const (
	ReasonLapsed     = "lapsed"
	ReasonBlocked    = "blocked"
	ReasonTripped    = "tripped"
	ReasonStalled    = "stalled"
	ReasonLongParked = "long_parked"
	// ReasonWorkerBehind is a claim whose worker is too old to read the lane a delegate started under a delegate
	// policy that restricts it (FR-AUTH-16). The worker renews the claim and starts nothing until it is upgraded,
	// so nothing lapses and the claim would otherwise look as if it were about to start.
	ReasonWorkerBehind = "worker_behind"
	// ReasonFailed is work whose lane failed, which stays failed until a member retries it, because nothing
	// retries it on its own (ADR-0014).
	ReasonFailed = "failed"
	// ReasonAcceptance is finished work that waits for a member's word.
	ReasonAcceptance = "acceptance"
	// ReasonResume is a parked lane that waits on a person and on nothing else.
	ReasonResume = "human_resume"
	// ReasonHistoryMissing is a merged adoption branch whose base lacks the design repository's tip it imported,
	// because a squash or a rebase merge dropped the history (FR-ADOPT-13). It clears once a check finds the tip.
	ReasonHistoryMissing = "history_missing"
	// ReasonHistoryUnread is a merged adoption branch whose base could not be checked for the design repository's
	// tip, such as one whose plan approval could not read the import. It clears once a check answers.
	ReasonHistoryUnread = "history_unread"
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
	// ActRetry returns failed work to ready, which adds an attempt when none is left.
	ActRetry = "retry"
	// ActStartOrchestrator asks for a worker's orchestrator, which resets a breaker that stopped it
	// and replaces a session that stalled.
	ActStartOrchestrator = "start_orchestrator"
)

// WorkActions lists what a read model can offer, so every surface can be held to knowing each one.
var WorkActions = []string{
	ActReassign, ActResolve, ActReplan, ActReset, ActRespawn, ActResume, ActAccept, ActRework, ActCancel, ActRetry, ActStartOrchestrator,
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
	// ExtraAttempt says the retry that failed work waits for adds an attempt, because the work used every one
	// it was given. Only a member can add one, so the card says so.
	ExtraAttempt bool `json:"extra_attempt,omitempty"`
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
	// Replaced lists the drafts approval will not decide again, because a decision outside the plan replaced or
	// overturned what an earlier version decided for them and their text has not changed. Each names the decision in
	// force. It is absent when there are none.
	Replaced   []questions.ReplacedDraft `json:"replaced,omitempty"`
	Difference planning.Difference       `json:"difference"`
	Summary    string                    `json:"summary"`
	// Adoption groups a plan that adopts an existing project, with every file its branch changed (ADR-0023).
	Adoption *planning.Adoption `json:"adoption,omitempty"`
	// Revision is the plan's open revision when this version is a later one, which the member reads beside the
	// difference: the decisions made since the approved version, and approving this version is the word on them.
	// It is absent when no decision moved past the approved version.
	Revision *PlanRevisionView `json:"revision,omitempty"`
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
	Project        *ProjectLabel       `json:"project,omitempty"`
	Kind           string              `json:"kind"`
	ID             string              `json:"id"`
	Since          int64               `json:"since"`
	Batch          *QuestionBatch      `json:"batch,omitempty"`
	Approval       *authority.Approval `json:"approval,omitempty"`
	Effect         *EffectView         `json:"effect,omitempty"`
	Work           *WorkNeed           `json:"work,omitempty"`
	PlanApproval   *PlanApprovalNeed   `json:"plan_approval,omitempty"`
	ContractReview *ContractReviewNeed `json:"contract_review,omitempty"`
	Card           *CardNeed           `json:"card,omitempty"`
	PlanRevision   *PlanRevisionView   `json:"plan_revision,omitempty"`
	CI             *CINeed             `json:"ci,omitempty"`
	Spend          *SpendNeed          `json:"spend,omitempty"`
	// RepositoryCreation is set for a repository_creation item, whose ID is the approval and the entry's name, joined by a
	// slash.
	RepositoryCreation *RepositoryCreationNeed `json:"repository_creation,omitempty"`
}

// RepositoryCreationNeed is a repository an approved plan version creates that waits for a member, or runs: the approval
// and the plan version it belongs to, the entry's name, its path as the manifest writes it and where that leads on the
// worker's machine, where its creation stands, and why it stopped. State is one of the RepositoryCreation constants, and
// a running creation offers no action. Actions says what the member can do.
type RepositoryCreationNeed struct {
	Approval     string `json:"approval"`
	Plan         string `json:"plan"`
	PlanVersion  int64  `json:"plan_version"`
	Name         string `json:"name"`
	ManifestPath string `json:"manifest_path"`
	Path         string `json:"path,omitempty"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	// Remedy says what a member does about it, when something can be done besides Retry and Skip.
	Remedy string `json:"remedy,omitempty"`
	// Creation is what the creation did before it stopped, when it did anything.
	Creation *ledger.CreationOutcome `json:"creation,omitempty"`
	Actions  []string                `json:"actions"`
}

// SpendNeed is a work order a hard spend limit parked, with what a member reads before deciding: what it is charged
// against its soft and hard limits and what remains, the band its limits came from and how far that can be trusted,
// what Resume once would grant, and what each lane that served it left. Actions says what the member can do. The lanes
// and the work order's brief never receive any of it (FR-SPEND-9).
type SpendNeed struct {
	WorkOrder workorders.WorkOrder  `json:"work_order"`
	Spend     ledger.WorkOrderSpend `json:"spend"`
	// Band is the work order's cost band, absent when it has no estimate.
	Band *delivery.SpendBand `json:"band,omitempty"`
	// Resume is what Resume once grants, absent when nothing sizes one more generation, which leaves a raise.
	Resume *SpendResume `json:"resume,omitempty"`
	Lanes  []SpendLane  `json:"lanes"`
	// WaitingShare is the share of the work order's spend that was waiting. It is absent while no model call has been
	// attributed as waiting or working.
	WaitingShare *float64 `json:"waiting_share,omitempty"`
	Actions      []string `json:"actions"`
}

// SpendResume is what Resume once would do now: the allowance of one more generation, where the figure comes from, and
// the hard limit it sets.
type SpendResume struct {
	AllowanceUSD float64 `json:"allowance_usd"`
	Basis        string  `json:"basis"`
	LimitUSD     float64 `json:"limit_usd"`
}

// SpendLane is what one lane of a stopped work order left: the digest of its last generation that sent one, and its
// last handoff, which is the agent's narrative or the note that it degraded to the deterministic part.
type SpendLane struct {
	Lane       string        `json:"lane"`
	Generation int64         `json:"generation,omitempty"`
	Digest     *lanes.Digest `json:"digest,omitempty"`
	Handoff    *SpendHandoff `json:"handoff,omitempty"`
}

// SpendHandoff is a lane's last handoff. Narrative is absent when the handoff degraded.
type SpendHandoff struct {
	Generation int64            `json:"generation"`
	Reason     string           `json:"reason,omitempty"`
	Degraded   bool             `json:"degraded,omitempty"`
	Narrative  *lanes.Narrative `json:"narrative,omitempty"`
}

// CINeed is what CI asks of a person about a work order's pull request. Kind is the item and the notice, one of
// ledger.CIItemKinds. Watch says where the head stands against the repository's limits, and Finding what the
// classification found, for the kinds it raises. Actions says what the person can do, and RunURL is the run's page on
// the forge when it gave an address a link can hold.
type CINeed struct {
	Kind        string               `json:"kind"`
	WorkOrder   workorders.WorkOrder `json:"work_order"`
	Number      int64                `json:"number"`
	PullRequest string               `json:"pull_request_url,omitempty"`
	Watch       ledger.CIWatch       `json:"watch"`
	Finding     *ledger.CIFinding    `json:"finding,omitempty"`
	RunURL      string               `json:"run_url,omitempty"`
	Actions     []string             `json:"actions"`
}

// CIWait is a member's Wait longer: the time to add to the CI timeout, or none for one more p85.
type CIWait struct {
	// By is a Go duration such as "45m", and empty for one more p85 of the repository's CI.
	By string `json:"by,omitempty"`
}

// CIWaited is where the head stands after Wait longer.
type CIWaited struct {
	WorkOrder string         `json:"work_order"`
	Watch     ledger.CIWatch `json:"watch"`
}

// CIRerun is what a rerun left: the reruns of the head, a member's and the automatic ones.
type CIRerun struct {
	WorkOrder string `json:"work_order"`
	Reruns    int64  `json:"reruns"`
}

// CardNeed is a confirmation card that waits for a member: the request, the operation it runs, and the
// delegate that prepared it, by its credential's label and the member it acts for. A credential whose
// record was removed reads as its ID.
type CardNeed struct {
	Approval authority.Approval   `json:"approval"`
	Detail   authority.CardDetail `json:"detail"`
	Label    string               `json:"label"`
	Member   string               `json:"member"`
	// DelegateKind is the credential's kind, `external` or `assistant`, which the card's form is decided on.
	DelegateKind string `json:"delegate_kind,omitempty"`
	// Conversation is the conversation with the assistant that prepared the card, for one an assistant's
	// credential prepared.
	Conversation *CardConversation `json:"conversation,omitempty"`
}

// CardConversation names the conversation a card the assistant prepared came from. After the conversation
// was deleted its name is empty and Deleted says so: the card stays in the ledger.
type CardConversation struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted,omitempty"`
}

// ApprovalDetail is one approval request as its own route answers it, with the conversation a card the
// assistant prepared came from.
type ApprovalDetail struct {
	authority.Approval
	Conversation *CardConversation `json:"conversation,omitempty"`
	// Registration is what a plan approval registered of the repositories its version's manifest lists.
	Registration *ManifestRegistration `json:"registration,omitempty"`
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
	// MilestoneAcceptedNotMerged is a milestone whose work spans repositories and is all done, where some of
	// it is in a repository at person_merges with review target branch: there done means a member accepted
	// the work, and the person merges the branch where ropeshift does not see it (ADR-0022, FR-REPO-4).
	// Nothing waits for it, as work that depends on work accepted there does not.
	MilestoneAcceptedNotMerged = "accepted_not_merged"
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

// PlanReview is one review of a plan, and what it left open: each question with the scope an answer to it takes
// and the plan's title, as QuestionView says.
type PlanReview struct {
	WorkOrder workorders.WorkOrder `json:"work_order"`
	Open      []QuestionView       `json:"open_questions"`
}

// PlanDetail is a plan, its contract versions, and its review results. Milestones are the plan's
// milestones in priority order, which a contract names by ID, PendingVersion is the plan version
// whose approval waits for a member, or 0 when none does, and ApprovedVersion is the newest version a
// member approved, which is the one in force, or 0 when none is.
type PlanDetail struct {
	Plan            planning.Plan          `json:"plan"`
	Goal            *planning.Goal         `json:"goal,omitempty"`
	Contracts       []ContractHistory      `json:"contracts"`
	Reviews         []PlanReview           `json:"reviews"`
	WorkOrders      []workorders.WorkOrder `json:"work_orders"`
	Milestones      []MilestoneProgress    `json:"milestones"`
	PendingVersion  int64                  `json:"pending_version,omitempty"`
	ApprovedVersion int64                  `json:"approved_version,omitempty"`
}

// WorkAllowance is what a member can do to one work order as it stands now. The domain's state machine says
// it, so no view holds a copy of the machine.
type WorkAllowance struct {
	// Actions are the work actions its state allows, in the order a view offers them: claim, start_lane,
	// retry, release, send_back, reassign, resolve, replan, cancel, and archive_lane.
	Actions []string `json:"actions"`
	// ExtraAttempt says its retry adds an attempt, because it used every attempt it was given.
	ExtraAttempt bool `json:"extra_attempt,omitempty"`
}

// Plans is the plans and contracts view. Unplanned is work no plan holds, which stands nowhere else
// until it starts. Reviews are the reviews of lane work that have not ended: a review is no part of the plan
// its contract belongs to, so no plan lists it, and a ready one waits for a member to start it as any ready
// work does. WorkActions says what a member can do to each work order of the project, by its ID.
type Plans struct {
	Stamp
	Plans       []PlanDetail             `json:"plans"`
	Unplanned   []workorders.WorkOrder   `json:"unplanned"`
	Reviews     []workorders.WorkOrder   `json:"reviews"`
	WorkActions map[string]WorkAllowance `json:"work_actions"`
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
// place. DecidedByName is the name of the worker whose orchestrator decided it, which its ID does not say, and
// ScopeTitle the title of the record its scope names, as DecisionView says. DecidedByKind is the kind of party that
// recorded it, `member` or `orchestrator` among them, as the event that recorded it names, so a decision whose
// orchestrator's worker the ledger no longer holds still reads as an orchestrator's.
type DecisionLine struct {
	questions.Decision
	Lineage       []string `json:"lineage"`
	SupersededBy  string   `json:"superseded_by,omitempty"`
	DecidedByName string   `json:"decided_by_name,omitempty"`
	DecidedByKind string   `json:"decided_by_kind,omitempty"`
	ScopeTitle    string   `json:"scope_title,omitempty"`
}

// Decisions is every decision, its status, rationale, and lineage.
type Decisions struct {
	Stamp
	Decisions []DecisionLine `json:"decisions"`
}

// Questions is the open questions, or those a QuestionFilter selects, and the categories that filter them.
type Questions struct {
	Stamp
	Categories []string       `json:"categories"`
	Questions  []QuestionView `json:"questions"`
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

// Stuck is what stopped: `lapsed`, `blocked`, `tripped`, `stalled`, long-parked, and `failed`.
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
	// Detail is what the lane's outcome said of its reason, such as the delegate policy version that refused
	// the lane's start.
	Detail string `json:"detail,omitempty"`
}

// Completed is work that ended, by any way a work order can end, the newest end first. Work that
// waits for acceptance stands here too.
type Completed struct {
	Stamp
	Items []CompletedWork `json:"items"`
}

// WorkerLine is a worker, how fresh its word is, and the one session it runs. ReportFreshness says how
// fresh its last resource report is, and is absent until it reports one (FR-UI-10). Drivers is the doctor's
// result for each driver its manifest names, in the words `ropeshift worker doctor` prints, and is empty for
// a worker whose manifest names none.
type WorkerLine struct {
	Worker          ledger.Worker         `json:"worker"`
	Freshness       string                `json:"freshness"`
	ReportFreshness string                `json:"report_freshness,omitempty"`
	Drivers         []ledger.DriverResult `json:"drivers"`
	Orchestrator    *Orchestrator         `json:"orchestrator,omitempty"`
	// OrchestratorHold is what keeps the worker's orchestrator from starting or running now, and ScheduledStart the
	// start a member scheduled for its sign-in's usage reset, whether or not it ever ran one.
	OrchestratorHold *ledger.OrchestratorHold `json:"orchestrator_hold,omitempty"`
	ScheduledStart   *ledger.ScheduledStart   `json:"scheduled_start,omitempty"`
}

// FloorLane is a lane and the title of the work it runs, because a lane's ID says nothing to a person.
// Archivable says a member may ask for its worktree to be removed: the lane ended, and the worktree still
// stands.
type FloorLane struct {
	ledger.LaneView
	WorkOrderTitle string `json:"work_order_title,omitempty"`
	Archivable     bool   `json:"archivable,omitempty"`
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
	// SearchConversation is one of the calling member's conversations with the assistant, by its name.
	SearchConversation = "conversation"
)

// SearchKinds lists every kind the search finds, in order.
var SearchKinds = []string{
	SearchWorkOrder, SearchLane, SearchPlan, SearchContract, SearchRequirement, SearchDecision, SearchQuestion,
	SearchDocument, SearchRepository, SearchConversation,
}

// SearchLimit is how many records a search with words returns when it names no limit. A search with no
// words lists every record, which is what a command line reads.
const SearchLimit = 20

// SearchRecord is one record a search found: its kind, its ID, what it is called, and the state it stands in.
// A work order also says what a member can do to it now, as the plans view does.
type SearchRecord struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Title   string   `json:"title,omitempty"`
	State   string   `json:"state,omitempty"`
	Actions []string `json:"actions,omitempty"`
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
	// Notes are the security notes the reviews left for the current attempt and its commit. They decide nothing.
	Notes []workorders.Note `json:"notes,omitempty"`
	Why   string            `json:"why,omitempty"`
	// Reason and Detail are what the outcome of the lane that ended the work said of why: its reason, and what the
	// worker said of it, such as the delegate policy version that refused the lane's start. Both are empty for work
	// no lane's outcome ended, and a lane that ended with no reason leaves the first empty.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	// PullRequest is the pull request the work lands through, when its repository reviews through pull
	// requests.
	PullRequest *ledger.PullRequest `json:"pull_request,omitempty"`
	// Integrations are the approvals of its reviewed commits for merge, and the merges that followed them.
	Integrations []ledger.IntegrationRecord `json:"integrations,omitempty"`
	// Estimate is its current estimate, with its confidence and explanation, once one was made.
	Estimate *delivery.Estimate `json:"estimate,omitempty"`
	// Baseline is the estimate its baseline holds, once one is recorded.
	Baseline *WorkBaseline `json:"baseline,omitempty"`
}
