package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

const evidenceColumns = `id, work_order_id, lane_id, fence, artifact_id, criterion_id, name, status, source, details_uri, created_at`

var (
	stmtEvidenceRecord = define("evidence.record",
		`INSERT INTO check_results (workspace_id, id, work_order_id, lane_id, fence, artifact_id, criterion_id, name, status, source, details_uri, created_at)
VALUES (:workspace_id, :id, :work_order_id, :lane_id, :fence, :artifact_id, :criterion_id, :name, :status, :source, :details_uri, :now)
ON CONFLICT (workspace_id, id) DO UPDATE SET status = :status, details_uri = :details_uri`)

	stmtEvidenceAttempt = define("evidence.attempt",
		`SELECT `+evidenceColumns+` FROM check_results
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND fence = :fence
 ORDER BY created_at, id`)

	stmtEvidenceChecks = define("evidence.checks",
		`SELECT `+evidenceColumns+` FROM check_results
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY created_at, id`)

	// stmtVerdictRecord leaves a second verdict for one criterion of one review alone, so the caller can
	// tell it from a new one by the rows it changed.
	stmtVerdictRecord = define("verdict.record",
		`INSERT INTO review_verdicts
  (workspace_id, review_id, work_order_id, attempt, commit_digest, lane_id, generation, fence, criterion_id, verdict, reasons, citations, max_rounds, created_at)
VALUES
  (:workspace_id, :review_id, :work_order_id, :attempt, :commit_digest, :lane_id, :generation, :fence, :criterion_id, :verdict, :reasons, :citations, :max_rounds, :now)
ON CONFLICT (workspace_id, review_id, criterion_id) DO NOTHING`)

	stmtVerdictList = define("verdict.list",
		`SELECT review_id, work_order_id, attempt, commit_digest, lane_id, generation, fence, criterion_id, verdict, reasons, citations, max_rounds, created_at
  FROM review_verdicts
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY created_at, review_id, criterion_id`)

	stmtVerdictReview = define("verdict.review",
		`SELECT kind, reviews, reviews_fence FROM work_orders WHERE workspace_id = :workspace_id AND id = :id`)

	stmtVerdictLane = define("verdict.lane",
		`SELECT work_order_id FROM lanes WHERE workspace_id = :workspace_id AND id = :id`)

	stmtReworkRecord = define("rework.record",
		`INSERT INTO rework_notes (workspace_id, work_order_id, attempt, review_id, reason, created_at)
VALUES (:workspace_id, :work_order_id, :attempt, :review_id, :reason, :now)
ON CONFLICT (workspace_id, work_order_id, attempt) DO UPDATE SET review_id = :review_id, reason = :reason`)

	stmtReworkNewest = define("rework.newest",
		`SELECT work_order_id, attempt, review_id, reason, created_at FROM rework_notes
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY attempt DESC LIMIT 1`)

	stmtCalibrationRecord = define("calibration.record",
		`INSERT INTO review_calibrations
  (workspace_id, id, work_order_id, review_id, commit_digest, repository_id, merge_authority, outcome, verdicts, decision, agreement, source, member_id, reason, created_at)
VALUES
  (:workspace_id, :id, :work_order_id, :review_id, :commit_digest, :repository_id, :merge_authority, :outcome, :verdicts, :decision, :agreement, :source, :member_id, :reason, :now)
ON CONFLICT DO NOTHING`)

	stmtCalibrationList = define("calibration.list",
		`SELECT id, work_order_id, review_id, commit_digest, repository_id, merge_authority, outcome, verdicts, decision, agreement, source, member_id, reason, created_at
  FROM review_calibrations
 WHERE workspace_id = :workspace_id
 ORDER BY created_at, id`)

	stmtEvidenceArtifacts = define("evidence.artifacts",
		`SELECT a.id, a.lane_id, a.kind, a.uri, a.digest, a.fence, a.created_at
  FROM artifacts a
  JOIN lanes l ON l.workspace_id = a.workspace_id AND l.id = a.lane_id
 WHERE a.workspace_id = :workspace_id AND l.work_order_id = :work_order_id
 ORDER BY a.created_at, a.kind, a.uri, a.id`)
)

