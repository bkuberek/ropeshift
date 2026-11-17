package worker

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// reissueResumes sends again what an earlier build's resumes left refused, once in a process. That build
// named no project in a woken lane's resume, so on an installation of several projects the ledger refused
// the resume and fenced every later write of its generation, while the worker went on renewing a claim the
// ledger shows no lane under. Where the ledger still awaits that generation under the same claim, the
// resume goes again with the claim's project, followed in order by every write of the lane the ledger
// refused under that claim, so the ledger records what the lane did and the claim ends the way the lane
// did. The ledger answers each copy by its usual rules, and a copy it refuses stays refused, except an end
// the lane's own park made moot (forgetMoot).
//
// What the journal holds goes first, before the refused events are read, and no other upload runs until
// the copies are stored: a write of the lane the earlier build journaled and never uploaded is then either
// refused and among the copies, or still unanswered, which the journal will not put a copy ahead of. A
// pass that finds one unanswered leaves the copies to the next pass.
func (w *Worker) reissueResumes(ctx context.Context) error {
	if w.reissued.Load() {
		return nil
	}
	w.uploading.Lock()
	defer w.uploading.Unlock()
	if err := w.uploadHeld(ctx); err != nil {
		return err
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
		return w.forgetMoot(ctx, refused)
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
	if err := w.uploadHeld(ctx); err != nil {
		return err
	}
	again, err := w.journal.Refused(ctx)
	if err != nil {
		return err
	}
	return w.forgetMoot(ctx, again)
}

// forgetMoot forgets the refusal of a lane's end that its parked commit made moot. A worker that found its
// park commit refused ended the lane, because no wake could reach it. Sent again after the resume, the park
// stands and the end is fenced, since the ledger holds the lane parked and its claim given up: it is a
// write nobody should hear, and its refusal would stay for good. The park the end answered must be the
// lane's own, of the same generation, answered and not refused. An end whose park is refused again, or
// whose lane parked no commit, stays refused, so what still matters is never dropped.
func (w *Worker) forgetMoot(ctx context.Context, refused []ledger.UploadEvent) error {
	through, err := w.journal.Through(ctx)
	if err != nil {
		return err
	}
	parks := map[string]*lanes.ParkProgress{}
	for _, event := range refused {
		if _, read := parks[event.Lane]; read || event.Type != ledger.UploadLaneEnded {
			continue
		}
		record, err := w.journal.Lane(ctx, event.Lane)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		parks[event.Lane] = record.Parking
	}
	moot := mootEnds(refused, parks, through)
	if len(moot) == 0 {
		return nil
	}
	seqs := make([]int64, len(moot))
	for i, event := range moot {
		seqs[i] = event.Seq
	}
	if err := w.journal.Forget(ctx, seqs); err != nil {
		return err
	}
	for _, event := range moot {
		w.config.Notice(fmt.Sprintf("lane %s: forgot a refused end that the lane's park, which the ledger holds, made moot", event.Lane))
	}
	return nil
}

// mootEnds picks the refused ends of lanes whose parked commit stands: the commit a lane's record names is
// answered, at or below through, is not itself refused, comes before the end, and is of the end's
// generation.
func mootEnds(refused []ledger.UploadEvent, parks map[string]*lanes.ParkProgress, through int64) []ledger.UploadEvent {
	standing := make(map[int64]bool, len(refused))
	for _, event := range refused {
		standing[event.Seq] = true
	}
	var out []ledger.UploadEvent
	for _, event := range refused {
		park := parks[event.Lane]
		if event.Type == ledger.UploadLaneEnded && park != nil && park.Seq != 0 && park.Seq < event.Seq && park.Seq <= through &&
			!standing[park.Seq] && park.Generation == event.Generation {
			out = append(out, event)
		}
	}
	return out
}

// reissues finds each refused resume that named no project and that the ledger still awaits: a lease of
// this worker holds the resume's claim on its lane, for the same work order at the same fence, and waits
// for the generation it began. Each takes that resume and every later refused event of the lane under
// that lease, in order, under this epoch, with the claim's project where an event names none.
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
		if !ok || resumed.Type != ledger.UploadLaneResumed || resumed.Project != "" || !underLease(resumed, lease) ||
			resumed.Generation != lease.Resume.Generation+1 {
			continue
		}
		delete(awaiting, resumed.ClaimID)
		reissue := ports.Reissue{Lane: resumed.Lane, Project: lease.Project}
		for _, event := range refused[i:] {
			if !underLease(event, lease) {
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

// underLease says an event is a lane's write under a lease: the same lane, claim, work order, and fence.
func underLease(event ledger.UploadEvent, lease ledger.DesiredLease) bool {
	return event.Lane == lease.Lane && event.ClaimID == lease.ClaimID && event.WorkOrder == lease.WorkOrder && event.Fence == lease.Fence
}
