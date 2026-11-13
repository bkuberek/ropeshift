package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const approvalColumns = `id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary,
       requested_by, state, decided_by, decided_kind, decided_digest, decided_at, reason, policy_version, raised_at, expires_at,
       channel, used_by, plan_id, plan_version, detail`

var (
	stmtInsertApproval = define("approval.insert",
		`INSERT INTO approval_requests
  (workspace_id, id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary,
   requested_by, state, reason, policy_version, raised_at, expires_at, channel, used_by, plan_id, plan_version, detail)
VALUES
  (:workspace_id, :id, :lane_id, :work_order_id, :generation, :kind, :subject, :subject_digest, :input, :command, :path, :summary,
   :requested_by, :state, :reason, :policy_version, :raised_at, :expires_at, :channel, :used_by, :plan_id, :plan_version, :detail)
ON CONFLICT (workspace_id, id) DO NOTHING`)

	stmtApproval = define("approval.get",
		`SELECT `+approvalColumns+` FROM approval_requests WHERE workspace_id = :workspace_id AND id = :id`)

	stmtApprovalsOfLane = define("approval.of_lane",
		`SELECT `+approvalColumns+` FROM approval_requests WHERE workspace_id = :workspace_id AND lane_id = :lane_id ORDER BY raised_at, id`)

	stmtApprovalsOfWork = define("approval.of_work_order",
		`SELECT `+approvalColumns+` FROM approval_requests WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id ORDER BY raised_at, id`)

	stmtApprovalsAll = define("approval.all",
		`SELECT `+approvalColumns+` FROM approval_requests WHERE workspace_id = :workspace_id ORDER BY raised_at, id`)

	stmtApprovalsDue = define("approval.due",
		`SELECT id FROM approval_requests
 WHERE workspace_id = :workspace_id AND state IN ('pending', 'approved') AND expires_at <> 0 AND expires_at <= :now
 ORDER BY id`)

	stmtLinkApprovalPlan = define("approval.link_plan",
		`UPDATE approval_requests SET plan_id = :plan_id, plan_version = :plan_version
 WHERE workspace_id = :workspace_id AND id = :id AND kind = 'plan' AND plan_id IS NULL`)

	stmtSaveApproval = define("approval.save",
		`UPDATE approval_requests
   SET state = :state, decided_by = :decided_by, decided_kind = :decided_kind, decided_digest = :decided_digest,
       decided_at = :decided_at, reason = :reason, policy_version = :policy_version, channel = :channel, used_by = :used_by
 WHERE workspace_id = :workspace_id AND id = :id AND state = :from_state`)
)

// approvalTx is the approval area of one transaction.
type approvalTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t approvalTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanApproval(row scanner) (authority.Approval, error) {
	var a authority.Approval
	var state string
	var lane, workOrder, decidedBy, decidedKind, decidedDigest, plan sql.NullString
	var generation, decidedAt, planVersion sql.NullInt64
	err := row.Scan(&a.ID, &lane, &workOrder, &generation, &a.Kind, &a.Subject, &a.SubjectDigest, &a.Input, &a.Command, &a.Path,
		&a.Summary, &a.RequestedBy, &state, &decidedBy, &decidedKind, &decidedDigest, &decidedAt, &a.Reason, &a.PolicyVersion,
		&a.RaisedAt, &a.ExpiresAt, &a.Channel, &a.UsedBy, &plan, &planVersion, &a.Detail)
	a.Lane, a.WorkOrder, a.Generation = lane.String, workOrder.String, generation.Int64
	a.Plan, a.PlanVersion = plan.String, planVersion.Int64
	a.State, a.DecidedBy, a.DecidedKind = authority.ApprovalState(state), decidedBy.String, decidedKind.String
	a.DecidedDigest, a.DecidedAt = decidedDigest.String, decidedAt.Int64
	return a, err
}

func (t approvalTx) Approval(id string) (authority.Approval, error) {
	row, err := t.row(stmtApproval, t.scoped(params{"id": id}))
	if err != nil {
		return authority.Approval{}, err
	}
	approval, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.Approval{}, fmt.Errorf("approval %s: %w", id, ports.ErrNotFound)
	}
	return approval, err
}

func (t approvalTx) list(st *statement, p params) ([]authority.Approval, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authority.Approval
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, approval)
	}
	return out, rows.Err()
}

func (t approvalTx) OfLane(lane string) ([]authority.Approval, error) {
	return t.list(stmtApprovalsOfLane, params{"lane_id": lane})
}

func (t approvalTx) OfWorkOrder(workOrder string) ([]authority.Approval, error) {
	return t.list(stmtApprovalsOfWork, params{"work_order_id": workOrder})
}

func (t approvalTx) All() ([]authority.Approval, error) {
	return t.list(stmtApprovalsAll, params{})
}

func (t approvalTx) Due(now int64) ([]string, error) {
	rows, err := t.rows(stmtApprovalsDue, t.scoped(params{"now": now}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (t approvalTx) Insert(a authority.Approval) (bool, error) {
	// A plan approval belongs to a plan and a card to the operation it runs, not to a lane, and the table
	// holds no empty lane for any other kind, so the generation is empty only for those kinds.
	var generation sql.NullInt64
	if a.Kind != authority.KindPlan && a.Kind != authority.KindConfirm {
		generation = sql.NullInt64{Int64: a.Generation, Valid: true}
	}
	planVersion := sql.NullInt64{Int64: a.PlanVersion, Valid: a.PlanVersion != 0}
	n, err := t.exec(stmtInsertApproval, t.scoped(params{
		"id": a.ID, "lane_id": nullText(a.Lane), "work_order_id": nullText(a.WorkOrder), "generation": generation, "kind": a.Kind, "subject": a.Subject,
		"subject_digest": a.SubjectDigest, "input": a.Input, "command": a.Command, "path": a.Path, "summary": a.Summary,
		"requested_by": a.RequestedBy, "state": string(a.State), "reason": a.Reason, "policy_version": a.PolicyVersion,
		"raised_at": a.RaisedAt, "expires_at": a.ExpiresAt, "channel": a.Channel, "used_by": a.UsedBy,
		"plan_id": nullText(a.Plan), "plan_version": planVersion, "detail": a.Detail,
	}))
	return n > 0, err
}

func (t approvalTx) Save(a authority.Approval, from authority.ApprovalState) (bool, error) {
	decidedAt := sql.NullInt64{Int64: a.DecidedAt, Valid: a.DecidedAt != 0}
	n, err := t.exec(stmtSaveApproval, t.scoped(params{
		"id": a.ID, "state": string(a.State), "from_state": string(from), "decided_by": nullText(a.DecidedBy), "decided_kind": nullText(a.DecidedKind),
		"decided_digest": nullText(a.DecidedDigest), "decided_at": decidedAt, "reason": a.Reason, "policy_version": a.PolicyVersion,
		"channel": a.Channel, "used_by": a.UsedBy,
	}))
	return n > 0, err
}

func (t approvalTx) LinkPlan(id, plan string, version int64) (bool, error) {
	n, err := t.exec(stmtLinkApprovalPlan, t.scoped(params{"id": id, "plan_id": plan, "plan_version": version}))
	return n > 0, err
}
