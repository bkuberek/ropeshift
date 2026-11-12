package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const forgeRequestColumns = `id, kind, work_order_id, repository_id, worker_id, binding, branch, base, head_commit, number, title, body,
       method, attempt, state, outcome, detail, requested_at, reported_at`

const integrationColumns = `id, work_order_id, pull_request, approved_commit, merge_authority, approved_by_kind, approved_by_id,
       approved_at, merged_commit, merged_at`

const pullRequestColumns = `work_order_id, repository_id, worker_id, number, url, state, base, branch, head_commit, mergeable,
       merge_commit, merged_at, checks, read_at`

var (
	stmtInsertForgeRequest = define("forge.request_insert",
		`INSERT INTO forge_requests
  (workspace_id, id, kind, work_order_id, repository_id, worker_id, binding, branch, base, head_commit, number, title, body,
   method, attempt, state, detail, requested_at)
VALUES
  (:workspace_id, :id, :kind, :work_order_id, :repository_id, :worker_id, :binding, :branch, :base, :head_commit, :number, :title, :body,
   :method, 1, 'requested', '', :requested_at)
ON CONFLICT DO NOTHING`)

	stmtForgeRequest = define("forge.request_get",
		`SELECT `+forgeRequestColumns+` FROM forge_requests WHERE workspace_id = :workspace_id AND id = :id`)

	stmtForgeRequestsFor = define("forge.requests_for",
		`SELECT `+forgeRequestColumns+` FROM forge_requests
 WHERE workspace_id = :workspace_id AND worker_id = :worker_id AND state = 'requested'
 ORDER BY requested_at, id`)

	stmtForgeRequestsOf = define("forge.requests_of",
		`SELECT `+forgeRequestColumns+` FROM forge_requests
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY requested_at, id`)

	stmtFailForgeRequest = define("forge.request_fail",
		`UPDATE forge_requests SET attempt = attempt + 1, detail = :detail
 WHERE workspace_id = :workspace_id AND id = :id AND attempt = :attempt AND state = 'requested'`)

	stmtFinishForgeRequest = define("forge.request_finish",
		`UPDATE forge_requests SET state = 'done', detail = '', reported_at = :reported_at
 WHERE workspace_id = :workspace_id AND id = :id AND attempt = :attempt AND state = 'requested'`)

	stmtFinishMerge = define("forge.request_finish_merge",
		`UPDATE forge_requests SET state = 'done', outcome = :outcome, detail = :detail, reported_at = :reported_at
 WHERE workspace_id = :workspace_id AND id = :id AND attempt = :attempt AND state = 'requested'`)

	stmtInsertIntegration = define("forge.integration_insert",
		`INSERT INTO integration_records
  (workspace_id, id, work_order_id, pull_request, approved_commit, merge_authority, approved_by_kind, approved_by_id, approved_at,
   merged_commit, merged_at)
VALUES
  (:workspace_id, :id, :work_order_id, :pull_request, :approved_commit, :merge_authority, :approved_by_kind, :approved_by_id,
   :approved_at, :merged_commit, :merged_at)
ON CONFLICT DO NOTHING`)

	stmtIntegration = define("forge.integration_get",
		`SELECT `+integrationColumns+` FROM integration_records WHERE workspace_id = :workspace_id AND id = :id`)

	stmtIntegrations = define("forge.integrations_of",
		`SELECT `+integrationColumns+` FROM integration_records
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY approved_at, id`)

	stmtRecordMerge = define("forge.integration_merged",
		`UPDATE integration_records SET merged_commit = :merged_commit, merged_at = :merged_at
 WHERE workspace_id = :workspace_id AND id = :id AND merged_at = 0`)

	stmtSavePullRequest = define("forge.pull_request_save",
		`INSERT INTO pull_requests
  (workspace_id, work_order_id, repository_id, worker_id, number, url, state, base, branch, head_commit, mergeable, merge_commit,
   merged_at, checks, read_at)
VALUES
  (:workspace_id, :work_order_id, :repository_id, :worker_id, :number, :url, :state, :base, :branch, :head_commit, :mergeable,
   :merge_commit, :merged_at, :checks, :read_at)
ON CONFLICT (workspace_id, work_order_id) DO UPDATE SET
  repository_id = :repository_id, worker_id = :worker_id, number = :number, url = :url, state = :state, base = :base,
  branch = :branch, head_commit = :head_commit, mergeable = :mergeable, merge_commit = :merge_commit, merged_at = :merged_at,
  checks = :checks, read_at = :read_at`)

	stmtPullRequest = define("forge.pull_request_get",
		`SELECT `+pullRequestColumns+` FROM pull_requests WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id`)

	stmtPullRequests = define("forge.pull_requests",
		`SELECT `+pullRequestColumns+` FROM pull_requests WHERE workspace_id = :workspace_id ORDER BY work_order_id`)

	stmtForgeHealthOf = define("forge.health_of",
		`SELECT state, detail FROM forge_health
 WHERE workspace_id = :workspace_id AND worker_id = :worker_id AND repository_id = :repository_id`)

	stmtSaveForgeHealth = define("forge.health_save",
		`INSERT INTO forge_health (workspace_id, worker_id, repository_id, state, detail, checked_at)
VALUES (:workspace_id, :worker_id, :repository_id, :state, :detail, :checked_at)
ON CONFLICT (workspace_id, worker_id, repository_id) DO UPDATE SET state = :state, detail = :detail, checked_at = :checked_at`)

	stmtForgeHealth = define("forge.health",
		`SELECT worker_id, repository_id, state, detail, checked_at FROM forge_health
 WHERE workspace_id = :workspace_id ORDER BY repository_id, worker_id`)
)

