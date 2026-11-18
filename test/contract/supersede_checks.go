package contract

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// SupersedeChecks are what an answer that names a decision it replaces owes: the answer and the end of the
// decision it replaces are one transaction, and what the answer binds is bound once (FR-QD-9, ADR-0030).
var SupersedeChecks = []Check{
	{"SA1", "an answer that names a decision in force replaces it in the transaction that records the answer", checkAnswerReplaces},
	{"SA2", "an answer that names a decision that no longer stands, or none, is refused and closes nothing", checkAnswerRefusesWhatItCannotReplace},
	{"SA3", "a one-way answer that replaces a decision reworks the finished work it binds once", checkAnswerReplacesAndBindsOnce},
	{"SA4", "an answer records the decisions it judged compatible, and a retry of a replacing answer changes nothing", checkAnswerRecordsNoConflictAndRetries},
}

// aboutThePlan asks a question whose subject is the scenario's plan, as its review lane's questions are, so the
// answer's scope is `plan:<id>`.
func (m *midPlan) aboutThePlan(text string, categories ...string) (string, error) {
	asked, err := m.r.ask(m.s, text, "lane-"+m.r.ID("l"), func(a *questions.Ask) {
		a.SubjectKind, a.SubjectID = planning.SubjectPlan, m.plan
		if len(categories) != 0 {
			a.Categories = categories
		}
	})
	return asked.QuestionID, err
}

// standing records a reversible decision of the scenario's plan, which an answer can then replace.
func (m *midPlan) standing() (questions.Decision, error) {
	return m.decide(m.r.member(), "plan:"+m.plan, questions.Reversible)
}

func checkAnswerReplaces(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	old, err := m.standing()
	if err != nil {
		return err
	}
	asked, err := m.aboutThePlan("Which ID scheme do the work orders take?")
	if err != nil {
		return err
	}
	answer, err := r.NewOps(m.s).Answer(r.Ctx, r.Scope, asked, questions.Answer{DecisionID: r.ID("d"), Statement: "Ulids.", Supersedes: old.ID}, r.member())
	if err != nil {
		return err
	}
	if answer.Supersedes != old.ID {
		return Violatedf("the answer records that it supersedes %q, want %s", answer.Supersedes, old.ID)
	}
	if ended, err := m.decisionFor(old.ID); err != nil || ended.Status != questions.DecisionReplaced {
		return Violatedf("the decision an answer replaced is %s: %v", ended.Status, err)
	}
	if err := m.replacedBy(old.ID, answer.ID); err != nil {
		return err
	}
	closed, err := r.readQuestion(m.s, asked)
	if err != nil {
		return err
	}
	if closed.Status != questions.StatusAnswered || closed.DecisionID != answer.ID {
		return Violatedf("the question reads %s, answered by %q", closed.Status, closed.DecisionID)
	}
	return r.CheckQuestions(m.s)
}

func checkAnswerRefusesWhatItCannotReplace(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	old, err := m.standing()
	if err != nil {
		return err
	}
	if _, err := m.replaceOf(old.ID); err != nil {
		return err
	}
	for _, supersedes := range []struct {
		name string
		id   string
		want error
	}{
		{"a decision a member already replaced", old.ID, questions.ErrDecisionEnded},
		{"a decision the ledger does not hold", "no-such-decision", ports.ErrNotFound},
	} {
		asked, err := m.aboutThePlan("Which ID scheme do the work orders take? " + supersedes.name)
		if err != nil {
			return err
		}
		decision := r.ID("d")
		_, err = r.NewOps(m.s).Answer(r.Ctx, r.Scope, asked, questions.Answer{DecisionID: decision, Statement: "Ulids.", Supersedes: supersedes.id}, r.member())
		if !errors.Is(err, supersedes.want) {
			return Violatedf("an answer that replaces %s was refused as %v, want %v", supersedes.name, err, supersedes.want)
		}
		open, err := r.readQuestion(m.s, asked)
		if err != nil {
			return err
		}
		if !open.Status.Unresolved() || open.DecisionID != "" {
			return Violatedf("a refused answer left the question %s, answered by %q", open.Status, open.DecisionID)
		}
		if _, err := r.decisionOf(m.s, decision); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a refused answer left decision %s on the ledger: %v", decision, err)
		}
	}
	return r.CheckQuestions(m.s)
}

// replaceOf replaces a decision with another, as a member records one: the first no longer stands.
func (m *midPlan) replaceOf(id string) (questions.Decision, error) {
	return m.r.NewOps(m.s).RecordDecision(m.r.Ctx, m.r.Scope, questions.Decision{
		ID: m.r.ID("d"), Status: questions.DecisionDecided, Statement: "Binding decision " + m.r.ID("s"), Rationale: "The brand guide says so.",
		Citations: []string{"brand-guide"}, Scope: "plan:" + m.plan, Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
		Supersedes: id,
	}, m.r.member())
}

func checkAnswerReplacesAndBindsOnce(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	old, err := m.standing()
	if err != nil {
		return err
	}
	asked, err := m.aboutThePlan("May audit data leave the region?", questions.CategoryData)
	if err != nil {
		return err
	}
	answer, err := r.NewOps(m.s).Answer(r.Ctx, r.Scope, asked, questions.Answer{DecisionID: r.ID("d"), Statement: "Ulids.", Supersedes: old.ID}, r.member())
	if err != nil {
		return err
	}
	if answer.Reversibility != questions.OneWay {
		return Violatedf("an answer in a one-way category is recorded %q", answer.Reversibility)
	}
	rework, err := m.reworkIDs(m.orders["done"])
	if err != nil || len(rework) != 1 {
		return Violatedf("the finished work has rework %v after a one-way answer that replaced a decision, want one: %v", rework, err)
	}
	reached, err := m.reached()
	if err != nil {
		return err
	}
	var ofAnswer []questions.Reached
	for _, one := range reached {
		if one.DecisionID == answer.ID {
			ofAnswer = append(ofAnswer, one)
		}
	}
	if len(ofAnswer) != 1 || !slices.Equal(ofAnswer[0].Rework, rework) {
		return Violatedf("the answer reached %+v, want the finished work once with its rework %v", ofAnswer, rework)
	}
	return r.CheckQuestions(m.s)
}

func checkAnswerRecordsNoConflictAndRetries(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	old, err := m.standing()
	if err != nil {
		return err
	}
	asked, err := m.aboutThePlan("Which ID scheme do the work orders take?")
	if err != nil {
		return err
	}
	given := questions.Answer{DecisionID: r.ID("d"), Statement: "Ulids.", Supersedes: old.ID, NoConflict: []string{m.p17}}
	ops := r.NewOps(m.s)
	first, err := ops.Answer(r.Ctx, r.Scope, asked, given, r.member())
	if err != nil {
		return err
	}
	if !slices.Equal(first.NoConflict, []string{m.p17}) {
		return Violatedf("the answer records no_conflict %v, want [%s]", first.NoConflict, m.p17)
	}
	events, err := r.Events(m.s, r.Scope)
	if err != nil {
		return err
	}
	again, err := ops.Answer(r.Ctx, r.Scope, asked, given, r.member())
	if err != nil {
		return Violatedf("a retry of the answer: %v", err)
	}
	if again.ID != first.ID || again.Supersedes != old.ID || !slices.Equal(again.NoConflict, first.NoConflict) {
		return Violatedf("a retry returned %+v, want %+v", again, first)
	}
	if after, err := r.Events(m.s, r.Scope); err != nil || len(after) != len(events) {
		return Violatedf("a retry wrote %d events: %v", len(after)-len(events), err)
	}
	return r.CheckQuestions(m.s)
}
