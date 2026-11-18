package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const practicesRequestColumns = `id, repository_id, worker, cause, requested_by, requested_at, expires_at, state, reported_at`

const practicesResultColumns = `repository_id, request_id, commit_digest, result, checked_at`

var (
	stmtInsertPracticesRequest = define("practices.request_insert",
		`INSERT INTO practices_requests (workspace_id, id, repository_id, worker, cause, requested_by, requested_at, expires_at, state)
VALUES (:workspace_id, :id, :repository_id, :worker, :cause, :requested_by, :requested_at, :expires_at, :state)`)

	stmtPracticesRequest = define("practices.request_get",
		`SELECT `+practicesRequestColumns+` FROM practices_requests WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOpenPracticesRequest = define("practices.request_open",
		`SELECT `+practicesRequestColumns+` FROM practices_requests
 WHERE workspace_id = :workspace_id AND repository_id = :repository_id AND state = :offered AND expires_at > :now
 ORDER BY requested_at DESC, id DESC
 LIMIT 1`)

	stmtOfferedPracticesRequests = define("practices.requests_offered",
		`SELECT `+practicesRequestColumns+` FROM practices_requests
 WHERE workspace_id = :workspace_id AND worker = :worker AND state = :offered
 ORDER BY requested_at, id`)

	// stmtSettlePracticesRequest changes a request only while no report settled it, so the first report is the one
	// kept. A request the tick stored as expired is still settled, because a late report is still recorded.
	stmtSettlePracticesRequest = define("practices.request_settle",
		`UPDATE practices_requests SET state = :reported, reported_at = :reported_at
 WHERE workspace_id = :workspace_id AND id = :id AND state IN (:offered, :expired)`)

	stmtDuePracticesRequests = define("practices.requests_due",
		`SELECT `+practicesRequestColumns+` FROM practices_requests
 WHERE workspace_id = :workspace_id AND state = :offered AND expires_at <= :now
 ORDER BY requested_at, id`)

	stmtExpirePracticesRequest = define("practices.request_expire",
		`UPDATE practices_requests SET state = :expired
 WHERE workspace_id = :workspace_id AND id = :id AND state = :offered`)

	stmtSavePracticesResult = define("practices.result_save",
		`INSERT INTO practices_results (workspace_id, repository_id, request_id, commit_digest, result, checked_at)
VALUES (:workspace_id, :repository_id, :request_id, :commit_digest, :result, :checked_at)
ON CONFLICT (workspace_id, repository_id) DO UPDATE SET
  request_id = :request_id, commit_digest = :commit_digest, result = :result, checked_at = :checked_at`)

	stmtPracticesResult = define("practices.result_get",
		`SELECT `+practicesResultColumns+` FROM practices_results WHERE workspace_id = :workspace_id AND repository_id = :repository_id`)

	stmtPracticesResults = define("practices.results",
		`SELECT `+practicesResultColumns+` FROM practices_results WHERE workspace_id = :workspace_id ORDER BY repository_id`)

	stmtSavePracticesScan = define("practices.scan_save",
		`INSERT INTO practices_scans (workspace_id, repository_id, scanned_tip, rules_version)
VALUES (:workspace_id, :repository_id, :scanned_tip, :rules_version)
ON CONFLICT (workspace_id, repository_id) DO UPDATE SET scanned_tip = :scanned_tip, rules_version = :rules_version`)

	stmtClearPracticesScan = define("practices.scan_clear",
		`DELETE FROM practices_scans WHERE workspace_id = :workspace_id AND repository_id = :repository_id`)

	stmtPracticesScan = define("practices.scan_get",
		`SELECT scanned_tip, rules_version FROM practices_scans WHERE workspace_id = :workspace_id AND repository_id = :repository_id`)
)

// practicesTx is the practices area of one transaction.
type practicesTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t practicesTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func (t *readTx) Practices() ports.PracticesReader {
	return practicesTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Practices() ports.PracticesWriter {
	return practicesTx{session: t.session, scope: t.scope}
}

func scanPracticesRequest(row scanner) (ledger.PracticesRequest, error) {
	var r ledger.PracticesRequest
	var cause, state string
	err := row.Scan(&r.ID, &r.Repository, &r.Worker, &cause, &r.RequestedBy, &r.RequestedAt, &r.ExpiresAt, &state, &r.ReportedAt)
	r.Cause, r.State = ledger.PracticesCause(cause), ledger.PracticesState(state)
	return r, err
}

func (t practicesTx) requests(st *statement, p params) ([]ledger.PracticesRequest, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.PracticesRequest
	for rows.Next() {
		request, err := scanPracticesRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, rows.Err()
}

func (t practicesTx) Request(id string) (ledger.PracticesRequest, error) {
	row, err := t.row(stmtPracticesRequest, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.PracticesRequest{}, err
	}
	request, err := scanPracticesRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.PracticesRequest{}, fmt.Errorf("practices request %s: %w", id, ports.ErrNotFound)
	}
	return request, err
}

func (t practicesTx) Open(repository string, now int64) (ledger.PracticesRequest, error) {
	row, err := t.row(stmtOpenPracticesRequest, t.scoped(params{
		"repository_id": repository, "offered": string(ledger.PracticesOffered), "now": now,
	}))
	if err != nil {
		return ledger.PracticesRequest{}, err
	}
	request, err := scanPracticesRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.PracticesRequest{}, fmt.Errorf("no open practices request of repository %s: %w", repository, ports.ErrNotFound)
	}
	return request, err
}

func (t practicesTx) Offered(worker string) ([]ledger.PracticesRequest, error) {
	return t.requests(stmtOfferedPracticesRequests, params{"worker": worker, "offered": string(ledger.PracticesOffered)})
}

func (t practicesTx) Insert(r ledger.PracticesRequest) error {
	_, err := t.exec(stmtInsertPracticesRequest, t.scoped(params{
		"id": r.ID, "repository_id": r.Repository, "worker": r.Worker, "cause": string(r.Cause), "requested_by": r.RequestedBy,
		"requested_at": r.RequestedAt, "expires_at": r.ExpiresAt, "state": string(ledger.PracticesOffered),
	}))
	return err
}

func (t practicesTx) Settle(id string, reportedAt int64) (bool, error) {
	n, err := t.exec(stmtSettlePracticesRequest, t.scoped(params{
		"id": id, "reported_at": reportedAt, "reported": string(ledger.PracticesReported),
		"offered": string(ledger.PracticesOffered), "expired": string(ledger.PracticesExpired),
	}))
	return n > 0, err
}

func (t practicesTx) Expire(now int64) ([]ledger.PracticesRequest, error) {
	due, err := t.requests(stmtDuePracticesRequests, params{"offered": string(ledger.PracticesOffered), "now": now})
	if err != nil {
		return nil, err
	}
	for _, request := range due {
		if _, err := t.exec(stmtExpirePracticesRequest, t.scoped(params{
			"id": request.ID, "offered": string(ledger.PracticesOffered), "expired": string(ledger.PracticesExpired),
		})); err != nil {
			return nil, err
		}
	}
	return due, nil
}

func (t practicesTx) SaveResult(record ledger.PracticesRecord) error {
	body, err := json.Marshal(record.Result)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSavePracticesResult, t.scoped(params{
		"repository_id": record.Repository, "request_id": record.Request, "commit_digest": record.Commit,
		"result": string(body), "checked_at": record.CheckedAt,
	}))
	return err
}

func scanPracticesRecord(row scanner) (ledger.PracticesRecord, error) {
	var record ledger.PracticesRecord
	var body string
	if err := row.Scan(&record.Repository, &record.Request, &record.Commit, &body, &record.CheckedAt); err != nil {
		return ledger.PracticesRecord{}, err
	}
	if err := json.Unmarshal([]byte(body), &record.Result); err != nil {
		return ledger.PracticesRecord{}, fmt.Errorf("practices result of repository %s: %w", record.Repository, err)
	}
	return record, nil
}

func (t practicesTx) Result(repository string) (ledger.PracticesRecord, error) {
	row, err := t.row(stmtPracticesResult, t.scoped(params{"repository_id": repository}))
	if err != nil {
		return ledger.PracticesRecord{}, err
	}
	record, err := scanPracticesRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.PracticesRecord{}, fmt.Errorf("practices result of repository %s: %w", repository, ports.ErrNotFound)
	}
	return record, err
}

func (t practicesTx) Results() ([]ledger.PracticesRecord, error) {
	rows, err := t.rows(stmtPracticesResults, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.PracticesRecord
	for rows.Next() {
		record, err := scanPracticesRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (t practicesTx) SaveScan(repository string, scan ledger.PracticesScan) error {
	if scan.Tip == "" {
		_, err := t.exec(stmtClearPracticesScan, t.scoped(params{"repository_id": repository}))
		return err
	}
	_, err := t.exec(stmtSavePracticesScan, t.scoped(params{"repository_id": repository, "scanned_tip": scan.Tip, "rules_version": int64(scan.RulesVersion)}))
	return err
}

func (t practicesTx) Scan(repository string) (ledger.PracticesScan, error) {
	row, err := t.row(stmtPracticesScan, t.scoped(params{"repository_id": repository}))
	if err != nil {
		return ledger.PracticesScan{}, err
	}
	var scan ledger.PracticesScan
	var version int64
	if err := row.Scan(&scan.Tip, &version); errors.Is(err, sql.ErrNoRows) {
		return ledger.PracticesScan{}, nil
	} else if err != nil {
		return ledger.PracticesScan{}, err
	}
	scan.RulesVersion = int(version)
	return scan, nil
}
