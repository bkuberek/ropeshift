package contract

import (
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReviewWorkChecks are what the ledger promises about the review of a completed change (FR-REVIEW-1,
// FR-REVIEW-2, FR-REVIEW-3).
var ReviewWorkChecks = []Check{
	{"RW1", "a review keeps what it judges through the projection, a replay, and a rebuild", checkReviewRoundTrips},
}

// aReviewOf puts the review of an accepted shape of work in the ledger directly, as the store sees it.
func (r *Rig) aReviewOf(s ports.Store, of workorders.WorkOrder, fence int64) (workorders.WorkOrder, error) {
	review, err := workorders.NewReview(r.ID("review"), of, fence)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	err = s.Write(r.Ctx, r.Scope, "review.make", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().Insert(review); err != nil {
			return err
		}
		event, err := workorders.CreatedEvent(review, r.Actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	return review, err
}

func checkReviewRoundTrips(r *Rig) error {
	s := r.Store()
	of := workorders.WorkOrder{
		ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Add the retry budget", RepositoryID: "repo-a", ContractID: "c-1", ContractVersion: 2,
		MaxAttempts: 2,
	}
	review, err := r.aReviewOf(s, of, 5)
	if err != nil {
		return err
	}
	read := func(when string) error {
		got, err := r.WorkOrder(s, review.ID)
		if err != nil {
			return err
		}
		if diff := workorders.Diff(got, review); len(diff) != 0 {
			return Violatedf("%s the review differs in %v: %+v", when, diff, got)
		}
		return nil
	}
	if err := read("after it was made,"); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	return read("after a rebuild,")
}
