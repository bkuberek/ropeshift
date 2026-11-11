package control

import (
	"cmp"
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// offPlan keeps intake to the plan of record (ADR-0022). When the project names a planning repository and
// the batch is of another repository, each plan file it holds is a problem rather than a record, so a
// misplaced plan is visible and never read. A project that names none reads plan files from every
// repository, and a file nobody could read stays the problem it was.
func (i *Intake) offPlan(ctx context.Context, scope ledger.WorkspaceID, instance string, batch ports.ChangeBatch) (ports.ChangeBatch, error) {
	if len(batch.Items)+len(batch.Records) == 0 {
		return batch, nil
	}
	var planner, here ledger.Repository
	var named bool
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		all, err := tx.Repositories().Repositories()
		if err != nil {
			return err
		}
		for _, record := range all {
			if record.Planning && !record.Removed() {
				planner, named = record, true
			}
			if record.ID == instance {
				here = record
			}
		}
		return nil
	})
	if err != nil || !named || planner.ID == instance {
		return batch, err
	}
	misplaced := func(path, source, what string) planning.SourceProblem {
		return planning.SourceProblem{Path: path, Source: source, Detail: fmt.Sprintf(
			"%v: %s is a plan file in repository %s, and the project's planning repository is %s; intake reads plan files only from the planning repository",
			planning.ErrNotPlanningRepository, what, cmp.Or(here.Name, instance), cmp.Or(planner.Name, planner.ID))}
	}
	out := ports.ChangeBatch{Problems: batch.Problems, Cursor: batch.Cursor}
	for _, record := range batch.Records {
		what := "goal " + record.Key.NativeID
		switch head, document := record.Head(); {
		case record.Plan != nil:
			what = "plan " + record.Key.NativeID
		case document:
			what = head.Ref()
		}
		out.Problems = append(out.Problems, misplaced(record.Path, record.Source, what))
	}
	for _, item := range batch.Items {
		out.Problems = append(out.Problems, misplaced(item.Path, item.Source, "contract "+item.Contract.ID))
	}
	return out, nil
}

// offPlanOf is offPlan for a source whose repository the caller has not described.
func (i *Intake) offPlanOf(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource, batch ports.ChangeBatch) (ports.ChangeBatch, error) {
	description, err := source.Describe(ctx)
	if err != nil {
		return batch, err
	}
	return i.offPlan(ctx, scope, description.Instance, batch)
}
