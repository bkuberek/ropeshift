package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// reviewMark is where one digest of a document stands in review, and the plan version that puts it there.
type reviewMark struct {
	state       string
	planVersion int64
}

// documentReviews reads where each document digest of a plan stands in review: listed by a plan version
// a member approved, or by one whose approval waits. A request bound to a digest its version no longer
// has covers nothing, and neither does a denied or superseded one. An approval is cited before a wait,
// and a newer version before an older one in the same state.
func documentReviews(tx ports.ReadTx, plan string) (map[string]reviewMark, error) {
	rows, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	marks := map[string]reviewMark{}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || row.Plan != plan || (row.State != authority.ApprovalApproved && row.State != authority.ApprovalPending) {
			continue
		}
		version, err := tx.Planning().PlanVersion(plan, row.PlanVersion)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if version.Digest != row.SubjectDigest {
			continue
		}
		state := api.ReviewPending
		if row.State == authority.ApprovalApproved {
			state = api.ReviewApproved
		}
		for _, entry := range version.Records {
			key := entry.Ref + "\x00" + entry.Digest
			held, taken := marks[key]
			if !taken || (state == api.ReviewApproved && held.state != api.ReviewApproved) ||
				(state == held.state && version.Version > held.planVersion) {
				marks[key] = reviewMark{state: state, planVersion: version.Version}
			}
		}
	}
	return marks, nil
}

// recordedAt reads when intake recorded each version of a document, which only the log knows.
func recordedAt(tx ports.ReadTx, id string) (map[int64]int64, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: planning.SubjectDocument, ID: id})
	if err != nil {
		return nil, err
	}
	at := map[int64]int64{}
	for _, event := range events {
		if event.Type != planning.EventDocumentVersion {
			continue
		}
		var recorded planning.DocumentRecorded
		if err := json.Unmarshal(event.Data, &recorded); err != nil {
			return nil, err
		}
		at[recorded.Version.Version] = event.RecordedAt
	}
	return at, nil
}

// sourcePath is the file a version's source names, without the commit after its last `@`.
func sourcePath(source string) string {
	if at := strings.LastIndexByte(source, '@'); at >= 0 {
		return source[:at]
	}
	return source
}

// recordedDocument reads every version of one document, newest first, with where each stands in review.
func recordedDocument(tx ports.ReadTx, record planning.DocumentRecord, versions []planning.DocumentVersion, marks map[string]reviewMark) (api.RecordedDocument, error) {
	at, err := recordedAt(tx, record.ID)
	if err != nil {
		return api.RecordedDocument{}, err
	}
	document := api.RecordedDocument{ID: record.ID, Kind: record.Kind, Title: record.Title, Versions: []api.RecordedVersion{}}
	for _, version := range slices.Backward(versions) {
		if version.DocumentID != record.ID {
			continue
		}
		if document.Path == "" {
			document.Path = sourcePath(version.Source)
		}
		mark, marked := marks[record.ID+"\x00"+version.Digest]
		if !marked {
			mark = reviewMark{state: api.ReviewNone}
		}
		document.Versions = append(document.Versions, api.RecordedVersion{
			Version: version.Version, Digest: version.Digest, Source: version.Source, RecordedAt: at[version.Version],
			Review: mark.state, PlanVersion: mark.planVersion,
		})
	}
	return document, nil
}

// planTraces lists each requirement a plan's requirements documents hold at their newest versions, in
// the order the files list them, with the criteria of the plan's newest contract versions that serve it
// and the work orders of each of those contracts (FR-DOC-7).
func planTraces(tx ports.ReadTx, plan string, documents []planning.DocumentRecord, contracts []planning.Contract) ([]api.RequirementTrace, error) {
	newest := []planning.Contract{}
	for _, contract := range contracts {
		if contract.PlanID != plan {
			continue
		}
		if last := len(newest) - 1; last >= 0 && newest[last].ID == contract.ID {
			newest[last] = contract
			continue
		}
		newest = append(newest, contract)
	}
	work := map[string][]api.TracedWork{}
	for _, contract := range newest {
		orders, err := tx.WorkOrders().ByContract(contract.ID)
		if err != nil {
			return nil, err
		}
		work[contract.ID] = []api.TracedWork{}
		for _, order := range orders {
			work[contract.ID] = append(work[contract.ID], api.TracedWork{ID: order.ID, Title: order.Title, State: order.State})
		}
	}
	traces := []api.RequirementTrace{}
	for _, document := range documents {
		if document.Kind != planning.FileRequirements {
			continue
		}
		latest, err := tx.Planning().LatestDocumentVersion(document.ID)
		if err != nil {
			return nil, err
		}
		var file planning.RequirementsFile
		if err := latest.Decode(&file); err != nil {
			return nil, err
		}
		for _, spec := range file.Requirements {
			trace := api.RequirementTrace{Key: spec.ID, Text: spec.Text, Document: document.ID, Criteria: []api.TracedCriterion{}}
			for _, contract := range newest {
				for _, criterion := range contract.Criteria {
					if !slices.Contains(criterion.Requirements, spec.ID) {
						continue
					}
					trace.Criteria = append(trace.Criteria, api.TracedCriterion{
						Contract: contract.ID, ContractTitle: contract.Title, ContractVersion: contract.Version, Criterion: criterion.ID,
						Text: criterion.Text, WorkOrders: work[contract.ID],
					})
				}
			}
			traces = append(traces, trace)
		}
	}
	return traces, nil
}

