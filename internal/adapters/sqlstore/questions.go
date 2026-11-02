package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

const (
	questionColumns = `id, text, normalized_hash, categories, status, rung, blocking, subject_kind, subject_id,
       batch_id, asked_by, options, recommendation, escalation_reason, ladder_trace,
       claimed_by, claim_until, decision_id`
	batchColumns    = `b.id, b.status, b.opened_at, b.closes_at, b.delivered_at`
	decisionColumns = `id, status, statement, rationale, citations, scope, reversibility, categories,
       decided_by, supersedes, question_id, policy_version, overlapping, no_conflict`

	// questionFilter and decisionFilter read an empty parameter as no filter. Each
	// parameter is cast, because Postgres cannot type one that appears twice.
	questionFilter = `
   AND (CAST(:status AS TEXT) = '' OR q.status = CAST(:status AS TEXT))
   AND (CAST(:scope AS TEXT) = '' OR q.subject_id = CAST(:scope AS TEXT))
   AND (CAST(:category AS TEXT) = '' OR q.categories LIKE CAST(:category AS TEXT))
   AND (CAST(:after AS TEXT) = '' OR q.id > CAST(:after AS TEXT))`
	decisionFilter = `
   AND (CAST(:status AS TEXT) = '' OR d.status = CAST(:status AS TEXT))
   AND (CAST(:scope AS TEXT) = '' OR d.scope = CAST(:scope AS TEXT))
   AND (CAST(:category AS TEXT) = '' OR d.categories LIKE CAST(:category AS TEXT))
   AND (CAST(:after AS TEXT) = '' OR d.id > CAST(:after AS TEXT))`
)

