package contract

import (
	"slices"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// OverlapChecks hold FR-QD-5: whatever route takes a question to a person, it names the decisions in force whose
// scope overlaps the question's.
var OverlapChecks = []Check{
	{"OV1", "a planning lane's question to the person names the decisions in force its scope overlaps", checkDirectNamesOverlaps},
	{"OV2", "the question a merge leaves for a person names the decisions in force its own scope overlaps", checkMergeTargetNamesOverlaps},
}

// decidedAbout records a decision in force about a plan, as the ledger holds one, and returns its ID.
func (r *Rig) decidedAbout(s ports.Store, plan string) (string, error) {
	decision := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Plan " + plan + " is settled.", Scope: "plan:" + plan}
	err := s.Write(r.Ctx, r.Scope, "test.decision", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(decision); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, decision.ID, decision, r.Actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	return decision.ID, err
}

// askedAbout is a planning lane's question about a plan.
func (r *Rig) askedAbout(s ports.Store, text, plan string) (string, error) {
	asked, err := r.ask(s, text, planner, func(a *questions.Ask) {
		a.Direct, a.SubjectKind, a.SubjectID = true, planning.SubjectPlan, plan
	})
	return asked.QuestionID, err
}

func checkDirectNamesOverlaps(r *Rig) error {
	s := r.Store()
	plan, bare := r.ID("ovplan"), r.ID("ovplan")
	decided, err := r.decidedAbout(s, plan)
	if err != nil {
		return err
	}
	covered, err := r.askedAbout(s, "Which logo set does the first plan use?", plan)
	if err != nil {
		return err
	}
	uncovered, err := r.askedAbout(s, "Which icon set does the second plan use?", bare)
	if err != nil {
		return err
	}
	for id, want := range map[string][]string{covered: {decided}, uncovered: nil} {
		row, err := r.readQuestion(s, id)
		if err != nil {
			return err
		}
		if row.Status != questions.StatusNeedsHuman || !slices.Equal(row.Overlaps, want) {
			return Violatedf("a planning lane's question %s reads %s with the overlaps %v, want %v", id, row.Status, row.Overlaps, want)
		}
	}
	return nil
}

func checkMergeTargetNamesOverlaps(r *Rig) error {
	s := r.Store()
	merged, stays := r.ID("ovplan"), r.ID("ovplan")
	if _, err := r.decidedAbout(s, merged); err != nil {
		return err
	}
	decided, err := r.decidedAbout(s, stays)
	if err != nil {
		return err
	}
	gone, err := r.askedAbout(s, "Which logo set does the merged plan use?", merged)
	if err != nil {
		return err
	}
	kept, err := r.askedAbout(s, "Which logo set does the kept plan use?", stays)
	if err != nil {
		return err
	}
	if err := r.NewOps(s).MergeQuestion(r.Ctx, r.Scope, gone, kept, r.Actor); err != nil {
		return err
	}
	row, err := r.readQuestion(s, kept)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || !slices.Equal(row.Overlaps, []string{decided}) {
		return Violatedf("the question a merge left reads %s with the overlaps %v, want %v", row.Status, row.Overlaps, []string{decided})
	}
	return nil
}
