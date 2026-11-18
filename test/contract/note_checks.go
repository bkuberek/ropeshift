package contract

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// noteOf is what the review's own lane would say of a security point, as the seq-th note of its report.
func (a aReview) noteOf(seq int64, point workorders.NotePoint) workorders.Note {
	return workorders.Note{
		ReviewNote: workorders.ReviewNote{Point: point, Reasons: "because " + string(point), Citations: []string{"main.go:12"}},
		Review:     a.Review.ID, WorkOrder: a.Work.ID, Attempt: a.Review.ReviewsFence, Commit: a.Head, Seq: seq,
		RedactionRules: ledger.CIRedactionRulesVersion,
	}
}

// recordNotes writes notes as the store alone sees them, from a lane, with the event a change must carry.
func (r *Rig) recordNotes(s ports.Store, lane string, notes ...workorders.Note) error {
	return s.Write(r.Ctx, r.Scope, "note.record", func(tx ports.WriteTx) error {
		for _, n := range notes {
			if err := tx.Evidence().RecordNote(n, lane); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: workorders.EventEvidenceRecorded, Actor: r.Actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: notes[0].WorkOrder},
		})
		return err
	})
}

func (r *Rig) notesOf(s ports.Store, id string) ([]workorders.Note, error) {
	var out []workorders.Note
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Evidence().Notes(id)
		return err
	})
	return out, err
}

func checkNoteRoundTrips(r *Rig) error {
	s := r.Store()
	a, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	first, second := a.noteOf(0, workorders.PointSecrets), a.noteOf(1, workorders.PointAuthority)
	second.Citations = nil
	if err := r.recordNotes(s, a.Holder.Lane, first, second); err != nil {
		return err
	}
	got, err := r.notesOf(s, a.Work.ID)
	if err != nil {
		return err
	}
	if len(got) != 2 {
		return Violatedf("the work holds %d notes, want 2", len(got))
	}
	for i, want := range []workorders.Note{first, second} {
		row := got[i]
		if row.CreatedAt == 0 {
			return Violatedf("note %d has no time", row.Seq)
		}
		row.CreatedAt = 0
		if row.Point != want.Point || row.Reasons != want.Reasons || !slices.Equal(row.Citations, want.Citations) || row.Review != want.Review ||
			row.WorkOrder != want.WorkOrder || row.Attempt != want.Attempt || row.Commit != want.Commit || row.Seq != want.Seq ||
			row.RedactionRules != ledger.CIRedactionRulesVersion {
			return Violatedf("the note read back as %+v, not %+v", row, want)
		}
	}
	if other, err := r.notesOf(s, a.Review.ID); err != nil || len(other) != 0 {
		return Violatedf("the review work order itself holds %d notes: %v", len(other), err)
	}
	return nil
}

func checkNoteFromTheReviewOnly(r *Rig) error {
	s := r.Store()
	a, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	other, err := r.judgingReview(s, criterionOne)
	if err != nil {
		return err
	}
	for name, refused := range map[string]struct {
		lane   string
		change func(*workorders.Note)
		want   error
	}{
		"the reviewed lane":               {a.Worker, func(*workorders.Note) {}, workorders.ErrForbidden},
		"a lane of another review":        {other.Holder.Lane, func(*workorders.Note) {}, workorders.ErrForbidden},
		"a lane nobody started":           {"no-such-lane", func(*workorders.Note) {}, workorders.ErrForbidden},
		"a review that names other work":  {a.Holder.Lane, func(n *workorders.Note) { n.WorkOrder = other.Work.ID }, workorders.ErrForbidden},
		"a review nobody made":            {a.Holder.Lane, func(n *workorders.Note) { n.Review = "no-such-review" }, workorders.ErrForbidden},
		"a note that names no point":      {a.Holder.Lane, func(n *workorders.Note) { n.Point = "" }, workorders.ErrInvalid},
		"a note that gives no reasons":    {a.Holder.Lane, func(n *workorders.Note) { n.Reasons = "" }, workorders.ErrInvalid},
		"a note that names no commit":     {a.Holder.Lane, func(n *workorders.Note) { n.Commit = "" }, workorders.ErrInvalid},
		"a note with no rule set version": {a.Holder.Lane, func(n *workorders.Note) { n.RedactionRules = 0 }, workorders.ErrInvalid},
		"a note placed before the first":  {a.Holder.Lane, func(n *workorders.Note) { n.Seq = -1 }, workorders.ErrInvalid},
	} {
		n := a.noteOf(0, workorders.PointInjection)
		refused.change(&n)
		if err := r.recordNotes(s, refused.lane, n); !errors.Is(err, refused.want) {
			return Violatedf("%s: %v", name, err)
		}
	}
	for _, id := range []string{a.Work.ID, other.Work.ID, a.Review.ID} {
		if got, err := r.notesOf(s, id); err != nil || len(got) != 0 {
			return Violatedf("a refused note left %d rows on %s: %v", len(got), id, err)
		}
	}
	if err := r.recordNotes(s, a.Holder.Lane, a.noteOf(0, workorders.PointSecrets)); err != nil {
		return err
	}
	if err := r.recordNotes(s, a.Holder.Lane, a.noteOf(0, workorders.PointDependencies)); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a second note in one place of one review: %v", err)
	}
	got, err := r.notesOf(s, a.Work.ID)
	if err != nil || len(got) != 1 || got[0].Point != workorders.PointSecrets {
		return Violatedf("the work holds %+v: %v", got, err)
	}
	return nil
}
