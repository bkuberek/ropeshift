package contract

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// VerdictChecks hold what the store promises about a review's verdicts (FR-REVIEW-4, FR-REVIEW-5): each is
// bound to the commit and the attempt it judged, and none comes from a lane that did the work it judges.
var VerdictChecks = []Check{
	{"V1", "a verdict keeps what the review said, bound to the commit and the attempt it judged", checkVerdictRoundTrips},
	{"V2", "the store refuses a verdict from any lane of the work it judges, whatever its generation", checkVerdictFromTheWorkersLane},
	{"V3", "the store refuses a verdict that does not come from the review that names the work", checkVerdictFromElsewhere},
	{"V4", "the store refuses a verdict that is not whole, and a second verdict for one criterion of one review", checkVerdictMustBeWhole},
	{"V5", "a note on why work went back keeps the newest, and none reads as not found", checkReworkNoteKeepsTheNewest},
}

// aReview is a work order that waits for its review, with the review claimed and running.
type aReview struct {
	Work   workorders.WorkOrder
	Review workorders.WorkOrder
	Holder workorders.Holder
	Head   string
	// Worker is the lane that did the work under review.
	Worker string
}

// judgingReview leaves a change that completed at a commit, and its review running on a lane of its own.
func (r *Rig) judgingReview(s ports.Store, criteria ...string) (aReview, error) {
	head := r.ID("commit")
	id, err := r.AwaitingReview(s, criteria, LaneFinished(head))
	if err != nil {
		return aReview{}, err
	}
	work, err := r.WorkOrder(s, id)
	if err != nil {
		return aReview{}, err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return aReview{}, err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return aReview{}, err
	}
	var worker string
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Lanes().Rows()
		for _, row := range rows {
			if row.WorkOrder == id {
				worker = row.ID
			}
		}
		return err
	})
	if err != nil {
		return aReview{}, err
	}
	if worker == "" {
		return aReview{}, Violatedf("the reviewed work ran no lane, so this proves nothing")
	}
	return aReview{Work: work, Review: review, Holder: holder, Head: head, Worker: worker}, nil
}

// verdictOf is what the review's own lane would say of a criterion.
func (a aReview) verdictOf(criterion string, kind workorders.VerdictKind) workorders.Verdict {
	return workorders.Verdict{
		CriterionVerdict: workorders.CriterionVerdict{
			Criterion: criterion, Verdict: kind, Reasons: "because " + criterion, Citations: []string{"main.go:12", "FR-1"},
		},
		Review: a.Review.ID, WorkOrder: a.Work.ID, Attempt: a.Review.ReviewsFence, Commit: a.Head,
		Lane: a.Holder.Lane, Generation: a.Holder.Generation, Fence: a.Holder.Fence + 1,
	}
}

// recordVerdicts writes verdicts as the store alone sees them, with the event a change must carry.
func (r *Rig) recordVerdicts(s ports.Store, verdicts ...workorders.Verdict) error {
	return s.Write(r.Ctx, r.Scope, "verdict.record", func(tx ports.WriteTx) error {
		for _, v := range verdicts {
			if err := tx.Evidence().RecordVerdict(v); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: workorders.EventEvidenceRecorded, Actor: r.Actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: verdicts[0].WorkOrder},
		})
		return err
	})
}

func (r *Rig) verdictsOf(s ports.Store, id string) ([]workorders.Verdict, error) {
	var out []workorders.Verdict
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Evidence().Verdicts(id)
		return err
	})
	return out, err
}

func checkVerdictRoundTrips(r *Rig) error {
	s := r.Store()
	a, err := r.judgingReview(s, criterionOne, criterionTwo)
	if err != nil {
		return err
	}
	pass, fail := a.verdictOf(criterionOne, workorders.VerdictPass), a.verdictOf(criterionTwo, workorders.VerdictFail)
	if err := r.recordVerdicts(s, pass, fail); err != nil {
		return err
	}
	got, err := r.verdictsOf(s, a.Work.ID)
	if err != nil {
		return err
	}
	if len(got) != 2 {
		return Violatedf("the work holds %d verdicts, want 2", len(got))
	}
	for i, want := range []workorders.Verdict{pass, fail} {
		row := got[i]
		if row.CreatedAt == 0 {
			return Violatedf("the verdict for %s has no time", row.Criterion)
		}
		row.CreatedAt = 0
		if row.Criterion != want.Criterion || row.Verdict != want.Verdict || row.Reasons != want.Reasons || !slices.Equal(row.Citations, want.Citations) ||
			row.Review != want.Review || row.WorkOrder != want.WorkOrder || row.Attempt != want.Attempt || row.Commit != want.Commit ||
			row.Lane != want.Lane || row.Generation != want.Generation || row.Fence != want.Fence {
			return Violatedf("the verdict read back as %+v, not %+v", row, want)
		}
	}
	if other, err := r.verdictsOf(s, a.Review.ID); err != nil || len(other) != 0 {
		return Violatedf("the review work order itself holds %d verdicts: %v", len(other), err)
	}
	return nil
}

