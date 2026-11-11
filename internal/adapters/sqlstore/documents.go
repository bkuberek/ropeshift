package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

const (
	documentColumns        = `id, kind, plan_id, title`
	documentVersionColumns = `document_id, version, title, digest, source, body, fields`
	planVersionColumns     = `plan_id, version, digest, source, documents, decisions, file_digest, records, work_order, left_out`
)

var (
	stmtSaveDocument = define("document.save",
		`INSERT INTO documents (workspace_id, id, kind, plan_id, title)
VALUES (:workspace_id, :id, :kind, :plan_id, :title)
ON CONFLICT (workspace_id, id) DO UPDATE SET plan_id = :plan_id, title = :title`)

	stmtReadDocument = define("document.read",
		`SELECT `+documentColumns+` FROM documents WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListDocuments = define("document.list",
		`SELECT `+documentColumns+` FROM documents WHERE workspace_id = :workspace_id ORDER BY id`)

	stmtPlanDocuments = define("document.of_plan",
		`SELECT `+documentColumns+` FROM documents WHERE workspace_id = :workspace_id AND plan_id = :plan_id ORDER BY id`)

	stmtInsertDocumentVersion = define("document_version.insert",
		`INSERT INTO document_versions (workspace_id, document_id, version, title, digest, source, body, fields)
VALUES (:workspace_id, :document_id, :version, :title, :digest, :source, :body, :fields)`)

	stmtReadDocumentVersion = define("document_version.read",
		`SELECT `+documentVersionColumns+` FROM document_versions
 WHERE workspace_id = :workspace_id AND document_id = :document_id AND version = :version`)

	stmtLatestDocumentVersion = define("document_version.latest",
		`SELECT `+documentVersionColumns+` FROM document_versions
 WHERE workspace_id = :workspace_id AND document_id = :document_id
 ORDER BY version DESC
 LIMIT 1`)

	stmtListDocumentVersions = define("document_version.list",
		`SELECT `+documentVersionColumns+` FROM document_versions
 WHERE workspace_id = :workspace_id
 ORDER BY document_id, version`)

	stmtInsertPlanVersion = define("plan_version.insert",
		`INSERT INTO plan_versions (workspace_id, plan_id, version, digest, source, documents, decisions, file_digest, records, work_order, left_out)
VALUES (:workspace_id, :plan_id, :version, :digest, :source, :documents, :decisions, :file_digest, :records, :work_order, :left_out)`)

	stmtPlanVersion = define("plan_version.get",
		`SELECT `+planVersionColumns+` FROM plan_versions
 WHERE workspace_id = :workspace_id AND plan_id = :plan_id AND version = :version`)

	stmtLatestPlanVersion = define("plan_version.latest",
		`SELECT `+planVersionColumns+` FROM plan_versions
 WHERE workspace_id = :workspace_id AND plan_id = :plan_id
 ORDER BY version DESC
 LIMIT 1`)

	stmtListPlanVersions = define("plan_version.list",
		`SELECT `+planVersionColumns+` FROM plan_versions
 WHERE workspace_id = :workspace_id
 ORDER BY plan_id, version`)

	stmtInsertContractDocument = define("contract_document.insert",
		`INSERT INTO contract_documents (workspace_id, contract_id, contract_version, position, document_id, document_version)
VALUES (:workspace_id, :contract_id, :contract_version, :position, :document_id, :document_version)`)

	stmtContractDocuments = define("contract_document.of",
		`SELECT document_id, document_version FROM contract_documents
 WHERE workspace_id = :workspace_id AND contract_id = :contract_id AND contract_version = :contract_version
 ORDER BY position`)

	stmtListContractDocuments = define("contract_document.list",
		`SELECT contract_id, contract_version, document_id, document_version FROM contract_documents
 WHERE workspace_id = :workspace_id
 ORDER BY contract_id, contract_version, position`)
)

func (t planningTx) SaveDocument(d planning.DocumentRecord) error {
	_, err := t.exec(stmtSaveDocument, t.scoped(params{
		"id": d.ID, "kind": string(d.Kind), "plan_id": d.PlanID, "title": d.Title,
	}))
	return err
}

func scanDocument(row scanner) (planning.DocumentRecord, error) {
	var d planning.DocumentRecord
	var kind string
	if err := row.Scan(&d.ID, &kind, &d.PlanID, &d.Title); err != nil {
		return planning.DocumentRecord{}, err
	}
	d.Kind = planning.FileKind(kind)
	return d, nil
}

func (t planningTx) Document(id string) (planning.DocumentRecord, error) {
	row, err := t.row(stmtReadDocument, t.scoped(params{"id": id}))
	if err != nil {
		return planning.DocumentRecord{}, err
	}
	d, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.DocumentRecord{}, fmt.Errorf("document %s: %w", id, ports.ErrNotFound)
	}
	return d, err
}

func (t planningTx) documents(st *statement, p params) ([]planning.DocumentRecord, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.DocumentRecord
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (t planningTx) Documents() ([]planning.DocumentRecord, error) {
	return t.documents(stmtListDocuments, nil)
}

func (t planningTx) PlanDocuments(plan string) ([]planning.DocumentRecord, error) {
	return t.documents(stmtPlanDocuments, params{"plan_id": plan})
}

func (t planningTx) InsertDocumentVersion(v planning.DocumentVersion) error {
	_, err := t.exec(stmtInsertDocumentVersion, t.scoped(params{
		"document_id": v.DocumentID, "version": v.Version, "title": v.Title, "digest": v.Digest,
		"source": nullText(v.Source), "body": v.Body, "fields": string(v.Fields),
	}))
	return err
}

func scanDocumentVersion(row scanner) (planning.DocumentVersion, error) {
	var v planning.DocumentVersion
	var source sql.NullString
	var fields string
	if err := row.Scan(&v.DocumentID, &v.Version, &v.Title, &v.Digest, &source, &v.Body, &fields); err != nil {
		return planning.DocumentVersion{}, err
	}
	v.Source, v.Fields = source.String, json.RawMessage(fields)
	return v, nil
}

func (t planningTx) oneDocumentVersion(st *statement, id string, p params) (planning.DocumentVersion, error) {
	p["document_id"] = id
	row, err := t.row(st, t.scoped(p))
	if err != nil {
		return planning.DocumentVersion{}, err
	}
	v, err := scanDocumentVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.DocumentVersion{}, fmt.Errorf("document %s: %w", id, ports.ErrNotFound)
	}
	return v, err
}

func (t planningTx) DocumentVersion(id string, version int64) (planning.DocumentVersion, error) {
	return t.oneDocumentVersion(stmtReadDocumentVersion, id, params{"version": version})
}

func (t planningTx) LatestDocumentVersion(id string) (planning.DocumentVersion, error) {
	return t.oneDocumentVersion(stmtLatestDocumentVersion, id, params{})
}

func (t planningTx) DocumentVersions() ([]planning.DocumentVersion, error) {
	rows, err := t.rows(stmtListDocumentVersions, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.DocumentVersion
	for rows.Next() {
		v, err := scanDocumentVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t planningTx) InsertPlanVersion(v planning.PlanVersion) error {
	documents, err := json.Marshal(nonNil(v.Documents))
	if err != nil {
		return err
	}
	decisions, err := json.Marshal(nonNil(v.Decisions))
	if err != nil {
		return err
	}
	records, err := json.Marshal(nonNil(v.Records))
	if err != nil {
		return err
	}
	leftOut, err := json.Marshal(nonNil(v.LeftOut))
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertPlanVersion, t.scoped(params{
		"plan_id": v.PlanID, "version": v.Version, "digest": v.Digest, "source": nullText(v.Source),
		"documents": string(documents), "decisions": string(decisions), "file_digest": v.FileDigest, "records": string(records),
		"work_order": v.WorkOrder, "left_out": string(leftOut),
	}))
	return err
}

func scanPlanVersion(row scanner) (planning.PlanVersion, error) {
	var v planning.PlanVersion
	var source sql.NullString
	var documents, decisions, records, leftOut string
	if err := row.Scan(&v.PlanID, &v.Version, &v.Digest, &source, &documents, &decisions, &v.FileDigest, &records, &v.WorkOrder, &leftOut); err != nil {
		return planning.PlanVersion{}, err
	}
	v.Source = source.String
	for _, field := range []struct {
		raw  string
		into any
	}{{documents, &v.Documents}, {decisions, &v.Decisions}, {records, &v.Records}, {leftOut, &v.LeftOut}} {
		if err := json.Unmarshal([]byte(field.raw), field.into); err != nil {
			return planning.PlanVersion{}, fmt.Errorf("plan %s version %d: %w", v.PlanID, v.Version, err)
		}
	}
	return v, nil
}

func (t planningTx) LatestPlanVersion(plan string) (planning.PlanVersion, error) {
	row, err := t.row(stmtLatestPlanVersion, t.scoped(params{"plan_id": plan}))
	if err != nil {
		return planning.PlanVersion{}, err
	}
	v, err := scanPlanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.PlanVersion{}, fmt.Errorf("plan %s has no version: %w", plan, ports.ErrNotFound)
	}
	return v, err
}

func (t planningTx) PlanVersion(plan string, version int64) (planning.PlanVersion, error) {
	row, err := t.row(stmtPlanVersion, t.scoped(params{"plan_id": plan, "version": version}))
	if err != nil {
		return planning.PlanVersion{}, err
	}
	v, err := scanPlanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.PlanVersion{}, fmt.Errorf("plan %s version %d: %w", plan, version, ports.ErrNotFound)
	}
	return v, err
}

func (t planningTx) PlanVersions() ([]planning.PlanVersion, error) {
	rows, err := t.rows(stmtListPlanVersions, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.PlanVersion
	for rows.Next() {
		v, err := scanPlanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// insertPins keeps the document versions a contract version cites, in the order the file lists them.
func (t planningTx) insertPins(c planning.Contract) error {
	for position, pin := range c.Documents {
		_, err := t.exec(stmtInsertContractDocument, t.scoped(params{
			"contract_id": c.ID, "contract_version": c.Version, "position": int64(position),
			"document_id": pin.Document, "document_version": pin.Version,
		}))
		if err != nil {
			return err
		}
	}
	return nil
}

// pinsOf reads what one contract version cites.
func (t planningTx) pinsOf(id string, version int64) ([]planning.DocumentPin, error) {
	rows, err := t.rows(stmtContractDocuments, t.scoped(params{"contract_id": id, "contract_version": version}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.DocumentPin
	for rows.Next() {
		var pin planning.DocumentPin
		if err := rows.Scan(&pin.Document, &pin.Version); err != nil {
			return nil, err
		}
		out = append(out, pin)
	}
	return out, rows.Err()
}

// pinsByVersion reads what every contract version cites, for a listing that attaches them.
func (t planningTx) pinsByVersion() (map[string][]planning.DocumentPin, error) {
	rows, err := t.rows(stmtListContractDocuments, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]planning.DocumentPin{}
	for rows.Next() {
		var id string
		var version int64
		var pin planning.DocumentPin
		if err := rows.Scan(&id, &version, &pin.Document, &pin.Version); err != nil {
			return nil, err
		}
		key := planning.ContractKey(id, version)
		out[key] = append(out[key], pin)
	}
	return out, rows.Err()
}