// forgeTx is the forge area of one transaction.
type forgeTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t forgeTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func (t *readTx) Forge() ports.ForgeReader { return forgeTx{session: t.session, scope: t.scope} }

func (t *writeTx) Forge() ports.ForgeWriter { return forgeTx{session: t.session, scope: t.scope} }

func scanForgeRequest(row scanner) (ledger.ForgeRequest, error) {
	var r ledger.ForgeRequest
	var kind, method, state, outcome string
	err := row.Scan(&r.ID, &kind, &r.WorkOrder, &r.Repository, &r.Worker, &r.Binding, &r.Branch, &r.Base, &r.Commit, &r.Number,
		&r.Title, &r.Body, &method, &r.Attempt, &state, &outcome, &r.Detail, &r.RequestedAt, &r.ReportedAt)
	r.Kind, r.Method, r.State, r.Outcome = ledger.ForgeKind(kind), ledger.MergeMethod(method), ledger.ForgeRequestState(state), ledger.MergeOutcome(outcome)
	return r, err
}

func (t forgeTx) Request(id string) (ledger.ForgeRequest, error) {
	row, err := t.row(stmtForgeRequest, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.ForgeRequest{}, err
	}
	request, err := scanForgeRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.ForgeRequest{}, fmt.Errorf("forge request %s: %w", id, ports.ErrNotFound)
	}
	return request, err
}