var (
	stmtInsertQuestion = define("question.insert",
		`INSERT INTO questions
  (workspace_id, id, text, normalized_hash, categories, status, rung, blocking, subject_kind, subject_id,
   batch_id, asked_by, options, recommendation, escalation_reason, ladder_trace,
   claimed_by, claim_until, decision_id)
VALUES
  (:workspace_id, :id, :text, :normalized_hash, :categories, :status, :rung, :blocking, :subject_kind, :subject_id,
   :batch_id, :asked_by, :options, :recommendation, :escalation_reason, :ladder_trace,
   :claimed_by, :claim_until, :decision_id)`)

	stmtSaveQuestion = define("question.save",
		`UPDATE questions
   SET categories = :categories, status = :status, rung = :rung, blocking = :blocking,
       batch_id = :batch_id, options = :options, recommendation = :recommendation,
       escalation_reason = :escalation_reason, ladder_trace = :ladder_trace,
       claimed_by = :claimed_by, claim_until = :claim_until, decision_id = :decision_id
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtReadQuestion = define("question.read",
		`SELECT `+questionColumns+` FROM questions WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListQuestions = define("question.list",
		`SELECT `+questionColumns+`
  FROM questions q
 WHERE q.workspace_id = :workspace_id`+questionFilter+`
 ORDER BY q.id
 LIMIT :limit`)

	stmtSearchQuestions = define("question.search",
		`SELECT `+questionColumns+`
  FROM questions q
  JOIN (`+matchMark+`) m ON m.workspace_id = q.workspace_id AND m.kind = 'question' AND m.record_id = q.id
 WHERE q.workspace_id = :workspace_id`+questionFilter+`
 ORDER BY m.score DESC, q.id
 LIMIT :limit`)

	stmtQuestionsByHash = define("question.by_hash",
		`SELECT `+questionColumns+` FROM questions
 WHERE workspace_id = :workspace_id AND normalized_hash = :normalized_hash
 ORDER BY id`)

	stmtAnsweredBy = define("question.answered_by",
		`SELECT `+questionColumns+` FROM questions
 WHERE workspace_id = :workspace_id AND decision_id = :decision_id
 ORDER BY id`)

	stmtInsertAttachment = define("question.attach",
		`INSERT INTO question_attachments (workspace_id, question_id, lane_id, generation, blocking, attached_at)
VALUES (:workspace_id, :question_id, :lane_id, :generation, :blocking, :attached_at)
ON CONFLICT (workspace_id, question_id, lane_id) DO NOTHING`)

	stmtListAttachments = define("question.attachments",
		`SELECT question_id, lane_id, generation, blocking, attached_at FROM question_attachments
 WHERE workspace_id = :workspace_id AND (CAST(:question_id AS TEXT) = '' OR question_id = CAST(:question_id AS TEXT))
 ORDER BY question_id, lane_id`)

	stmtOpenBlocking = define("question.open_blocking",
		`SELECT count(*) FROM questions
 WHERE workspace_id = :workspace_id
   AND blocking = 1
   AND status IN ('open', 'triage', 'needs_human')
   AND ((subject_kind = 'work_order' AND subject_id = :work_order_id)
     OR (subject_kind = 'contract' AND subject_id = :contract_id))`)

	stmtTakeQuestion = define("question.take",
		`UPDATE questions
   SET claimed_by = :holder,
       claim_until = CAST(:now AS BIGINT) + CAST(:ttl AS BIGINT),
       status = 'triage'
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND status IN ('open', 'triage')
   AND (claimed_by IS NULL OR claim_until <= :now OR claimed_by = :holder)`)

	stmtInsertDecision = define("decision.insert",
		`INSERT INTO decisions
  (workspace_id, id, status, statement, rationale, citations, scope, reversibility, categories,
   decided_by, supersedes, question_id, policy_version, overlapping, no_conflict)
VALUES
  (:workspace_id, :id, :status, :statement, :rationale, :citations, :scope, :reversibility, :categories,
   :decided_by, :supersedes, :question_id, :policy_version, :overlaps, :no_conflict)`)

	stmtReadDecision = define("decision.read",
		`SELECT `+decisionColumns+` FROM decisions WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListDecisions = define("decision.list",
		`SELECT `+decisionColumns+`
  FROM decisions d
 WHERE d.workspace_id = :workspace_id`+decisionFilter+`
 ORDER BY d.id
 LIMIT :limit`)

	stmtSearchDecisions = define("decision.search",
		`SELECT d.id, d.status, d.statement, d.rationale, d.citations, d.scope, d.reversibility, d.categories,
       d.decided_by, d.supersedes, d.question_id, d.policy_version, d.overlapping, d.no_conflict
  FROM decisions d
  JOIN (`+matchMark+`) m ON m.workspace_id = d.workspace_id AND m.kind = 'decision' AND m.record_id = d.id
 WHERE d.workspace_id = :workspace_id`+decisionFilter+`
 ORDER BY m.score DESC, d.id
 LIMIT :limit`)

	stmtEndDecision = define("decision.end",
		`UPDATE decisions SET status = :status
 WHERE workspace_id = :workspace_id AND id = :id
   AND status IN ('assumed', 'decided')`)

	// stmtOverlapping repeats questions.ScopesOverlap. It compares prefixes with substr,
	// because a scope may hold the marks a LIKE pattern reads.
	stmtOverlapping = define("decision.overlapping",
		`SELECT `+decisionColumns+`
  FROM decisions d
 WHERE d.workspace_id = :workspace_id
   AND d.status IN ('assumed', 'decided')
   AND (d.scope = CAST(:scope AS TEXT)
     OR substr(d.scope, 1, length(CAST(:scope AS TEXT)) + 1) = CAST(:scope AS TEXT) || '/'
     OR substr(CAST(:scope AS TEXT), 1, length(d.scope) + 1) = d.scope || '/')
 ORDER BY d.id`)

	stmtAssumptions = define("decision.assumptions",
		`SELECT count(*)
  FROM decisions d
  JOIN questions q ON q.workspace_id = d.workspace_id AND q.id = d.question_id
 WHERE d.workspace_id = :workspace_id AND d.status = 'assumed'
   AND q.subject_kind = 'work_order' AND q.subject_id = :work_order_id`)

	stmtOpenAssumptions = define("decision.open_assumptions",
		`SELECT d.id
  FROM decisions d
  JOIN questions q ON q.workspace_id = d.workspace_id AND q.id = d.question_id
 WHERE d.workspace_id = :workspace_id AND d.status = 'assumed'
   AND q.subject_kind = 'work_order' AND q.subject_id = :work_order_id
 ORDER BY d.id`)

	stmtInsertNotice = define("decision_notice.insert",
		`INSERT INTO decision_notices (workspace_id, decision_id, lane_id, generation, replaced_by, noticed_at)
VALUES (:workspace_id, :decision_id, :lane_id, :generation, :replaced_by, :noticed_at)
ON CONFLICT (workspace_id, decision_id, lane_id) DO NOTHING`)

	stmtListNotices = define("decision_notice.list",
		`SELECT decision_id, lane_id, generation, replaced_by, noticed_at FROM decision_notices
 WHERE workspace_id = :workspace_id AND (CAST(:decision_id AS TEXT) = '' OR decision_id = CAST(:decision_id AS TEXT))
 ORDER BY decision_id, lane_id`)

	stmtAddCategory = define("category.add",
		`INSERT INTO workspace_categories (workspace_id, name) VALUES (:workspace_id, :name)
ON CONFLICT (workspace_id, name) DO NOTHING`)

	stmtListCategories = define("category.list",
		`SELECT name FROM workspace_categories WHERE workspace_id = :workspace_id ORDER BY name`)

	stmtInsertBatch = define("question_batch.insert",
		`INSERT INTO question_batches (workspace_id, id, status, opened_at, closes_at)
VALUES (:workspace_id, :id, :status, :opened_at, :closes_at)`)

	stmtReadBatch = define("question_batch.read",
		`SELECT `+batchColumns+` FROM question_batches b WHERE b.workspace_id = :workspace_id AND b.id = :id`)

	stmtOpenBatch = define("question_batch.open",
		`SELECT `+batchColumns+` FROM question_batches b WHERE b.workspace_id = :workspace_id AND b.status = 'open'`)

	stmtListBatches = define("question_batch.list",
		`SELECT `+batchColumns+` FROM question_batches b WHERE b.workspace_id = :workspace_id ORDER BY b.opened_at, b.id`)

	stmtDueBatches = define("question_batch.due",
		`SELECT id FROM question_batches
 WHERE workspace_id = :workspace_id AND status = 'open'
   AND closes_at <= :now
 ORDER BY closes_at, id`)

	stmtWaitingBatches = define("question_batch.waiting",
		`SELECT `+batchColumns+`
  FROM question_batches b
 WHERE b.workspace_id = :workspace_id AND b.status = 'delivered'
   AND EXISTS (SELECT 1 FROM questions q
                WHERE q.workspace_id = b.workspace_id AND q.batch_id = b.id AND q.status = 'needs_human')
 ORDER BY b.delivered_at, b.id
 LIMIT :limit`)

	stmtBatchQuestions = define("question_batch.questions",
		`SELECT `+questionColumns+` FROM questions WHERE workspace_id = :workspace_id AND batch_id = :batch_id ORDER BY id`)

	stmtHastenBatch = define("question_batch.hasten",
		`UPDATE question_batches SET closes_at = :closes_at
 WHERE workspace_id = :workspace_id AND id = :id AND status = 'open' AND closes_at > :closes_at`)

	stmtCloseBatch = define("question_batch.close",
		`UPDATE question_batches SET status = :status, delivered_at = :delivered_at
 WHERE workspace_id = :workspace_id AND id = :id
   AND status = 'open'`)
)

// questionTx is the question area of one transaction.
type questionTx struct {
	*session
	scope ledger.WorkspaceID
	now   int64
}

func (t questionTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

// recordFilter binds what a filter asks for. A category is matched inside its JSON list,
// and CheckCategoryName keeps a name free of the marks a pattern reads.
func recordFilter(filter ports.RecordFilter) params {
	category := ""
	if filter.Category != "" {
		category = `%"` + filter.Category + `"%`
	}
	limit := filter.Limit
	if limit <= 0 || limit > ports.MaxRecords {
		limit = ports.MaxRecords
	}
	return params{"status": filter.Status, "scope": filter.Scope, "category": category, "after": filter.After, "limit": int64(limit)}
}

func questionParams(q questions.Question) (params, error) {
	categories, err := json.Marshal(nonNil(q.Categories))
	if err != nil {
		return nil, err
	}
	options, err := json.Marshal(nonNil(q.Options))
	if err != nil {
		return nil, err
	}
	trace, err := json.Marshal(nonNil(q.LadderTrace))
	if err != nil {
		return nil, err
	}
	return params{
		"id": q.ID, "text": q.Text, "normalized_hash": q.NormalizedHash, "categories": string(categories),
		"status": string(q.Status), "rung": q.Rung, "blocking": flag(q.Blocking), "subject_kind": nullText(q.SubjectKind),
		"subject_id": nullText(q.SubjectID), "batch_id": nullText(q.BatchID), "asked_by": nullText(q.AskedBy),
		"options": string(options), "recommendation": nullText(q.Recommendation), "escalation_reason": nullText(q.EscalationReason),
		"ladder_trace": string(trace), "claimed_by": nullText(q.ClaimedBy), "claim_until": nullInt(q.ClaimUntil),
		"decision_id": nullText(q.DecisionID),
	}, nil
}

func flag(on bool) int64 {
	if on {
		return 1
	}
	return 0
}

func (t questionTx) Insert(q questions.Question) error {
	p, err := questionParams(q)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertQuestion, t.scoped(p))
	return err
}

func (t questionTx) Save(q questions.Question) error {
	p, err := questionParams(q)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSaveQuestion, t.scoped(p))
	return err
}

func scanQuestion(row scanner) (questions.Question, error) {
	var q questions.Question
	var status, categories, options, trace string
	var blocking int64
	var subjectKind, subjectID, batch, askedBy, recommendation, reason, claimedBy, decision sql.NullString
	var claimUntil sql.NullInt64
	err := row.Scan(&q.ID, &q.Text, &q.NormalizedHash, &categories, &status, &q.Rung, &blocking, &subjectKind, &subjectID,
		&batch, &askedBy, &options, &recommendation, &reason, &trace, &claimedBy, &claimUntil, &decision)
	if err != nil {
		return questions.Question{}, err
	}
	q.Status, q.Blocking = questions.Status(status), blocking != 0
	q.SubjectKind, q.SubjectID, q.BatchID, q.AskedBy = subjectKind.String, subjectID.String, batch.String, askedBy.String
	q.Recommendation, q.EscalationReason = recommendation.String, reason.String
	q.ClaimedBy, q.ClaimUntil, q.DecisionID = claimedBy.String, claimUntil.Int64, decision.String
	for _, field := range []struct {
		raw  string
		into any
	}{{categories, &q.Categories}, {options, &q.Options}, {trace, &q.LadderTrace}} {
		if err := json.Unmarshal([]byte(field.raw), field.into); err != nil {
			return questions.Question{}, fmt.Errorf("question %s: %w", q.ID, err)
		}
	}
	return q, nil
}

func (t questionTx) Question(id string) (questions.Question, error) {
	row, err := t.row(stmtReadQuestion, t.scoped(params{"id": id}))
	if err != nil {
		return questions.Question{}, err
	}
	q, err := scanQuestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return questions.Question{}, fmt.Errorf("question %s: %w", id, ports.ErrNotFound)
	}
	return q, err
}

func (t questionTx) listQuestions(st *statement, p params) ([]questions.Question, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []questions.Question
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (t questionTx) Questions(filter ports.RecordFilter) ([]questions.Question, error) {
	p := recordFilter(filter)
	if filter.Text == "" {
		return t.listQuestions(stmtListQuestions, p)
	}
	terms := questions.Terms(filter.Text)
	if len(terms) == 0 {
		return nil, nil
	}
	p["query"] = t.store.dialect.MatchQuery(terms)
	return t.listQuestions(stmtSearchQuestions, p)
}

func (t questionTx) ByHash(hash string) ([]questions.Question, error) {
	return t.listQuestions(stmtQuestionsByHash, params{"normalized_hash": hash})
}

func (t questionTx) AnsweredBy(decision string) ([]questions.Question, error) {
	return t.listQuestions(stmtAnsweredBy, params{"decision_id": decision})
}

func (t questionTx) Attach(a questions.Attachment) (bool, error) {
	n, err := t.exec(stmtInsertAttachment, t.scoped(params{
		"question_id": a.QuestionID, "lane_id": a.Lane, "generation": a.Generation,
		"blocking": flag(a.Blocking), "attached_at": t.now,
	}))
	return n > 0, err
}

func (t questionTx) Take(id, holder string, ttlMs int64) (bool, error) {
	n, err := t.exec(stmtTakeQuestion, t.scoped(params{"id": id, "holder": holder, "now": t.now, "ttl": ttlMs}))
	return n > 0, err
}

func (t questionTx) OpenBlocking(workOrder, contract string) (int64, error) {
	row, err := t.row(stmtOpenBlocking, t.scoped(params{"work_order_id": workOrder, "contract_id": contract}))
	if err != nil {
		return 0, err
	}
	var count int64
	err = row.Scan(&count)
	return count, err
}

func (t questionTx) Attachments(question string) ([]questions.Attachment, error) {
	rows, err := t.rows(stmtListAttachments, t.scoped(params{"question_id": question}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []questions.Attachment
	for rows.Next() {
		var a questions.Attachment
		var blocking int64
		if err := rows.Scan(&a.QuestionID, &a.Lane, &a.Generation, &blocking, &a.AttachedAt); err != nil {
			return nil, err
		}
		a.Blocking = blocking != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (t questionTx) InsertDecision(d questions.Decision) error {
	citations, err := json.Marshal(nonNil(d.Citations))
	if err != nil {
		return err
	}
	categories, err := json.Marshal(nonNil(d.Categories))
	if err != nil {
		return err
	}
	overlaps, err := json.Marshal(nonNil(d.Overlaps))
	if err != nil {
		return err
	}
	noConflict, err := json.Marshal(nonNil(d.NoConflict))
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertDecision, t.scoped(params{
		"id": d.ID, "status": string(d.Status), "statement": d.Statement, "rationale": nullText(d.Rationale),
		"citations": string(citations), "scope": nullText(d.Scope), "reversibility": nullText(d.Reversibility),
		"categories": string(categories), "decided_by": nullText(d.DecidedBy), "supersedes": nullText(d.Supersedes),
		"question_id": nullText(d.QuestionID), "policy_version": d.PolicyVersion,
		"overlaps": string(overlaps), "no_conflict": string(noConflict),
	}))
	return err
}

func scanDecision(row scanner) (questions.Decision, error) {
	var d questions.Decision
	var status, citations, categories, overlaps, noConflict string
	var rationale, scope, reversibility, decidedBy, supersedes, question sql.NullString
	err := row.Scan(&d.ID, &status, &d.Statement, &rationale, &citations, &scope, &reversibility, &categories,
		&decidedBy, &supersedes, &question, &d.PolicyVersion, &overlaps, &noConflict)
	if err != nil {
		return questions.Decision{}, err
	}
	d.Status, d.Rationale, d.Scope, d.Reversibility = questions.DecisionStatus(status), rationale.String, scope.String, reversibility.String
	d.DecidedBy, d.Supersedes, d.QuestionID = decidedBy.String, supersedes.String, question.String
	for _, field := range []struct {
		raw  string
		into *[]string
	}{{citations, &d.Citations}, {categories, &d.Categories}, {overlaps, &d.Overlaps}, {noConflict, &d.NoConflict}} {
		if err := json.Unmarshal([]byte(field.raw), field.into); err != nil {
			return questions.Decision{}, fmt.Errorf("decision %s: %w", d.ID, err)
		}
	}
	return d, nil
}

func (t questionTx) Decision(id string) (questions.Decision, error) {
	row, err := t.row(stmtReadDecision, t.scoped(params{"id": id}))
	if err != nil {
		return questions.Decision{}, err
	}
	d, err := scanDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return questions.Decision{}, fmt.Errorf("decision %s: %w", id, ports.ErrNotFound)
	}
	return d, err
}

func (t questionTx) listDecisions(st *statement, p params) ([]questions.Decision, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []questions.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (t questionTx) Decisions(filter ports.RecordFilter) ([]questions.Decision, error) {
	p := recordFilter(filter)
	if filter.Text == "" {
		return t.listDecisions(stmtListDecisions, p)
	}
	terms := questions.Terms(filter.Text)
	if len(terms) == 0 {
		return nil, nil
	}
	p["query"] = t.store.dialect.MatchQuery(terms)
	return t.listDecisions(stmtSearchDecisions, p)
}

func (t questionTx) EndDecision(id string, status questions.DecisionStatus) (bool, error) {
	n, err := t.exec(stmtEndDecision, t.scoped(params{"id": id, "status": string(status)}))
	return n > 0, err
}

func (t questionTx) Overlapping(scope string) ([]questions.Decision, error) {
	if scope == "" {
		return nil, nil
	}
	return t.listDecisions(stmtOverlapping, params{"scope": scope})
}

func (t questionTx) Assumptions(workOrder string) (int64, error) {
	row, err := t.row(stmtAssumptions, t.scoped(params{"work_order_id": workOrder}))
	if err != nil {
		return 0, err
	}
	var count int64
	err = row.Scan(&count)
	return count, err
}

func (t questionTx) OpenAssumptions(workOrder string) ([]string, error) {
	rows, err := t.rows(stmtOpenAssumptions, t.scoped(params{"work_order_id": workOrder}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (t questionTx) Notice(n questions.Notice) (bool, error) {
	added, err := t.exec(stmtInsertNotice, t.scoped(params{
		"decision_id": n.DecisionID, "lane_id": n.Lane, "generation": n.Generation,
		"replaced_by": n.ReplacedBy, "noticed_at": n.NoticedAt,
	}))
	return added > 0, err
}

func (t questionTx) Notices(decision string) ([]questions.Notice, error) {
	rows, err := t.rows(stmtListNotices, t.scoped(params{"decision_id": decision}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []questions.Notice
	for rows.Next() {
		var n questions.Notice
		if err := rows.Scan(&n.DecisionID, &n.Lane, &n.Generation, &n.ReplacedBy, &n.NoticedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (t questionTx) AddCategory(name string) (bool, error) {
	n, err := t.exec(stmtAddCategory, t.scoped(params{"name": name}))
	return n > 0, err
}

func (t questionTx) Categories() ([]string, error) {
	rows, err := t.rows(stmtListCategories, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func scanBatch(row scanner) (questions.Batch, error) {
	var batch questions.Batch
	var deliveredAt sql.NullInt64
	if err := row.Scan(&batch.ID, &batch.Status, &batch.OpenedAt, &batch.ClosesAt, &deliveredAt); err != nil {
		return questions.Batch{}, err
	}
	batch.DeliveredAt = deliveredAt.Int64
	return batch, nil
}

func (t questionTx) oneBatch(st *statement, p params, what string) (questions.Batch, error) {
	row, err := t.row(st, t.scoped(p))
	if err != nil {
		return questions.Batch{}, err
	}
	batch, err := scanBatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return questions.Batch{}, fmt.Errorf("%s: %w", what, ports.ErrNotFound)
	}
	return batch, err
}

func (t questionTx) Batch(id string) (questions.Batch, error) {
	return t.oneBatch(stmtReadBatch, params{"id": id}, "batch "+id)
}

func (t questionTx) OpenBatch() (questions.Batch, error) {
	return t.oneBatch(stmtOpenBatch, params{}, "open batch")
}

func (t questionTx) listBatches(st *statement, p params) ([]questions.Batch, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []questions.Batch
	for rows.Next() {
		batch, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, batch)
	}
	return out, rows.Err()
}

func (t questionTx) Batches() ([]questions.Batch, error) {
	return t.listBatches(stmtListBatches, params{})
}

func (t questionTx) WaitingBatches() ([]questions.Batch, error) {
	return t.listBatches(stmtWaitingBatches, params{"limit": ports.MaxRecords})
}

func (t questionTx) DueBatches(now int64) ([]string, error) {
	rows, err := t.rows(stmtDueBatches, t.scoped(params{"now": now}))
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

func (t questionTx) BatchQuestions(id string) ([]questions.Question, error) {
	return t.listQuestions(stmtBatchQuestions, params{"batch_id": id})
}

func (t questionTx) InsertBatch(batch questions.Batch) error {
	_, err := t.exec(stmtInsertBatch, t.scoped(params{
		"id": batch.ID, "status": string(batch.Status), "opened_at": batch.OpenedAt, "closes_at": batch.ClosesAt,
	}))
	return err
}

func (t questionTx) HastenBatch(id string, closesAt int64) (bool, error) {
	n, err := t.exec(stmtHastenBatch, t.scoped(params{"id": id, "closes_at": closesAt}))
	return n > 0, err
}

func (t questionTx) CloseBatch(id string, status questions.BatchStatus, deliveredAt int64) (bool, error) {
	n, err := t.exec(stmtCloseBatch, t.scoped(params{"id": id, "status": string(status), "delivered_at": nullInt(deliveredAt)}))
	return n > 0, err
}