// evidenceTx is the acceptance area of one transaction.
type evidenceTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t evidenceTx) Record(e workorders.Evidence) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtEvidenceRecord, params{
		"workspace_id": string(t.scope), "id": e.ID, "work_order_id": e.WorkOrder, "lane_id": e.Lane, "fence": e.Fence,
		"artifact_id": e.ArtifactID, "criterion_id": e.Criterion, "name": e.Name, "status": string(e.Status),
		"source": string(e.Source), "details_uri": e.DetailsURI, "now": now,
	})
	return err
}

func (t evidenceTx) Attempt(workOrder string, fence int64) ([]workorders.Evidence, error) {
	rows, err := t.rows(stmtEvidenceAttempt, params{"workspace_id": string(t.scope), "work_order_id": workOrder, "fence": fence})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func (t evidenceTx) Checks(workOrder string) ([]workorders.Evidence, error) {
	rows, err := t.rows(stmtEvidenceChecks, params{"workspace_id": string(t.scope), "work_order_id": workOrder})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func (t evidenceTx) Artifacts(workOrder string) ([]ledger.Artifact, error) {
	rows, err := t.rows(stmtEvidenceArtifacts, params{"workspace_id": string(t.scope), "work_order_id": workOrder})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func scanEvidence(rows *sql.Rows) ([]workorders.Evidence, error) {
	var out []workorders.Evidence
	for rows.Next() {
		var e workorders.Evidence
		var status, source string
		if err := rows.Scan(&e.ID, &e.WorkOrder, &e.Lane, &e.Fence, &e.ArtifactID, &e.Criterion, &e.Name, &status, &source, &e.DetailsURI, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Status, e.Source = workorders.CheckStatus(status), workorders.EvidenceSource(source)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordVerdict keeps a verdict only from a lane of the review work order that names the reviewed work. The
// reads and the insert share the caller's transaction, so a verdict cannot be judged by one state and kept
// under another.
func (t evidenceTx) RecordVerdict(v workorders.Verdict) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.Commit == "" || v.Review == "" || v.WorkOrder == "" || v.Lane == "" {
		return fmt.Errorf("%w: a verdict names its review, its work, its lane, and the commit it judged", workorders.ErrInvalid)
	}
	if err := t.mayJudge(v); err != nil {
		return err
	}
	citations, err := json.Marshal(append([]string{}, v.Citations...))
	if err != nil {
		return err
	}
	now, err := t.readClock()
	if err != nil {
		return err
	}
	n, err := t.exec(stmtVerdictRecord, params{
		"workspace_id": string(t.scope), "review_id": v.Review, "work_order_id": v.WorkOrder, "attempt": v.Attempt,
		"commit_digest": v.Commit, "lane_id": v.Lane, "generation": v.Generation, "fence": v.Fence, "criterion_id": v.Criterion,
		"verdict": string(v.Verdict), "reasons": v.Reasons, "citations": string(citations), "max_rounds": v.MaxRounds, "now": now,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: review %s already judged criterion %q", workorders.ErrInvalid, v.Review, v.Criterion)
	}
	return nil
}

// mayJudge holds a verdict to its author: the lane belongs to the review work order, the review names the
// work it judges and the attempt, and the lane is not one that did that work (FR-REVIEW-5).
func (t evidenceTx) mayJudge(v workorders.Verdict) error {
	var kind string
	var reviews sql.NullString
	var attempt sql.NullInt64
	row, err := t.row(stmtVerdictReview, params{"workspace_id": string(t.scope), "id": v.Review})
	if err != nil {
		return err
	}
	switch err := row.Scan(&kind, &reviews, &attempt); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s is no review", workorders.ErrForbidden, v.Review)
	case err != nil:
		return err
	}
	if kind != string(workorders.KindReview) || reviews.String != v.WorkOrder || attempt.Int64 != v.Attempt {
		return fmt.Errorf("%w: %s is not the review of %s at attempt %d", workorders.ErrForbidden, v.Review, v.WorkOrder, v.Attempt)
	}
	row, err = t.row(stmtVerdictLane, params{"workspace_id": string(t.scope), "id": v.Lane})
	if err != nil {
		return err
	}
	var owner string
	switch err := row.Scan(&owner); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: lane %s started nowhere", workorders.ErrForbidden, v.Lane)
	case err != nil:
		return err
	}
	switch owner {
	case v.WorkOrder:
		return fmt.Errorf("%w: lane %s did the work it would judge", workorders.ErrForbidden, v.Lane)
	case v.Review:
		return nil
	}
	return fmt.Errorf("%w: lane %s belongs to %s, not to review %s", workorders.ErrForbidden, v.Lane, owner, v.Review)
}

func (t evidenceTx) Verdicts(workOrder string) ([]workorders.Verdict, error) {
	rows, err := t.rows(stmtVerdictList, params{"workspace_id": string(t.scope), "work_order_id": workOrder})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workorders.Verdict
	for rows.Next() {
		var v workorders.Verdict
		var kind, citations string
		if err := rows.Scan(&v.Review, &v.WorkOrder, &v.Attempt, &v.Commit, &v.Lane, &v.Generation, &v.Fence, &v.Criterion, &kind, &v.Reasons, &citations, &v.MaxRounds, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Verdict = workorders.VerdictKind(kind)
		if err := json.Unmarshal([]byte(citations), &v.Citations); err != nil {
			return nil, fmt.Errorf("verdict %s of %s: %w", v.Criterion, v.Review, err)
		}
		if len(v.Citations) == 0 {
			v.Citations = nil
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t evidenceTx) RecordRework(note workorders.ReworkNote) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtReworkRecord, params{
		"workspace_id": string(t.scope), "work_order_id": note.WorkOrder, "attempt": note.Attempt, "review_id": note.Review,
		"reason": note.Reason, "now": now,
	})
	return err
}

func (t evidenceTx) Reworked(workOrder string) (workorders.ReworkNote, error) {
	row, err := t.row(stmtReworkNewest, params{"workspace_id": string(t.scope), "work_order_id": workOrder})
	if err != nil {
		return workorders.ReworkNote{}, err
	}
	var note workorders.ReworkNote
	err = row.Scan(&note.WorkOrder, &note.Attempt, &note.Review, &note.Reason, &note.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return workorders.ReworkNote{}, fmt.Errorf("work order %s never went back: %w", workOrder, ports.ErrNotFound)
	}
	return note, err
}

// RecordCalibration keeps a member's decision on reviewed work. A second record for one review, source, and
// decision leaves the first alone, because a decision is made once.
func (t evidenceTx) RecordCalibration(c workorders.Calibration) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	verdicts, err := json.Marshal(append([]workorders.SeenVerdict{}, c.Verdicts...))
	if err != nil {
		return false, err
	}
	now, err := t.readClock()
	if err != nil {
		return false, err
	}
	n, err := t.exec(stmtCalibrationRecord, params{
		"workspace_id": string(t.scope), "id": c.ID, "work_order_id": c.WorkOrder, "review_id": c.Review, "commit_digest": c.Commit,
		"repository_id": c.Repository, "merge_authority": string(c.Authority), "outcome": string(c.Outcome), "verdicts": string(verdicts),
		"decision": string(c.Decision), "agreement": string(c.Agreement), "source": string(c.Source), "member_id": c.Member,
		"reason": c.Reason, "now": now,
	})
	return n > 0, err
}

func (t evidenceTx) Calibrations() ([]workorders.Calibration, error) {
	rows, err := t.rows(stmtCalibrationList, params{"workspace_id": string(t.scope)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workorders.Calibration
	for rows.Next() {
		var c workorders.Calibration
		var authority, outcome, decision, agreement, source, verdicts string
		if err := rows.Scan(&c.ID, &c.WorkOrder, &c.Review, &c.Commit, &c.Repository, &authority, &outcome, &verdicts, &decision, &agreement, &source, &c.Member, &c.Reason, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Authority, c.Outcome, c.Decision = ledger.MergeAuthority(authority), workorders.ReviewOutcome(outcome), workorders.CalibrationDecision(decision)
		c.Agreement, c.Source = workorders.Agreement(agreement), workorders.CalibrationSource(source)
		if err := json.Unmarshal([]byte(verdicts), &c.Verdicts); err != nil {
			return nil, fmt.Errorf("calibration %s: %w", c.ID, err)
		}
		if len(c.Verdicts) == 0 {
			c.Verdicts = nil
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
