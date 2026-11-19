package adoption

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/pathmask"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/practicerun"
)

// PracticesLimit is how long the check of one repository may run in a dry run. A history too large to scan in that
// time leaves the repository with a note, and the plan goes on.
const PracticesLimit = 30 * time.Second

// CheckPractices reads the practices of the clone at dir for a dry run (FR-PRAC-6) and returns the repository with
// them. The check reads the base branch a registration would record, and knows no forge binding, the default merge
// authority, and no earlier scan, so the secret scan reads the whole history. It records nothing.
//
// A clone the check cannot read has no practices and a note that says why, with no path of the machine in it, and so
// has one whose check runs past limit. A request that ends is an error, because the plan is no longer wanted. Sources
// with no reads to make leave the repository as it was.
func CheckPractices(ctx context.Context, sources practicerun.Sources, repository planning.PlannedRepository, dir, base string, limit time.Duration) (planning.PlannedRepository, error) {
	if sources.Snapshots == nil || sources.Secrets == nil || sources.Clock == nil {
		return repository, nil
	}
	if base == "" {
		repository.PracticesNote = "the practices were not read: no base branch is known for this clone"
		return repository, nil
	}
	checking, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	result, err := practicerun.Run(checking, sources, practicerun.Request{
		Repository: cmp.Or(repository.ID, repository.Name), Dir: dir, BaseBranch: base, MergeAuthority: ledger.PersonMerges,
	})
	switch {
	case ctx.Err() != nil:
		return repository, ctx.Err()
	case errors.Is(checking.Err(), context.DeadlineExceeded):
		repository.PracticesNote = fmt.Sprintf("the practices were not read within %g s", limit.Seconds())
		return repository, nil
	case err != nil:
		repository.PracticesNote = practicerun.Clip("the practices were not read: " + pathmask.Text(err.Error(), dir))
		return repository, nil
	}
	for i, status := range result.Practices {
		result.Practices[i].Reason = practicerun.Clip(pathmask.Text(status.Reason, dir))
	}
	repository.Practices = &result
	return repository, nil
}
