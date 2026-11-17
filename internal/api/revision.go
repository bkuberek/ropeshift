package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PlanRevisionView is what a member reads of a plan that decisions have moved past: the decisions made since the
// version a member approved, with what each says, the finished work the reversible ones reached, and the work those
// decisions bind that waits in contract review until the revision closes. Revision is absent for a plan with none
// open.
type PlanRevisionView struct {
	Plan      string                 `json:"plan"`
	Title     string                 `json:"title"`
	Revision  *planning.Revision     `json:"revision,omitempty"`
	Decisions []RevisionDecision     `json:"decisions"`
	Finished  []RevisionFinishedWork `json:"finished"`
	Waiting   []RevisionWaitingWork  `json:"waiting"`
}

// RevisionDecision is one decision of a revision.
type RevisionDecision struct {
	ID            string                   `json:"id"`
	Statement     string                   `json:"statement"`
	Scope         string                   `json:"scope,omitempty"`
	Reversibility string                   `json:"reversibility"`
	Status        questions.DecisionStatus `json:"status"`
	DecidedBy     string                   `json:"decided_by,omitempty"`
}

// RevisionFinishedWork is a finished work order that reversible decisions reached, with the decisions that did and
// the rework a member made of it, which is empty until one did.
type RevisionFinishedWork struct {
	WorkOrder string           `json:"work_order"`
	Title     string           `json:"title"`
	State     workorders.State `json:"state"`
	Decisions []string         `json:"decisions"`
	Rework    string           `json:"rework,omitempty"`
}

// RevisionWaitingWork is a work order that a decision of the revision binds, which waits in contract review for the
// revision to close.
type RevisionWaitingWork struct {
	WorkOrder string `json:"work_order"`
	Title     string `json:"title"`
}

// ReviseSession is the planning session a revision starts: the work order's ID, which the client generates, and the
// repository it plans in. An empty repository takes none, as a planning session with no goal does.
type ReviseSession struct {
	ID         string `json:"id"`
	Repository string `json:"repository,omitempty"`
}

// DecisionRework asks for the rework of one finished work order for the decisions a revision lists for it.
type DecisionRework struct {
	Decisions []string `json:"decisions"`
}

// DecisionReworked is the rework a request made, or the one that waits and took the decisions.
type DecisionReworked struct {
	WorkOrder string `json:"work_order"`
	Rework    string `json:"rework"`
}

func revisionPath(plan string, rest ...string) string {
	path := "/v1/plans/" + url.PathEscape(plan) + "/revision"
	for _, part := range rest {
		path += "/" + url.PathEscape(part)
	}
	return path
}

// PlanRevision reads the open revision of a plan, or one with no Revision when the plan has none.
func (c *ProjectClient) PlanRevision(ctx context.Context, plan string) (PlanRevisionView, error) {
	var out PlanRevisionView
	err := c.do(ctx, http.MethodGet, revisionPath(plan), nil, &out)
	return out, err
}

// ConfirmRevision records that the approved version of a plan stands with the decisions since, which closes the
// revision and releases the work that waited for it.
func (c *ProjectClient) ConfirmRevision(ctx context.Context, plan string) (planning.Revision, error) {
	var out planning.Revision
	err := c.do(ctx, http.MethodPost, revisionPath(plan, "confirm"), struct{}{}, &out)
	return out, err
}

// ReviseSession records the planning work order of a session that revises a plan, whose brief lists the decisions
// of its open revision. The caller reviews, claims, and starts it as it does any planning session.
func (c *ProjectClient) ReviseSession(ctx context.Context, plan string, in ReviseSession) (workorders.WorkOrder, error) {
	var out workorders.WorkOrder
	err := c.do(ctx, http.MethodPost, revisionPath(plan, "sessions"), in, &out)
	return out, err
}

// DecisionRework makes the rework of one finished work order for decisions its plan's revision lists.
func (c *ProjectClient) DecisionRework(ctx context.Context, id string, in DecisionRework) (DecisionReworked, error) {
	var out DecisionReworked
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "decision-rework"), in, &out)
	return out, err
}
