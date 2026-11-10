package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// recordFile keeps what a goal, a plan, or a document says. It is one transaction, and
// a file that says what the ledger holds appends nothing.
func (i *Intake) recordFile(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource, item ports.RecordItem, refs *batchRefs) (itemOutcome, error) {
	var outcome itemOutcome
	windows := i.windows
	err := withLineage(ctx, source, func(known lineages) error {
		return i.store.Write(ctx, scope, "intake.file", func(tx ports.WriteTx) error {
			r := &fileRecorder{tx: tx, item: item, known: known, windows: windows, refs: refs}
			var err error
			switch {
			case item.Goal != nil:
				outcome, err = r.goal()
			case item.Plan != nil:
				outcome, err = r.plan()
			default:
				outcome, err = r.document()
			}
			crashpoint.Hit("intake.file", "recorded")
			return err
		})
	})
	return outcome, err
}

type fileRecorder struct {
	tx      ports.WriteTx
	item    ports.RecordItem
	known   lineages
	windows messages.Windows
	// refs are the milestones and contracts the whole batch holds, which this file's references resolve
	// against before the ledger.
	refs *batchRefs
}

// standing reads a changed record against this file: true when the change stands, and an outcome
// that says so when it does not, because the record's version was written knowing this commit, or
// the two changed a base neither saw.
func (r *fileRecorder) standing(kind, id, recorded string) (itemOutcome, bool, error) {
	stands, err := r.known.of(r.item.Source, recorded)
	switch {
	case err != nil:
		return itemOutcome{}, false, err
	case stands == ports.LineageDiverged:
		return itemOutcome{conflicts: 1, problem: baseMoved(kind, id)}, false, nil
	}
	return itemOutcome{}, stands == ports.LineageAhead, nil
}

func (r *fileRecorder) writer() planningWriter {
	return planningWriter{tx: r.tx, actor: intakeActor}
}

