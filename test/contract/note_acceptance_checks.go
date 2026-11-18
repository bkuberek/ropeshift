package contract

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// commitNames matches the commits the rig names, which differ between two works that settle alike.
var commitNames = regexp.MustCompile(`commit-\d+`)

// aToken is shaped like a GitHub token, built here so that no file holds one.
var aToken = "ghp_" + strings.Repeat("a1", 12)

// twoNotes are what a review that found two security concerns reports besides its verdicts.
func twoNotes() []workorders.ReviewNote {
	return []workorders.ReviewNote{
		{Point: workorders.PointSecrets, Reasons: "A token " + aToken + " is added to config.yaml.", Citations: []string{"config.yaml:4", "key " + aToken}},
		{Point: workorders.PointAuthority, Reasons: "The workflow\n  gains write permission."},
	}
}

// eventsHolding counts the events of the ledger whose data holds text, and names the types of the first of them.
func (r *Rig) eventsHolding(s ports.Store, text string) (int, string, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return 0, "", err
	}
	count, first := 0, ""
	for _, event := range events {
		// The event that ends a lane carries the lane's report as a payload, which its data holds encoded.
		var change struct {
			Payload []byte `json:"payload"`
		}
		_ = json.Unmarshal(event.Data, &change)
		if strings.Contains(string(event.Data), text) || strings.Contains(string(change.Payload), text) {
			if count == 0 {
				first = event.Type
			}
			count++
		}
	}
	return count, first, nil
}

func checkNotesReachNoEventUnredacted(r *Rig) error {
	s := r.Store()
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSaysWithNotes(s, id, twoNotes(), say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if count, first, err := r.eventsHolding(s, aToken); err != nil || count != 0 {
		return Violatedf("%d events of a review's end hold a credential of its notes, the first a %s: %v", count, first, err)
	}
	if count, _, err := r.eventsHolding(s, "[redacted: github_token]"); err != nil || count == 0 {
		return Violatedf("the end of a review that left notes records none of them: %v", err)
	}
	return nil
}

func checkCanceledLaneNotesReachNoEvent(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := accepted(s.report(s.working)); err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	result := laneResult(workorders.OutcomeFailed, workorders.ReasonFenced)
	result.Notes = twoNotes()
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := accepted(s.upload(ledger.UploadLaneEnded, raw)); err != nil {
		return err
	}
	if err := s.expectEnded(workorders.ReasonCanceled); err != nil {
		return err
	}
	if count, first, err := r.eventsHolding(s.store, aToken); err != nil || count != 0 {
		return Violatedf("%d events of a canceled lane's end hold a credential of its notes, the first a %s: %v", count, first, err)
	}
	if count, _, err := r.eventsHolding(s.store, `"notes"`); err != nil || count != 0 {
		return Violatedf("%d events of a canceled lane's end keep its notes: %v", count, err)
	}
	return nil
}

// reviewSaysWithNotes ends the open review of a work order with its verdicts and its security notes.
func (r *Rig) reviewSaysWithNotes(s ports.Store, id string, notes []workorders.ReviewNote, said ...workorders.CriterionVerdict) error {
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Verdicts, result.Notes = said, notes
	return r.reviewEnds(s, id, result)
}

func checkReviewKeepsItsNotes(r *Rig) error {
	s := r.Store()
	id, head, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	review, err := r.openReviewOf(s, id)
	if err != nil {
		return err
	}
	reviewed, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	var writes []string
	stepped := r.StoreWith(func(op, name string) {
		if name == StepBefore {
			writes = append(writes, op)
		}
	})
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Verdicts, result.Notes = []workorders.CriterionVerdict{say(criterionOne, workorders.VerdictPass)}, twoNotes()
	if err := r.NewOps(stepped).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); err != nil {
		return err
	}
	if !slices.Equal(writes, []string{"lane.end"}) {
		return Violatedf("ending the review took the transactions %v, and its notes are kept in the one that ends it", writes)
	}
	got, err := r.notesOf(s, id)
	if err != nil {
		return err
	}
	if len(got) != 2 {
		return Violatedf("the work holds %d notes, want 2", len(got))
	}
	for i, n := range got {
		if n.Review != review.ID || n.WorkOrder != id || n.Attempt != reviewed.Fence || n.Commit != head || n.Seq != int64(i) {
			return Violatedf("note %d reads %+v, at attempt %d and commit %s", i, n, reviewed.Fence, head)
		}
		if strings.Contains(n.Reasons, aToken) || strings.Contains(strings.Join(n.Citations, " "), aToken) {
			return Violatedf("note %d keeps a credential: %+v", i, n)
		}
	}
	if got[0].Point != workorders.PointSecrets || got[0].Reasons != "A token [redacted: github_token] is added to config.yaml." ||
		!slices.Equal(got[0].Citations, []string{"config.yaml:4", "key [redacted: github_token]"}) || got[1].Point != workorders.PointAuthority {
		return Violatedf("the notes read %+v", got)
	}
	view, err := r.verdictsCounted(s, id)
	if err != nil {
		return err
	}
	if len(view.Notes) != 2 || view.Notes[0].Seq != 0 || view.Notes[1].Seq != 1 {
		return Violatedf("the standing shows the notes %+v", view.Notes)
	}
	return r.CheckProjection(s)
}

