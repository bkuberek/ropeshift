package ledger

// ApproverKind names who approved a merge.
type ApproverKind string

// Who approves a merge (FR-MERGE-6).
const (
	// ApprovedByMember is a member, whose approval or acceptance binds to the reviewed commit.
	ApprovedByMember ApproverKind = "member"
	// ApprovedByReview is the review lane, by its generation, whose every-criterion pass stands for the
	// person's word at review_approves.
	ApprovedByReview ApproverKind = "review"
	// ApprovedByForge is the person merging the pull request on the forge, under an account the ledger does
	// not know as a member, at a level where the person merges.
	ApprovedByForge ApproverKind = "forge"
)

// IntegrationRecord is one approval of a work order's reviewed commit for merge, and the merge that followed
// it (ADR-0020, FR-MERGE-6). An approval binds to its commit: a new commit needs an approval of its own, and
// this one never merges anything else. MergedAt is zero until the commit lands. The approval of a planning
// branch is a member's plan approval, and names the plan version it approved (ADR-0018).
type IntegrationRecord struct {
	ID             string         `json:"id"`
	WorkOrder      string         `json:"work_order_id"`
	PullRequest    int64          `json:"pull_request,omitempty"`
	ApprovedCommit string         `json:"approved_commit"`
	MergeAuthority MergeAuthority `json:"merge_authority"`
	ApprovedByKind ApproverKind   `json:"approved_by_kind"`
	ApprovedByID   string         `json:"approved_by_id"`
	ApprovedAt     int64          `json:"approved_at"`
	MergedCommit   string         `json:"merged_commit,omitempty"`
	MergedAt       int64          `json:"merged_at,omitempty"`
	Plan           string         `json:"plan_id,omitempty"`
	PlanVersion    int64          `json:"plan_version,omitempty"`
}

// Merged says the approved commit landed.
func (r IntegrationRecord) Merged() bool { return r.MergedAt != 0 }

// IntegrationID keys the approval of one commit of a work order, so the same approval recorded twice is one.
func IntegrationID(workOrder, commit string) string { return "integration:" + workOrder + ":" + commit }

// The events of integration. Each names the work order, the pull request when there is one, the level in
// force, who approved, the approved commit, and once it landed, the merged commit.
const (
	// SubjectIntegration is what integration's events are about: the work order that merges.
	SubjectIntegration = "integration"
	// EventIntegrationApproved records an approval of a reviewed commit for merge.
	EventIntegrationApproved = "integration.approved"
	// EventIntegrationMerged records the reviewed commit landing on its base.
	EventIntegrationMerged = "integration.merged"
	// EventIntegrationHeld records a merge that cannot go on without a member, with why.
	EventIntegrationHeld = "integration.held"
	// EventIntegrationWaiting records approved work whose merge waits for work it depends on to merge, with
	// each dependency, once for each set of them (ADR-0020, FR-REPO-3).
	EventIntegrationWaiting = "integration.waiting"
	// EventIntegrationReported records what a worker's merge came to, whatever it was.
	EventIntegrationReported = "integration.reported"
)