// recordID finds a key's record, and reports false for a key the ledger has not seen.
func (r *fileRecorder) recordID(kind string, key ledger.ExternalKey) (string, bool, error) {
	link, err := r.tx.Planning().Link(kind, key)
	if errors.Is(err, ports.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return link.RecordID, true, nil
}

// newRecord generates an ID and links it to its key.
func (r *fileRecorder) newRecord(kind string, key ledger.ExternalKey) (string, error) {
	id, err := newID(r.tx.Now())
	if err != nil {
		return "", err
	}
	return id, r.writer().link(kind, id, key)
}

func (r *fileRecorder) goal() (itemOutcome, error) {
	file := r.item.Goal
	next := planning.Goal{
		Title: file.Title, Status: planning.StatusActive, SuccessMeasures: file.SuccessMeasures,
		Statement: r.item.Statement, Source: r.item.Source,
	}
	id, known, err := r.recordID(planning.SubjectGoal, r.item.Key)
	if err != nil {
		return itemOutcome{}, err
	}
	if known {
		current, err := r.tx.Planning().Goal(id)
		if err != nil {
			return itemOutcome{}, err
		}
		if sameGoal(current, next) {
			return itemOutcome{}, nil
		}
		if outcome, stands, err := r.standing(planning.SubjectGoal, r.item.Key.NativeID, current.Source); err != nil || !stands {
			return outcome, err
		}
	} else if id, err = r.newRecord(planning.SubjectGoal, r.item.Key); err != nil {
		return itemOutcome{}, err
	}
	next.ID = id
	return itemOutcome{records: 1}, r.writer().goal(next)
}

func sameGoal(a, b planning.Goal) bool {
	return a.Title == b.Title && a.Statement == b.Statement && slices.Equal(a.SuccessMeasures, b.SuccessMeasures)
}

// planNamed resolves the plan a file names by the plan file's own ID. A file that names none belongs
// to no plan, and a plan the ledger has not seen is reported.
func (r *fileRecorder) planNamed(native string) (string, bool, error) {
	if native == "" {
		return "", true, nil
	}
	key := r.item.Key
	key.NativeID = native
	return r.recordID(planning.SubjectPlan, key)
}

// digest is the digest of the file as the source read it, or else of its content.
func (r *fileRecorder) digest(parts ...string) string {
	if r.item.Digest != "" {
		return r.item.Digest
	}
	return planning.ContentDigest(append(parts, r.item.Statement)...)
}

// plan records a plan file. A plan's file changes when its digest does, because what it lists and
// drafts is part of it. It is not a version of the plan yet: a plan version covers every record that
// names the plan, so the end of the batch makes it, once the batch's documents and contracts are in.
func (r *fileRecorder) plan() (itemOutcome, error) {
	file := r.item.Plan
	next := planning.Plan{Title: file.Title, Status: planning.StatusActive, Source: r.item.Source}
	if file.Goal != "" {
		key := r.item.Key
		key.NativeID = file.Goal
		goal, known, err := r.recordID(planning.SubjectGoal, key)
		if err != nil {
			return itemOutcome{}, err
		}
		if !known {
			return itemOutcome{problem: fmt.Sprintf("%v: goal %s is not known", planning.ErrBadFile, file.Goal)}, nil
		}
		next.GoalID = goal
	}
	fields, err := json.Marshal(file)
	if err != nil {
		return itemOutcome{}, err
	}
	read := planFileRead{digest: r.digest(string(fields)), source: r.item.Source, documents: file.Documents, decisions: file.Decisions}
	id, known, err := r.recordID(planning.SubjectPlan, r.item.Key)
	if err != nil {
		return itemOutcome{}, err
	}
	if !known {
		if next.ID, err = r.newRecord(planning.SubjectPlan, r.item.Key); err != nil {
			return itemOutcome{}, err
		}
		r.refs.plans[next.ID] = read
		return itemOutcome{records: 1}, r.writer().plan(next)
	}
	current, err := r.tx.Planning().Plan(id)
	if err != nil {
		return itemOutcome{}, err
	}
	latest, err := r.tx.Planning().LatestPlanVersion(id)
	switch {
	case err == nil && latest.FileDigest == read.digest:
		return itemOutcome{}, nil
	case err != nil && !errors.Is(err, ports.ErrNotFound):
		return itemOutcome{}, err
	}
	if outcome, stands, err := r.standing(planning.SubjectPlan, r.item.Key.NativeID, current.Source); err != nil || !stands {
		return outcome, err
	}
	next.ID = id
	r.refs.plans[id] = read
	return itemOutcome{records: 1}, r.writer().changedPlan(next)
}

// document records a file of any document kind: the document, and a new version of it when the file
// changed. A requirements file also records the requirements it holds.
func (r *fileRecorder) document() (itemOutcome, error) {
	head, ok := r.item.Head()
	if !ok {
		return itemOutcome{}, nil
	}
	plan, known, err := r.planNamed(head.Plan)
	if err != nil {
		return itemOutcome{}, err
	}
	if !known {
		return itemOutcome{problem: fmt.Sprintf("%v: plan %s is not known", planning.ErrBadFile, head.Plan)}, nil
	}
	if milestone := r.item.File.Milestone; milestone != nil {
		if problem, err := r.milestoneDependencies(*milestone, plan); err != nil || problem != "" {
			return itemOutcome{problem: problem}, err
		}
	}
	outcome, err := r.documentVersion(head, plan)
	if err != nil || outcome.problem != "" || r.item.Requirements == nil {
		return outcome, err
	}
	held, err := r.requirements()
	if err != nil {
		return itemOutcome{}, err
	}
	outcome.records += held.records
	outcome.conflicts += held.conflicts
	outcome.problem = strings.TrimPrefix(outcome.problem+"; "+held.problem, "; ")
	return outcome, nil
}

// documentVersion gives a document its next version when its file changed. A document is known by its
// reference, and a reference another source recorded is not this file's to take over.
func (r *fileRecorder) documentVersion(head planning.DocumentHead, plan string) (itemOutcome, error) {
	ref := head.Ref()
	key := r.item.Key
	key.NativeID = ref
	next := planning.DocumentVersion{
		DocumentID: ref, Version: 1, Title: head.Name(), Digest: r.digest(string(head.Fields)), Source: r.item.Source,
		Body: r.item.Statement, Fields: head.Fields,
	}
	_, known, err := r.recordID(planning.SubjectDocument, key)
	if err != nil {
		return itemOutcome{}, err
	}
	if !known {
		_, err := r.tx.Planning().Document(ref)
		if err == nil {
			return itemOutcome{problem: fmt.Sprintf("%v: document %s is already recorded from another source", planning.ErrBadFile, ref)}, nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return itemOutcome{}, err
		}
		if err := r.writer().link(planning.SubjectDocument, ref, key); err != nil {
			return itemOutcome{}, err
		}
	} else {
		latest, err := r.tx.Planning().LatestDocumentVersion(ref)
		if err != nil {
			return itemOutcome{}, err
		}
		if latest.Digest == next.Digest {
			return itemOutcome{}, nil
		}
		if outcome, stands, err := r.standing(planning.SubjectDocument, ref, latest.Source); err != nil || !stands {
			return outcome, err
		}
		next.Version = latest.Version + 1
	}
	record := planning.DocumentRecord{ID: ref, Kind: head.Kind, PlanID: plan, Title: head.Name()}
	if err := r.writer().document(record, next); err != nil {
		return itemOutcome{}, err
	}
	if next.Version > 1 {
		if err := r.tellDocument(record, next); err != nil {
			return itemOutcome{}, err
		}
	}
	return itemOutcome{records: 1}, nil
}

// tellDocument tells each lane whose pack holds the document, which the item index finds. A lane
// stays on the version it pinned, so a new one never changes a session silently, and policy decides
// whether the lane goes on (FR-DOC-6).
func (r *fileRecorder) tellDocument(record planning.DocumentRecord, version planning.DocumentVersion) error {
	touched := reach{lanes: map[string]int64{}}
	if err := addPinnedLanes(r.tx, &touched, contextpacks.KindDocument, record.ID); err != nil {
		return err
	}
	if err := addRunningLanes(r.tx, &touched); err != nil {
		return err
	}
	ref := planning.ContractKey(record.ID, version.Version)
	return tellPinned(r.tx, touched, r.windows, pinnedNotice{
		kind: messages.KindDocumentNotice, ref: ref, body: contextpacks.DocumentNotice(record.Title, ref), reason: "document version " + ref,
	})
}

// requirements records each requirement that changed, as a new version of that one.
func (r *fileRecorder) requirements() (itemOutcome, error) {
	var outcome itemOutcome
	for _, spec := range r.item.Requirements.Requirements {
		key := r.item.Key
		key.NativeID = spec.ID
		next := planning.Requirement{
			Version: 1, Key: spec.ID, Text: spec.Text, Source: r.item.Source, Digest: planning.RequirementDigest(spec.ID, spec.Text),
		}
		id, known, err := r.recordID(planning.SubjectRequirement, key)
		if err != nil {
			return itemOutcome{}, err
		}
		if known {
			current, err := r.tx.Planning().Requirement(spec.ID)
			if err != nil {
				return itemOutcome{}, err
			}
			if current.Digest == next.Digest {
				continue
			}
			held, stands, err := r.standing(planning.SubjectRequirement, spec.ID, current.Source)
			if err != nil {
				return itemOutcome{}, err
			}
			if !stands {
				outcome.conflicts += held.conflicts
				outcome.problem = strings.TrimPrefix(outcome.problem+"; "+held.problem, "; ")
				continue
			}
			next.ID, next.Version = id, current.Version+1
		} else if next.ID, err = r.newRecord(planning.SubjectRequirement, key); err != nil {
			return itemOutcome{}, err
		}
		if err := r.writer().requirement(next); err != nil {
			return itemOutcome{}, err
		}
		outcome.records++
	}
	return outcome, nil
}