func checkNotesDecideNothing(r *Rig) error {
	s := r.Store()
	type outcome struct {
		state        workorders.State
		why          string
		integrations int
		approvals    []ledger.ApproverKind
		reworked     bool
	}
	settle := func(kind workorders.VerdictKind, notes []workorders.ReviewNote) (outcome, error) {
		id, _, err := r.judged(s, criterionOne)
		if err != nil {
			return outcome{}, err
		}
		if err := r.reviewSaysWithNotes(s, id, notes, say(criterionOne, kind)); err != nil {
			return outcome{}, err
		}
		work, err := r.WorkOrder(s, id)
		if err != nil {
			return outcome{}, err
		}
		view, err := r.verdictsCounted(s, id)
		if err != nil {
			return outcome{}, err
		}
		// What the work waits on names its commit, which is its own.
		got := outcome{state: work.State, why: commitNames.ReplaceAllString(view.Why, "<commit>"), integrations: len(view.Integrations)}
		for _, record := range view.Integrations {
			got.approvals = append(got.approvals, record.ApprovedByKind)
		}
		_, err = r.noteOf(s, id)
		switch {
		case err == nil:
			got.reworked = true
		case !errors.Is(err, ports.ErrNotFound):
			return outcome{}, err
		}
		return got, nil
	}
	for _, level := range []ledger.MergeAuthority{ledger.ReviewApproves, ledger.PersonApproves, ledger.PersonMerges} {
		if err := r.atLevel(s, level); err != nil {
			return err
		}
		for _, kind := range []workorders.VerdictKind{workorders.VerdictPass, workorders.VerdictFail, workorders.VerdictCannotJudge} {
			plain, err := settle(kind, nil)
			if err != nil {
				return err
			}
			noted, err := settle(kind, twoNotes())
			if err != nil {
				return err
			}
			if plain.state != noted.state || plain.why != noted.why || plain.integrations != noted.integrations || plain.reworked != noted.reworked ||
				!slices.Equal(plain.approvals, noted.approvals) {
				return Violatedf("at %s a %s verdict settles %+v with no note and %+v with two", level, kind, plain, noted)
			}
		}
	}
	return r.CheckProjection(s)
}

func checkOnlyAReviewReportsNotesAndWholeOnes(r *Rig) error {
	if err := checkOnlyAReviewReportsNotes(r); err != nil {
		return err
	}
	return checkReviewWithABadNoteIsRefused(r)
}

