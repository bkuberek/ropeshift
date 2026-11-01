package control

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
)

// recordFile keeps what a goal, plan, or requirements file says. It is one transaction, and
// a file that says what the ledger holds appends nothing.
func (i *Intake) recordFile(ctx context.Context, scope ledger.WorkspaceID, item ports.RecordItem) (itemOutcome, error) {
	var outcome itemOutcome
	err := i.store.Write(ctx, scope, "intake.file", func(tx ports.WriteTx) error {
		r := &fileRecorder{tx: tx, item: item}
		var err error
		switch {
		case item.Goal != nil:
			outcome, err = r.goal()
		case item.Plan != nil:
			outcome, err = r.plan()
		default:
			outcome, err = r.requirements()
		}
		crashpoint.Hit("intake.file", "recorded")
		return err
	})
	return outcome, err
}

type fileRecorder struct {
	tx   ports.WriteTx
	item ports.RecordItem
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
	} else if id, err = r.newRecord(planning.SubjectGoal, r.item.Key); err != nil {
		return itemOutcome{}, err
	}
	next.ID = id
	return itemOutcome{records: 1}, r.writer().goal(next)
}

func sameGoal(a, b planning.Goal) bool {
	return a.Title == b.Title && a.Statement == b.Statement && slices.Equal(a.SuccessMeasures, b.SuccessMeasures)
}

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
	id, known, err := r.recordID(planning.SubjectPlan, r.item.Key)
	if err != nil {
		return itemOutcome{}, err
	}
	if !known {
		if next.ID, err = r.newRecord(planning.SubjectPlan, r.item.Key); err != nil {
			return itemOutcome{}, err
		}
		return itemOutcome{records: 1}, r.writer().plan(next)
	}
	current, err := r.tx.Planning().Plan(id)
	if err != nil {
		return itemOutcome{}, err
	}
	if current.Title == next.Title && current.GoalID == next.GoalID {
		return itemOutcome{}, nil
	}
	next.ID = id
	return itemOutcome{records: 1}, r.writer().changedPlan(next)
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