func checkVerdictFromTheWorkersLane(r *Rig) error {
	s := r.Store()
	a, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	for _, generation := range []int64{1, 2, 7} {
		v := a.verdictOf(criterionOne, workorders.VerdictPass)
		v.Lane, v.Generation = a.Worker, generation
		if err := r.recordVerdicts(s, v); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a verdict from the reviewed lane at generation %d: %v", generation, err)
		}
	}
	if got, err := r.verdictsOf(s, a.Work.ID); err != nil || len(got) != 0 {
		return Violatedf("a refused verdict left %d rows: %v", len(got), err)
	}
	return nil
}

func checkVerdictFromElsewhere(r *Rig) error {
	s := r.Store()
	a, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	other, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	for name, change := range map[string]func(*workorders.Verdict){
		"a lane of another review":                 func(v *workorders.Verdict) { v.Lane = other.Holder.Lane },
		"a lane nobody started":                    func(v *workorders.Verdict) { v.Lane = "no-such-lane" },
		"a review that names other work":           func(v *workorders.Verdict) { v.WorkOrder = other.Work.ID },
		"the reviewed work order as the review":    func(v *workorders.Verdict) { v.Review = a.Work.ID },
		"a work order nothing reviews as the work": func(v *workorders.Verdict) { v.WorkOrder = a.Review.ID },
		"a review nobody made":                     func(v *workorders.Verdict) { v.Review = "no-such-review" },
	} {
		v := a.verdictOf(criterionOne, workorders.VerdictPass)
		change(&v)
		if err := r.recordVerdicts(s, v); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s: %v", name, err)
		}
	}
	for _, id := range []string{a.Work.ID, other.Work.ID, a.Review.ID} {
		if got, err := r.verdictsOf(s, id); err != nil || len(got) != 0 {
			return Violatedf("a refused verdict left %d rows on %s: %v", len(got), id, err)
		}
	}
	return nil
}

func checkVerdictMustBeWhole(r *Rig) error {
	s := r.Store()
	a, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	for name, change := range map[string]func(*workorders.Verdict){
		"a verdict nobody offers": func(v *workorders.Verdict) { v.Verdict = "maybe" },
		"no reasons":              func(v *workorders.Verdict) { v.Reasons = "" },
		"no criterion":            func(v *workorders.Verdict) { v.Criterion = "" },
		"no commit":               func(v *workorders.Verdict) { v.Commit = "" },
	} {
		v := a.verdictOf(criterionOne, workorders.VerdictPass)
		change(&v)
		if err := r.recordVerdicts(s, v); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("%s: %v", name, err)
		}
	}
	first := a.verdictOf(criterionOne, workorders.VerdictPass)
	if err := r.recordVerdicts(s, first); err != nil {
		return err
	}
	second := a.verdictOf(criterionOne, workorders.VerdictFail)
	if err := r.recordVerdicts(s, second); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a second verdict for one criterion of one review: %v", err)
	}
	got, err := r.verdictsOf(s, a.Work.ID)
	if err != nil || len(got) != 1 || got[0].Verdict != workorders.VerdictPass {
		return Violatedf("the work holds %+v: %v", got, err)
	}
	return nil
}

func checkReworkNoteKeepsTheNewest(r *Rig) error {
	s := r.Store()
	id := r.ID("wo")
	read := func() (workorders.ReworkNote, error) {
		var note workorders.ReworkNote
		err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			var err error
			note, err = tx.Evidence().Reworked(id)
			return err
		})
		return note, err
	}
	if _, err := read(); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a work order that never went back: %v", err)
	}
	note := func(attempt int64, review, reason string) error {
		return s.Write(r.Ctx, r.Scope, "rework.note", func(tx ports.WriteTx) error {
			if err := tx.Evidence().RecordRework(workorders.ReworkNote{WorkOrder: id, Attempt: attempt, Review: review, Reason: reason}); err != nil {
				return err
			}
			_, err := tx.Append(ledger.EventDraft{Type: workorders.EventEvidenceRecorded, Actor: r.Actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: id}})
			return err
		})
	}
	if err := note(4, "", "the first reason"); err != nil {
		return err
	}
	if err := note(9, "rv-2", "the review found it"); err != nil {
		return err
	}
	if err := note(9, "rv-2", "the review found it, said again"); err != nil {
		return err
	}
	got, err := read()
	if err != nil {
		return err
	}
	if got.Attempt != 9 || got.Review != "rv-2" || got.Reason != "the review found it, said again" || got.CreatedAt == 0 || got.WorkOrder != id {
		return Violatedf("the newest note reads %+v", got)
	}
	return nil
}
