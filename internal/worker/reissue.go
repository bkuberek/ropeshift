package worker

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// reissueResumes sends again what an earlier build's resumes left refused, once in a process. That build
// named no project in a woken lane's resume, so on an installation of several projects the ledger refused
// the resume and fenced every later write of its generation, while the worker went on renewing a claim the
// ledger shows no lane under. Where the ledger still awaits that generation under the same claim, the
// resume goes again with the claim's project, followed in order by every write of the lane the ledger
// refused under that claim, so the ledger records what the lane did and the claim ends the way the lane
// did. The ledger answers each copy by its usual rules, and a copy it refuses stays refused.
//
// What the journal holds goes first, because a copy must not go ahead of an unanswered write of its lane.
// A pass that still finds one leaves the copies to the next pass.
func (w *Worker) reissueResumes(ctx context.Context) error {
	if w.reissued.Load() {
		return nil
	}
	refused, err := w.journal.Refused(ctx)
	if err != nil {
		return err
	}
	leases, err := w.desiredLeases(ctx)
	if err != nil {
		return err
	}
	found := reissues(refused, leases, w.epoch())
	if len(found) == 0 {
		w.reissued.Store(true)
		return nil
	}
	if err := w.Upload(ctx); err != nil {
		return err
	}
	done := true
	for _, reissue := range found {
		stored, err := w.journal.Reissue(ctx, reissue)
		if err != nil {
			return err
		}
		done = done && stored
		if stored {
			w.config.Notice(fmt.Sprintf("lane %s: sent again %d writes the ledger refused after a resume that named no project",
				reissue.Lane, len(reissue.Copies)))
		}
	}
	w.reissued.Store(done)
	return w.Upload(ctx)
}

// reissues finds each refused resume that named no project and that the ledger still awaits: a lease of
// this worker holds the resume's claim on its lane and waits for the generation it began. Each takes that
// resume and every later refused event of the lane under the claim, in order, under this epoch, with the
// claim's project where an event names none.
func reissues(refused []ledger.UploadEvent, leases []ledger.DesiredLease, epoch int64) []ports.Reissue {
	awaiting := map[string]ledger.DesiredLease{}
	for _, lease := range leases {
		if lease.Resume != nil && lease.Project != "" && lease.ClaimID != "" {
			awaiting[lease.ClaimID] = lease
		}
	}
	var out []ports.Reissue
	for i := range refused {
		resumed := refused[i]
		lease, ok := awaiting[resumed.ClaimID]
		if !ok || resumed.Type != ledger.UploadLaneResumed || resumed.Project != "" || resumed.Lane != lease.Lane ||
			resumed.Generation != lease.Resume.Generation+1 {
			continue
		}
		delete(awaiting, resumed.ClaimID)
		reissue := ports.Reissue{Lane: resumed.Lane, Project: lease.Project}
		for _, event := range refused[i:] {
			if event.Lane != resumed.Lane || event.ClaimID != resumed.ClaimID {
				continue
			}
			if event.Project == "" {
				event.Project = lease.Project
			}
			event.Epoch = epoch
			reissue.Copies = append(reissue.Copies, event)
		}
		out = append(out, reissue)
	}
	return out
}