// recordedPlans reads, for each plan whose files recorded a document, those documents with every version
// and how its requirements trace to work. Plans come by title, the way a person looks one up.
func recordedPlans(tx ports.ReadTx) ([]api.PlanDocuments, error) {
	plans, err := plansByTitle(tx)
	if err != nil {
		return nil, err
	}
	versions, err := tx.Planning().DocumentVersions()
	if err != nil {
		return nil, err
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return nil, err
	}
	out := []api.PlanDocuments{}
	for _, plan := range plans {
		documents, err := tx.Planning().PlanDocuments(plan.ID)
		if err != nil {
			return nil, err
		}
		if len(documents) == 0 {
			continue
		}
		marks, err := documentReviews(tx, plan.ID)
		if err != nil {
			return nil, err
		}
		row := api.PlanDocuments{Plan: plan, Documents: []api.RecordedDocument{}}
		for _, record := range documents {
			document, err := recordedDocument(tx, record, versions, marks)
			if err != nil {
				return nil, err
			}
			row.Documents = append(row.Documents, document)
		}
		if row.Requirements, err = planTraces(tx, plan.ID, documents, contracts); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// markRecorded names, on each live file the ledger holds as a record, that record, by the file the
// record came from: a planning document a plan recorded, by its newest version, and a goal, a plan, or a
// contract, each of which a page shows on its own.
func markRecorded(tx ports.ReadTx, listed []api.Document, plans []api.PlanDocuments) ([]api.Document, error) {
	recordOf := map[string]string{}
	mark := func(source, kind, id string) {
		if path := sourcePath(source); path != "" {
			recordOf[path] = kind + ":" + id
		}
	}
	goals, err := tx.Planning().Goals()
	if err != nil {
		return nil, err
	}
	for _, goal := range goals {
		mark(goal.Source, planning.SubjectGoal, goal.ID)
	}
	held, err := tx.Planning().Plans()
	if err != nil {
		return nil, err
	}
	for _, plan := range held {
		mark(plan.Source, planning.SubjectPlan, plan.ID)
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return nil, err
	}
	for _, contract := range contracts {
		mark(contract.Source, planning.SubjectContract, contract.ID)
	}
	for _, plan := range plans {
		for _, document := range plan.Documents {
			if document.Path != "" {
				recordOf[document.Path] = document.ID
			}
		}
	}
	marked := slices.Clone(listed)
	for i := range marked {
		marked[i].Record = recordOf[marked[i].Path]
	}
	return marked, nil
}

// readRecorded reads one version of a planning document with its stored body. An empty version reads
// the newest.
func (h *handler) readRecorded(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id := query.Get("id")
	var wanted int64
	if raw := query.Get("version"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			invalid(w, "a version is a whole number from 1")
			return
		}
		wanted = parsed
	}
	var out api.RecordedRead
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Planning().Document(id)
		if err != nil {
			return err
		}
		var version planning.DocumentVersion
		if wanted == 0 {
			version, err = tx.Planning().LatestDocumentVersion(id)
		} else {
			version, err = tx.Planning().DocumentVersion(id, wanted)
		}
		if err != nil {
			return err
		}
		all, err := tx.Planning().DocumentVersions()
		if err != nil {
			return err
		}
		marks := map[string]reviewMark{}
		if record.PlanID != "" {
			plan, err := tx.Planning().Plan(record.PlanID)
			if err != nil {
				return err
			}
			out.Plan = &plan
			if marks, err = documentReviews(tx, record.PlanID); err != nil {
				return err
			}
		}
		if out.Document, err = recordedDocument(tx, record, all, marks); err != nil {
			return err
		}
		for _, row := range out.Document.Versions {
			if row.Version == version.Version {
				out.Version = row
			}
		}
		out.Body = version.Body
		out.AsOf, err = tx.Now()
		return err
	})
	if err != nil {
		fail(w, fmt.Errorf("document %s: %w", id, err))
		return
	}
	reply(w, http.StatusOK, out)
}
