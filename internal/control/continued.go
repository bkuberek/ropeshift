package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// Checking makes intake read whether the branch each work order continues is there (ADR-0023), through the
// checkouts and the git a claim's start reads. Without it intake reads no branch, and a claim's start
// holds work whose branch is gone all the same.
func (i *Intake) Checking(starts *Starts) *Intake {
	i.starts = starts
	return i
}

// missingBranches finds the work orders of a batch that continue a branch their repository holds neither
// on its remote nor in its checkout, by the ID the file gives each, with what a person reads of it. It
// reads git before any transaction, because a transaction asks no source (R17). A repository this machine
// keeps no checkout of, or whose git cannot answer, says nothing here, and a claim's start reads it again.
func (i *Intake) missingBranches(ctx context.Context, scope ledger.WorkspaceID, batch ports.ChangeBatch) (map[string]string, error) {
	if i.starts == nil {
		return nil, nil
	}
	type continued struct{ order, branch, repo, instance string }
	var wanted []continued
	for _, item := range batch.Items {
		for _, spec := range item.Contract.WorkOrders {
			if spec.Continues != "" {
				wanted = append(wanted, continued{order: spec.ID, branch: spec.Continues, repo: spec.Repo, instance: item.Key.Instance})
			}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	ids := map[string]string{}
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		for _, one := range wanted {
			if one.repo == "" || one.repo == planning.CurrentRepository {
				ids[one.repo] = one.instance
				continue
			}
			record, err := tx.Repositories().Named(one.repo)
			switch {
			case errors.Is(err, ports.ErrNotFound):
			case err != nil:
				return err
			case !record.Removed():
				ids[one.repo] = record.ID
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	missing := map[string]string{}
	for _, one := range wanted {
		id, known := ids[one.repo]
		if !known {
			continue
		}
		path, err := i.starts.Checkout(ctx, scope, id)
		if err != nil {
			continue
		}
		if _, err := i.starts.Bases.Branch(ctx, path, one.branch); errors.Is(err, ports.ErrNotFound) {
			name := one.repo
			if name == "" || name == planning.CurrentRepository {
				name = "this repository"
			}
			missing[one.order] = fmt.Sprintf("work order %s continues branch %s, which %s holds neither on its remote nor in its checkout", one.order, one.branch, strings.TrimSpace(name))
		}
	}
	return missing, nil
}
