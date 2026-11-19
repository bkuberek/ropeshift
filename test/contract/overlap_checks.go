package contract

import (
	"slices"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// OverlapChecks hold FR-QD-5: a question a planning lane puts to the person names the decisions in force whose scope
// overlaps its own, and the scopes of a page resolve together to what each resolves to alone.
var OverlapChecks = []Check{
	{"OV1", "a planning lane's question to the person names the decisions in force its scope overlaps", checkDirectNamesOverlaps},
	{"OV2", "the decisions each of several scopes overlaps are the same resolved together as resolved alone", checkScopesResolveTogetherAsAlone},
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

func checkScopesResolveTogetherAsAlone(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	web, err := m.order("web")
	if err != nil {
		return err
	}
	repository := r.ID("ovrepo")
	if err := r.HoldRepository(m.s, repository); err != nil {
		return err
	}
	for _, scope := range []string{"plan:" + m.plan, "contract:" + web.ContractID, "wo:" + web.ID, "repo:" + repository} {
		if _, err := m.decide(r.member(), scope, questions.Reversible); err != nil {
			return Violatedf("a decision scoped %q was refused: %v", scope, err)
		}
	}
	scopes := []string{"plan:" + m.plan, "contract:" + web.ContractID, "wo:" + web.ID, "wo:ghost", "repo:" + repository, "plan:ghost", "", "plan:" + m.plan}
	var together map[string][]string
	alone := map[string][]string{}
	err = m.s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		if together, err = control.OverlapsOfScopes(tx, scopes); err != nil {
			return err
		}
		for _, scope := range scopes {
			if _, read := alone[scope]; read {
				continue
			}
			alone[scope] = []string{}
			found, err := control.DecisionsOverlapping(tx, scope)
			if err != nil {
				return err
			}
			for _, decision := range found {
				alone[scope] = append(alone[scope], decision.ID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if !slices.Equal(together[scope], alone[scope]) {
			return Violatedf("the scope %q overlaps %v resolved with others, and %v resolved alone", scope, together[scope], alone[scope])
		}
	}
	if len(alone["plan:"+m.plan]) == 0 || len(alone["repo:"+repository]) == 0 || len(alone["wo:ghost"]) != 0 {
		return Violatedf("the scenario overlaps %v for its plan, %v for its repository, and %v for a work order nobody holds", alone["plan:"+m.plan], alone["repo:"+repository], alone["wo:ghost"])
	}
	return nil
}
