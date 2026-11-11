package sqlstore

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// matchMark stands for a dialect's match statement inside a neutral one.
const matchMark = "{{match}}"

// expandMatch puts a dialect's match statement where a neutral statement names it.
func expandMatch(text string, dialect Dialect) string {
	return strings.ReplaceAll(text, matchMark, dialect.MatchSQL())
}

var (
	stmtIndexDocument = define("search.index",
		`INSERT INTO search_documents (workspace_id, kind, record_id, body)
VALUES (:workspace_id, :kind, :record_id, :body)
ON CONFLICT (workspace_id, kind, record_id) DO UPDATE SET body = :body`)

	// stmtSearch leaves out what a ladder must not act on: a question that was answered
	// or dismissed, and a decision a person overturned or a later one replaced.
	stmtSearch = define("search.ranked",
		`SELECT m.kind, m.record_id, m.score
  FROM (`+matchMark+`) m
 WHERE m.workspace_id = :workspace_id
   AND CAST(:kinds AS TEXT) LIKE '%,' || m.kind || ',%'
   AND NOT EXISTS (
     SELECT 1 FROM questions q
      WHERE q.workspace_id = m.workspace_id AND m.kind = 'question' AND q.id = m.record_id
        AND q.status IN ('answered', 'dismissed'))
   AND NOT EXISTS (
     SELECT 1 FROM decisions d
      WHERE d.workspace_id = m.workspace_id AND m.kind = 'decision' AND d.id = m.record_id
        AND d.status IN ('overturned', 'replaced'))
 ORDER BY m.score DESC, m.kind, m.record_id
 LIMIT :limit`)
)

// searchIn runs one search inside any transaction.
func searchIn(se *session, scope ledger.WorkspaceID, query ports.SearchQuery) ([]ports.SearchHit, error) {
	terms := questions.Terms(query.Text)
	if len(terms) == 0 || len(query.Kinds) == 0 {
		return nil, nil
	}
	limit := query.Limit
	if limit <= 0 || limit > ports.MaxRecords {
		limit = ports.MaxRecords
	}
	rows, err := se.rows(stmtSearch, params{
		"workspace_id": string(scope), "query": se.store.dialect.MatchQuery(terms),
		"kinds": "," + strings.Join(query.Kinds, ",") + ",", "limit": int64(limit),
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.SearchHit
	for rows.Next() {
		var hit ports.SearchHit
		if err := rows.Scan(&hit.Kind, &hit.RecordID, &hit.Score); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (t *readTx) Search(query ports.SearchQuery) ([]ports.SearchHit, error) {
	return searchIn(t.session, t.scope, query)
}

func (t *writeTx) Search(query ports.SearchQuery) ([]ports.SearchHit, error) {
	return searchIn(t.session, t.scope, query)
}

// Index keeps a record's text for search. It stores the words search compares, so every
// database reads the same words whatever its own tokenizer does with marks.
func (t *writeTx) Index(kind, id, text string) error {
	_, err := t.exec(stmtIndexDocument, params{
		"workspace_id": string(t.scope), "kind": kind, "record_id": id,
		"body": strings.Join(questions.Terms(text), " "),
	})
	return err
}

func (t *readTx) Questions() ports.QuestionReader {
	return questionTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Questions() ports.QuestionWriter {
	return questionTx{session: t.session, scope: t.scope, now: t.now}
}

func (t *readTx) Members() ports.MemberReader {
	se, scope := t.side.session(t.session, t.scope)
	return memberTx{session: se, scope: scope}
}

func (t *writeTx) Members() ports.MemberReader {
	se, scope := t.side.session(t.session, t.scope)
	return memberTx{session: se, scope: scope}
}

func (t *readTx) Approvals() ports.ApprovalReader {
	return approvalTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Approvals() ports.ApprovalWriter {
	return approvalTx{session: t.session, scope: t.scope}
}

func (t *readTx) Policies() ports.PolicyReader {
	se, scope := t.side.session(t.session, t.scope)
	return policyTx{session: t.session, scope: t.scope, autonomy: se, autonomyScope: scope}
}

func (t *writeTx) Policies() ports.PolicyWriter {
	se, scope := t.side.session(t.session, t.scope)
	return projectPolicies{policyTx{session: t.session, scope: t.scope, autonomy: se, autonomyScope: scope}}
}

// projectPolicies is the policy area of a project transaction, which reads the autonomy policy and never
// writes it: a version of it commits through the installation.
type projectPolicies struct {
	policyTx
}

func (t projectPolicies) Insert(p authority.PolicyVersion) (bool, error) {
	if p.Kind == authority.PolicyAutonomy {
		return false, fmt.Errorf("%w: the autonomy policy is the installation's", ports.ErrNotShared)
	}
	return t.policyTx.Insert(p)
}

func (t *readTx) Evidence() ports.EvidenceReader {
	return evidenceTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Evidence() ports.EvidenceWriter {
	return evidenceTx{session: t.session, scope: t.scope}
}

func (t *readTx) Measures() ports.MeasureReader {
	return measureTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Measures() ports.MeasureWriter {
	return measureTx{session: t.session, scope: t.scope}
}
