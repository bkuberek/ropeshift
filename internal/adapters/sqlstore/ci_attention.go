package sqlstore

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	stmtCIWaits = define("ci.waits",
		`SELECT work_order_id, head_commit, extended_ms, reruns, auto_reruns, rerun_at FROM ci_waits
 WHERE workspace_id = :workspace_id ORDER BY work_order_id, head_commit`)

	stmtCIWait = define("ci.wait_of",
		`SELECT extended_ms, reruns, auto_reruns, rerun_at FROM ci_waits
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND head_commit = :head_commit`)

	stmtCIExtend = define("ci.wait_extend",
		`INSERT INTO ci_waits (workspace_id, work_order_id, head_commit, extended_ms, reruns)
VALUES (:workspace_id, :work_order_id, :head_commit, :extended_ms, 0)
ON CONFLICT (workspace_id, work_order_id, head_commit) DO UPDATE SET extended_ms = ci_waits.extended_ms + :extended_ms`)

	// An automatic rerun counts in both columns, and a member's in reruns alone.
	stmtCIRerun = define("ci.rerun_count",
		`INSERT INTO ci_waits (workspace_id, work_order_id, head_commit, extended_ms, reruns, auto_reruns, rerun_at)
VALUES (:workspace_id, :work_order_id, :head_commit, 0, 1, :auto, :rerun_at)
ON CONFLICT (workspace_id, work_order_id, head_commit) DO UPDATE
  SET reruns = ci_waits.reruns + 1, auto_reruns = ci_waits.auto_reruns + :auto, extended_ms = 0, rerun_at = :rerun_at`)

	stmtCIFindings = define("ci.findings",
		`SELECT work_order_id, head_commit, kind, check_name, conclusion, url, base_commit, raised_at FROM ci_findings
 WHERE workspace_id = :workspace_id ORDER BY raised_at, work_order_id, head_commit, kind`)

	stmtCIRaise = define("ci.finding_raise",
		`INSERT INTO ci_findings (workspace_id, work_order_id, head_commit, kind, check_name, conclusion, url, base_commit, raised_at)
VALUES (:workspace_id, :work_order_id, :head_commit, :kind, :check_name, :conclusion, :url, :base_commit, :raised_at)
ON CONFLICT DO NOTHING`)

	stmtCIClear = define("ci.findings_clear",
		`DELETE FROM ci_findings WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND head_commit = :head_commit`)

	stmtCIClearKind = define("ci.finding_clear",
		`DELETE FROM ci_findings WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND head_commit = :head_commit AND kind = :kind`)
)

// ciTx is what members did about CI that waits for them, and the findings of its classification, in one transaction.
type ciTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t ciTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func (t *readTx) CI() ports.CIAttentionReader { return ciTx{session: t.session, scope: t.scope} }

func (t *writeTx) CI() ports.CIAttentionWriter { return ciTx{session: t.session, scope: t.scope} }

func (t ciTx) CIWaits() ([]ledger.CIWaitRecord, error) {
	rows, err := t.rows(stmtCIWaits, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.CIWaitRecord
	for rows.Next() {
		var r ledger.CIWaitRecord
		if err := rows.Scan(&r.WorkOrder, &r.Head, &r.ExtendedMs, &r.Reruns, &r.AutoReruns, &r.RerunAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t ciTx) CIFindings() ([]ledger.CIFinding, error) {
	rows, err := t.rows(stmtCIFindings, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.CIFinding
	for rows.Next() {
		var f ledger.CIFinding
		if err := rows.Scan(&f.WorkOrder, &f.Head, &f.Kind, &f.Check, &f.Conclusion, &f.URL, &f.BaseCommit, &f.RaisedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// waiting reads what a head holds, which is nothing for a head no one acted on.
func (t ciTx) waiting(workOrder, head string) (ledger.CIWaiting, error) {
	row, err := t.row(stmtCIWait, t.scoped(params{"work_order_id": workOrder, "head_commit": head}))
	if err != nil {
		return ledger.CIWaiting{}, err
	}
	var w ledger.CIWaiting
	return w, row.Scan(&w.ExtendedMs, &w.Reruns, &w.AutoReruns, &w.RerunAt)
}

func (t ciTx) ExtendCIWait(workOrder, head string, ms int64) (ledger.CIWaiting, error) {
	if _, err := t.exec(stmtCIExtend, t.scoped(params{"work_order_id": workOrder, "head_commit": head, "extended_ms": ms})); err != nil {
		return ledger.CIWaiting{}, err
	}
	return t.waiting(workOrder, head)
}

func (t ciTx) CountCIRerun(workOrder, head string, at int64, auto bool) (ledger.CIWaiting, error) {
	counted := int64(0)
	if auto {
		counted = 1
	}
	if _, err := t.exec(stmtCIRerun, t.scoped(params{"work_order_id": workOrder, "head_commit": head, "rerun_at": at, "auto": counted})); err != nil {
		return ledger.CIWaiting{}, err
	}
	return t.waiting(workOrder, head)
}

func (t ciTx) RaiseCIFinding(f ledger.CIFinding) (bool, error) {
	n, err := t.exec(stmtCIRaise, t.scoped(params{
		"work_order_id": f.WorkOrder, "head_commit": f.Head, "kind": f.Kind, "check_name": f.Check, "conclusion": f.Conclusion,
		"url": f.URL, "base_commit": f.BaseCommit, "raised_at": f.RaisedAt,
	}))
	return n > 0, err
}

func (t ciTx) ClearCIFindings(workOrder, head string) (int64, error) {
	return t.exec(stmtCIClear, t.scoped(params{"work_order_id": workOrder, "head_commit": head}))
}

func (t ciTx) ClearCIFinding(workOrder, head, kind string) (int64, error) {
	return t.exec(stmtCIClearKind, t.scoped(params{"work_order_id": workOrder, "head_commit": head, "kind": kind}))
}
