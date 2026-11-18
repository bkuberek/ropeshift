package contract

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// laneOptions and the recommendation after them are what a lane gives a question in the checks of its recommendation.
var laneOptions = []string{"Retry three times", "Retry until the deadline"}

const (
	laneRecommendation = "Retry three times"
	laneRationale      = "Three tries stay inside the deadline."
)

// withRecommendation gives a question the options, the recommendation, and the rationale of a lane that has a view.
func withRecommendation(a *questions.Ask) {
	a.Options, a.Recommendation, a.RecommendationRationale = slices.Clone(laneOptions), laneRecommendation, laneRationale
}

// holdsLaneView reads a question that holds what withRecommendation gave it.
func holdsLaneView(q questions.Question) bool {
	return slices.Equal(q.Options, laneOptions) && q.Recommendation == laneRecommendation && q.RecommendationRationale == laneRationale
}

func checkAskRecommendation(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "How often does a failed call retry?", "lane-a", withRecommendation)
	if err != nil {
		return err
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if !holdsLaneView(row) || row.Status != questions.StatusOpen {
		return Violatedf("question %+v", row)
	}
	for name, change := range map[string]func(*questions.Ask){
		"a recommendation that is no option": func(a *questions.Ask) { withRecommendation(a); a.Recommendation = "Never retry" },
		"a rationale with no recommendation": func(a *questions.Ask) { a.Options, a.RecommendationRationale = laneOptions, laneRationale },
	} {
		if _, err := r.ask(s, "How long does a retry wait? ("+name+")", "lane-a", change); !errors.Is(err, questions.ErrInvalid) {
			return Violatedf("%s: %v", name, err)
		}
	}
	rows, err := r.filterQuestions(s, ports.RecordFilter{})
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return Violatedf("a refused question left rows %v", rows)
	}
	// A recommendation with no options is the lane's word until the orchestrator names the options.
	bare, err := r.ask(s, "Which queue takes the retries?", "lane-a", func(a *questions.Ask) { a.Recommendation = "The default queue" })
	if err != nil {
		return Violatedf("a recommendation with no options: %v", err)
	}
	if row, err = r.readQuestion(s, bare.QuestionID); err != nil || row.Recommendation != "The default queue" {
		return Violatedf("a recommendation with no options is kept as %+v, %v", row, err)
	}
	return r.CheckQuestions(s)
}

// checkEscalationKeepsRecommendation holds that escalation keeps a lane's options and recommendation unless the
// orchestrator states its own, and that a recommendation of an option the orchestrator replaced does not stand.
func checkEscalationKeepsRecommendation(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	escalate := func(text string, escalation questions.Escalation) (questions.Question, error) {
		asked, err := r.ask(s, text, "lane-a", withRecommendation)
		if err != nil {
			return questions.Question{}, err
		}
		if err := r.climbed(s, asked.QuestionID, holder); err != nil {
			return questions.Question{}, err
		}
		escalation.Reason = questions.ReasonScopeChange
		escalated, err := ops.Escalate(r.Ctx, r.Scope, asked.QuestionID, escalation, holder)
		if err != nil {
			return questions.Question{}, err
		}
		return escalated, nil
	}
	kept, err := escalate("How often does the first call retry?", questions.Escalation{})
	if err != nil {
		return Violatedf("an escalation that states none: %v", err)
	}
	if kept.Status != questions.StatusNeedsHuman || !holdsLaneView(kept) {
		return Violatedf("an escalation that states none leaves %+v", kept)
	}
	stored, err := r.readQuestion(s, kept.ID)
	if err != nil {
		return err
	}
	if !holdsLaneView(stored) {
		return Violatedf("an escalation that states none stores %+v", stored)
	}
	own, err := escalate("How often does the second call retry?", questions.Escalation{
		Recommendation: "Retry until the deadline", RecommendationRationale: "The deadline is the limit that counts.",
	})
	if err != nil {
		return Violatedf("an escalation that states its own: %v", err)
	}
	if own.Recommendation != "Retry until the deadline" || own.RecommendationRationale != "The deadline is the limit that counts." || !slices.Equal(own.Options, laneOptions) {
		return Violatedf("an escalation that states its own leaves %+v", own)
	}
	reworded, err := escalate("How often does the third call retry?", questions.Escalation{RecommendationRationale: "The deadline allows three tries."})
	if err != nil {
		return Violatedf("an escalation that states a rationale: %v", err)
	}
	if reworded.Recommendation != laneRecommendation || reworded.RecommendationRationale != "The deadline allows three tries." {
		return Violatedf("an escalation that states a rationale leaves %+v", reworded)
	}
	_, err = escalate("How often does the fourth call retry?", questions.Escalation{Options: []string{"Retry twice", "Retry once"}})
	var gate *questions.GateError
	if !errors.As(err, &gate) || len(gate.Failures) != 1 || gate.Failures[0].Check != questions.GateRecommendation {
		return Violatedf("an escalation that replaces the recommended option: %v", err)
	}
	return r.CheckQuestions(s)
}

// checkDirectKeepsRecommendation holds that a planner's question carries the options and the recommendation it was
// asked with to the item a member answers.
func checkDirectKeepsRecommendation(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "Which branch does the plan start from?", planner, func(a *questions.Ask) { direct(a); withRecommendation(a) })
	if err != nil {
		return err
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || !row.Direct() || !holdsLaneView(row) {
		return Violatedf("question %+v", row)
	}
	if err := r.deliveredOnce(s, asked.QuestionID); err != nil {
		return err
	}
	items, err := r.waitingItems(s)
	if err != nil {
		return err
	}
	if len(items) != 1 || items[0].ID != asked.QuestionID || !slices.Equal(items[0].Options, laneOptions) ||
		items[0].Recommendation != laneRecommendation || items[0].RecommendationRationale != laneRationale {
		return Violatedf("a member sees %+v", items)
	}
	return r.CheckQuestions(s)
}