func (t forgeTx) requests(st *statement, p params) ([]ledger.ForgeRequest, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.ForgeRequest
	for rows.Next() {
		request, err := scanForgeRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, rows.Err()
}

func (t forgeTx) RequestsFor(worker string) ([]ledger.ForgeRequest, error) {
	return t.requests(stmtForgeRequestsFor, params{"worker_id": worker})
}

func (t forgeTx) RequestsOf(workOrder string) ([]ledger.ForgeRequest, error) {
	return t.requests(stmtForgeRequestsOf, params{"work_order_id": workOrder})
}

func (t forgeTx) InsertRequest(r ledger.ForgeRequest) (bool, error) {
	n, err := t.exec(stmtInsertForgeRequest, t.scoped(params{
		"id": r.ID, "kind": string(r.Kind), "work_order_id": r.WorkOrder, "repository_id": r.Repository, "worker_id": r.Worker,
		"binding": r.Binding, "branch": r.Branch, "base": r.Base, "head_commit": r.Commit, "number": r.Number, "title": r.Title,
		"body": r.Body, "method": string(r.Method), "requested_at": r.RequestedAt,
	}))
	return n > 0, err
}

func (t forgeTx) FailRequest(id string, attempt int64, detail string) (bool, error) {
	n, err := t.exec(stmtFailForgeRequest, t.scoped(params{"id": id, "attempt": attempt, "detail": detail}))
	return n > 0, err
}

func (t forgeTx) FinishRequest(id string, attempt, at int64) (bool, error) {
	n, err := t.exec(stmtFinishForgeRequest, t.scoped(params{"id": id, "attempt": attempt, "reported_at": at}))
	return n > 0, err
}

func (t forgeTx) FinishMerge(id string, attempt, at int64, outcome ledger.MergeOutcome, detail string) (bool, error) {
	n, err := t.exec(stmtFinishMerge, t.scoped(params{"id": id, "attempt": attempt, "reported_at": at, "outcome": string(outcome), "detail": detail}))
	return n > 0, err
}

func scanIntegration(row scanner) (ledger.IntegrationRecord, error) {
	var r ledger.IntegrationRecord
	var authority, kind string
	err := row.Scan(&r.ID, &r.WorkOrder, &r.PullRequest, &r.ApprovedCommit, &authority, &kind, &r.ApprovedByID, &r.ApprovedAt,
		&r.MergedCommit, &r.MergedAt)
	r.MergeAuthority, r.ApprovedByKind = ledger.MergeAuthority(authority), ledger.ApproverKind(kind)
	return r, err
}

func (t forgeTx) Integration(id string) (ledger.IntegrationRecord, error) {
	row, err := t.row(stmtIntegration, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.IntegrationRecord{}, err
	}
	record, err := scanIntegration(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.IntegrationRecord{}, fmt.Errorf("integration record %s: %w", id, ports.ErrNotFound)
	}
	return record, err
}

func (t forgeTx) Integrations(workOrder string) ([]ledger.IntegrationRecord, error) {
	rows, err := t.rows(stmtIntegrations, t.scoped(params{"work_order_id": workOrder}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.IntegrationRecord
	for rows.Next() {
		record, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (t forgeTx) InsertIntegration(r ledger.IntegrationRecord) (bool, error) {
	n, err := t.exec(stmtInsertIntegration, t.scoped(params{
		"id": r.ID, "work_order_id": r.WorkOrder, "pull_request": r.PullRequest, "approved_commit": r.ApprovedCommit,
		"merge_authority": string(r.MergeAuthority), "approved_by_kind": string(r.ApprovedByKind), "approved_by_id": r.ApprovedByID,
		"approved_at": r.ApprovedAt, "merged_commit": r.MergedCommit, "merged_at": r.MergedAt,
	}))
	return n > 0, err
}

func (t forgeTx) RecordMerge(id, commit string, at int64) (bool, error) {
	n, err := t.exec(stmtRecordMerge, t.scoped(params{"id": id, "merged_commit": commit, "merged_at": at}))
	return n > 0, err
}

func scanPullRequest(row scanner) (ledger.WorkPullRequest, error) {
	var pr ledger.WorkPullRequest
	var state, checks string
	err := row.Scan(&pr.WorkOrder, &pr.Repository, &pr.Worker, &pr.Number, &pr.URL, &state, &pr.Base, &pr.Branch, &pr.HeadCommit,
		&pr.Mergeable, &pr.MergeCommit, &pr.MergedAt, &checks, &pr.ReadAt)
	if err != nil {
		return pr, err
	}
	pr.State = ledger.PullRequestState(state)
	if err := json.Unmarshal([]byte(checks), &pr.Checks); err != nil {
		return pr, fmt.Errorf("the checks of pull request %d: %w", pr.Number, err)
	}
	return pr, nil
}

func (t forgeTx) PullRequest(workOrder string) (ledger.WorkPullRequest, error) {
	row, err := t.row(stmtPullRequest, t.scoped(params{"work_order_id": workOrder}))
	if err != nil {
		return ledger.WorkPullRequest{}, err
	}
	pr, err := scanPullRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.WorkPullRequest{}, fmt.Errorf("the pull request of %s: %w", workOrder, ports.ErrNotFound)
	}
	return pr, err
}

func (t forgeTx) PullRequests() ([]ledger.WorkPullRequest, error) {
	rows, err := t.rows(stmtPullRequests, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.WorkPullRequest
	for rows.Next() {
		pr, err := scanPullRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (t forgeTx) SavePullRequest(pr ledger.WorkPullRequest) error {
	checks := pr.Checks
	if checks == nil {
		checks = []ledger.PullRequestCheck{}
	}
	raw, err := json.Marshal(checks)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSavePullRequest, t.scoped(params{
		"work_order_id": pr.WorkOrder, "repository_id": pr.Repository, "worker_id": pr.Worker, "number": pr.Number, "url": pr.URL,
		"state": string(pr.State), "base": pr.Base, "branch": pr.Branch, "head_commit": pr.HeadCommit, "mergeable": pr.Mergeable,
		"merge_commit": pr.MergeCommit, "merged_at": pr.MergedAt, "checks": string(raw), "read_at": pr.ReadAt,
	}))
	return err
}

func (t forgeTx) SaveHealth(h ledger.ForgeHealth) (bool, error) {
	row, err := t.row(stmtForgeHealthOf, t.scoped(params{"worker_id": h.Worker, "repository_id": h.Repository}))
	if err != nil {
		return false, err
	}
	var state, detail string
	err = row.Scan(&state, &detail)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	}
	if !errors.Is(err, sql.ErrNoRows) && state == string(h.State) && detail == h.Detail {
		return false, nil
	}
	_, err = t.exec(stmtSaveForgeHealth, t.scoped(params{
		"worker_id": h.Worker, "repository_id": h.Repository, "state": string(h.State), "detail": h.Detail, "checked_at": h.CheckedAt,
	}))
	return err == nil, err
}

func (t forgeTx) Health() ([]ledger.ForgeHealth, error) {
	rows, err := t.rows(stmtForgeHealth, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.ForgeHealth
	for rows.Next() {
		var h ledger.ForgeHealth
		var state string
		if err := rows.Scan(&h.Worker, &h.Repository, &state, &h.Detail, &h.CheckedAt); err != nil {
			return nil, err
		}
		h.State = ledger.ForgeHealthState(state)
		out = append(out, h)
	}
	return out, rows.Err()
}
