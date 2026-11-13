package server

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// BranchSource opens a project's repository at a branch or a commit, in the checkout of the machine the
// daemon runs on, for what is committed there.
type BranchSource func(ctx context.Context, project ledger.WorkspaceID, repository, ref string) (ports.WorkSource, error)

// sessionRecords is what the ledger says of a planning session: its work order, the questions nobody has
// answered, and the requests to approve a plan it raised.
type sessionRecords struct {
	order     workorders.WorkOrder
	questions []api.OutlineQuestion
	approvals []authority.Approval
}

func (h *handler) readSession(ctx context.Context, lane ledger.LaneView) (sessionRecords, error) {
	var out sessionRecords
	err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		var err error
		if out.order, err = tx.WorkOrders().Get(lane.WorkOrder); err != nil {
			return err
		}
		open, err := unresolved(tx, ports.RecordFilter{})
		if err != nil {
			return err
		}
		for _, question := range open {
			if question.SubjectKind == workorders.SubjectKind && question.SubjectID == out.order.ID {
				out.questions = append(out.questions, api.OutlineQuestion{ID: question.ID, Text: question.Text, Blocking: question.Blocking})
			}
		}
		out.approvals, err = tx.Approvals().OfWorkOrder(out.order.ID)
		return err
	})
	return out, err
}

// laneOutline reads the plan a planning lane has committed to its branch, at the branch's tip now, the way
// intake reads a proposed plan, and compares it with the commit the lane's branch began at. What cannot be
// read is said, and nothing else is.
func (h *handler) laneOutline(w http.ResponseWriter, r *http.Request) {
	lane, err := h.workers.Lane(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	session, err := h.readSession(r.Context(), lane)
	if err != nil {
		fail(w, err)
		return
	}
	if session.order.Kind != workorders.KindPlan {
		invalid(w, "lane %s runs no planning session", lane.ID)
		return
	}
	out, unread := h.readBranch(r.Context(), lane, session)
	if unread != "" {
		reply(w, http.StatusOK, api.PlanOutline{Unread: unread})
		return
	}
	out.Questions = session.questions
	out.Approval = standingRequest(session.approvals, out.Commit)
	if out.Approval == nil {
		for _, question := range session.questions {
			if question.Blocking {
				out.NotSubmitted = "A blocking question is open, so the session has not submitted this version for approval."
				break
			}
		}
	}
	reply(w, http.StatusOK, out)
}

// standingRequest finds the request to approve a plan that stands for one commit of the branch.
func standingRequest(approvals []authority.Approval, commit string) *api.OutlineApproval {
	var found *authority.Approval
	for i := range approvals {
		if approvals[i].Kind != authority.KindPlan || api.PlanDetailOf(approvals[i]).Source != commit {
			continue
		}
		if found == nil || betterApproval(approvals[i], *found) {
			found = &approvals[i]
		}
	}
	if found == nil {
		return nil
	}
	return &api.OutlineApproval{ID: found.ID, State: found.State, Version: found.PlanVersion}
}

// readBranch reads the lane's branch. The branch is the one the work order keeps, or else the one the lane
// was given, which a lane's profile names by a prefix and the lane's ID. A second answer says why the
// branch could not be read.
func (h *handler) readBranch(ctx context.Context, lane ledger.LaneView, session sessionRecords) (api.PlanOutline, string) {
	order := session.order
	switch {
	case h.Branches == nil || h.Starts == nil:
		return api.PlanOutline{}, "this control plane reads no repository's history"
	case order.RepositoryID == "":
		return api.PlanOutline{}, "the work order names no repository to read"
	case lane.BaseCommit == "":
		return api.PlanOutline{}, "the lane recorded no commit its branch began at"
	}
	branch := cmp.Or(order.Branch, h.LaneBranchPrefix+lane.ID)
	source, err := h.Branches(ctx, h.Scope, order.RepositoryID, branch)
	if err != nil {
		return api.PlanOutline{}, err.Error()
	}
	batch, err := source.Poll(ctx, "")
	if err != nil {
		return api.PlanOutline{}, fmt.Sprintf("branch %s could not be read: %v", branch, err)
	}
	tip := string(batch.Cursor)
	path, err := h.Starts.Checkout(ctx, h.Scope, order.RepositoryID)
	if err != nil {
		return api.PlanOutline{}, err.Error()
	}
	files, err := h.Starts.Bases.Changes(ctx, path, lane.BaseCommit, tip)
	if err != nil {
		return api.PlanOutline{}, fmt.Sprintf("git could not read branch %s from %s to %s: %v", branch, lane.BaseCommit, tip, err)
	}
	return outlineOf(batch, files), ""
}

// outlineOf lays out what a poll of the branch found: the documents that changed or are new, with the
// plan's milestones, contracts, and draft decisions as the files hold them.
func outlineOf(batch ports.ChangeBatch, files []planning.FileChange) api.PlanOutline {
	changed := map[string]string{}
	for _, file := range files {
		switch file.Status {
		case planning.ChangeAdded:
			changed[file.Path] = "new"
		case planning.ChangeChanged, planning.ChangeMoved:
			changed[file.Path] = "changed"
		}
	}
	out := api.PlanOutline{Commit: string(batch.Cursor)}
	note := func(path, kind, title string) {
		if status, ok := changed[path]; ok {
			out.Documents = append(out.Documents, api.OutlineDocument{Path: path, Kind: kind, Title: title, Status: status})
		}
	}
	for _, record := range batch.Records {
		switch {
		case record.Goal != nil:
			note(record.Path, string(planning.FileGoal), record.Goal.Title)
		case record.Plan != nil:
			note(record.Path, string(planning.FilePlan), record.Plan.Title)
			out.Decisions = append(out.Decisions, record.Plan.Decisions...)
		default:
			if head, document := record.Head(); document {
				note(record.Path, string(head.Kind), head.Title)
			}
		}
		if milestone := record.File.Milestone; milestone != nil {
			out.Milestones = append(out.Milestones, api.OutlineMilestone{ID: milestone.ID, Title: milestone.Title, Priority: milestone.Priority})
		}
	}
	for _, item := range batch.Items {
		note(item.Path, string(planning.FileContract), item.Contract.Title)
		out.Contracts = append(out.Contracts, api.OutlineContract{
			ID: item.Contract.ID, Title: item.Contract.Title, Criteria: len(item.Contract.Criteria), WorkOrders: len(item.Contract.WorkOrders),
		})
	}
	slices.SortStableFunc(out.Milestones, func(a, b api.OutlineMilestone) int { return cmp.Compare(a.Priority, b.Priority) })
	slices.SortStableFunc(out.Documents, func(a, b api.OutlineDocument) int { return cmp.Compare(a.Path, b.Path) })
	return out
}
