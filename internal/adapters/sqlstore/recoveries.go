package sqlstore

import (
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	stmtRecordRecovery = define("recovery.record",
		`INSERT INTO recovery_acts
  (workspace_id, id, act, work_order_id, lane_id, actor, generation, reason, policy_version, result, check_failed, approval_id, recorded_at)
VALUES
  (:workspace_id, :id, :act, :work_order_id, :lane_id, :actor, :generation, :reason, :policy_version, :result, :check_failed, :approval_id, :recorded_at)`)

	stmtCountRecoveries = define("recovery.count",
		`SELECT count(*) FROM recovery_acts
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND act = :act AND result = 'done'
   AND actor LIKE :actor_kind AND recorded_at > :since`)
)

// recoveryTx is the recovery area of one transaction.
type recoveryTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t *writeTx) Recoveries() ports.RecoveryWriter {
	return recoveryTx{session: t.session, scope: t.scope}
}

func (t recoveryTx) Record(r authority.RecoveryRecord) error {
	_, err := t.exec(stmtRecordRecovery, params{
		"workspace_id": string(t.scope), "id": r.ID, "act": string(r.Act), "work_order_id": r.WorkOrder, "lane_id": r.Lane,
		"actor": r.Actor, "generation": r.Generation, "reason": r.Reason, "policy_version": r.PolicyVersion, "result": r.Result,
		"check_failed": r.Check, "approval_id": r.Approval, "recorded_at": r.At,
	})
	return err
}

func (t recoveryTx) Done(workOrder string, act authority.RecoveryAct, by ledger.ActorKind, since int64) (int64, error) {
	row, err := t.row(stmtCountRecoveries, params{
		"workspace_id": string(t.scope), "work_order_id": workOrder, "act": string(act), "actor_kind": string(by) + ":%", "since": since,
	})
	if err != nil {
		return 0, err
	}
	var n int64
	err = row.Scan(&n)
	return n, err
}