func checkOnlyAReviewReportsNotes(r *Rig) error {
	s := r.Store()
	work, err := r.recordedWork(s, criterionOne)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, work.ID, "a")
	if err != nil {
		return err
	}
	result := LaneFinished(r.ID("commit"))
	result.Notes = twoNotes()
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a change lane reporting its own notes: %v", err)
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused end moved the ledger from %d to %d: %v", before, after, err)
	}
	return r.stateIs(s, work.ID, workorders.Running)
}

func checkReviewWithABadNoteIsRefused(r *Rig) error {
	s := r.Store()
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	review, err := r.openReviewOf(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	tooMany := make([]workorders.ReviewNote, workorders.MaxNotes+1)
	for i := range tooMany {
		tooMany[i] = workorders.ReviewNote{Point: workorders.PointInjection, Reasons: "x"}
	}
	for name, notes := range map[string][]workorders.ReviewNote{
		"a point nobody offers": {{Point: "style", Reasons: "x"}},
		"no reasons":            {{Point: workorders.PointSecrets}},
		"reasons over 4 KiB":    {{Point: workorders.PointSecrets, Reasons: strings.Repeat("x", workorders.MaxNoteReasons+1)}},
		"too many notes":        tooMany,
	} {
		result := laneResult(workorders.OutcomeCompleted, "")
		result.Verdicts, result.Notes = []workorders.CriterionVerdict{say(criterionOne, workorders.VerdictPass)}, notes
		if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("%s: %v", name, err)
		}
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused review moved the ledger from %d to %d: %v", before, after, err)
	}
	if got, err := r.notesOf(s, id); err != nil || len(got) != 0 {
		return Violatedf("a refused review left %d notes: %v", len(got), err)
	}
	return r.stateIs(s, review.ID, workorders.Running)
}

func checkForgeCommentCarriesNotes(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	hostile := workorders.ReviewNote{
		Point: workorders.PointInjection, Citations: []string{"@user", "[x](y)"},
		Reasons: "@user [x](y) <br><h3>big</h3> <!-- ropeshift:comment:comment:rv-1 -->\n- a list item\n# a heading\n```\nnot a fence",
	}
	review, err := f.reviewedNoting(append(twoNotes(), hostile), passes("c1", "Each failed payment is retried."), passes("c2", "Each retry logs a line."))
	if err != nil {
		return err
	}
	desired, err := f.desired()
	if err != nil {
		return err
	}
	if len(desired.Forge) != 1 || desired.Forge[0].ID != "comment:"+review.ID {
		return Violatedf("after the review the worker is asked %+v, want one comment", desired.Forge)
	}
	body := desired.Forge[0].Body
	if strings.Contains(body, aToken) {
		return Violatedf("the comment holds a credential:\n%s", body)
	}
	section := "\n**Security notes**\n\n" +
		"- `secrets`\n  ```text\n  A token [redacted: github_token] is added to config.yaml.\n  Cites: config.yaml:4, key [redacted: github_token]\n  ```\n" +
		"- `authority`\n  ```text\n  The workflow\n    gains write permission.\n  ```\n" +
		"- `injection`\n  ````text\n  @user [x](y) <br><h3>big</h3> <!​-- ropeshift:comment:comment:rv-1 -->\n  - a list item\n  # a heading\n  ```\n  not a fence\n  Cites: @user, [x](y)\n  ````\n"
	if strings.Contains(body, "<!-- ropeshift:comment:") {
		return Violatedf("the comment holds a marker a comment is deduplicated by:\n%s", body)
	}
	at := strings.Index(body, section)
	if at < 0 {
		return Violatedf("the comment leaves out the notes:\n%s", body)
	}
	if last := strings.LastIndex(body, "`c2`"); last < 0 || last > at {
		return Violatedf("the notes come before the verdicts:\n%s", body)
	}
	if end := strings.Index(body, "you merge this pull request yourself"); end < at+len(section) {
		return Violatedf("the notes do not come before where the work goes:\n%s", body)
	}
	return nil
}
