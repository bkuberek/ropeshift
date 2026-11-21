package planning

import (
	"fmt"
	"strings"
)

// The words the project templates start a plan with. A plan, goal, or contract file that still holds one of
// them has not been written yet, and a test holds each to the templates, so a template that changes its words
// fails the build before a member reads the difference.
const (
	PlaceholderPlanTitle     = "State the plan in one line"
	PlaceholderGoalTitle     = "State the goal in one line"
	PlaceholderMeasure       = "State one measurable sign that the goal is met"
	PlaceholderPlanProse     = "How the work is planned, in prose: the order of the milestones, the shared contracts built first, and what the plan leaves for later."
	PlaceholderGoalProse     = "Why this goal matters, who it serves, and what it leaves out, in prose."
	PlaceholderContractProse = "What this work must deliver, in prose: the behavior, its boundaries, and what is out of scope. The lane reads this statement with the criteria, and the review judges the work against both."
)

// FinishContract is what a contract of a plan says that tells whether it is written: its text, the work orders
// its file lists, and whether it records code that exists already.
type FinishContract struct {
	ID         string
	Statement  string
	WorkOrders int
	Existing   bool
}

// Finish is what a plan version holds that tells whether it is finished. A branch's version and a version the
// ledger holds fill it the same way, so the same plan reads the same in both.
type Finish struct {
	Title string
	// GoalNamed says the plan's file names a goal, and Goal is what the version holds of it, nil when nobody does.
	GoalNamed bool
	Goal      *DetailGoal
	// Documents counts the documents the plan lists, and Decisions the answers it drafts.
	Documents int
	Decisions int
	// Body is the plan file's prose.
	Body      string
	Contracts []FinishContract
}

// templateText says a text is the template's own text and nothing else, line breaks aside. A text that keeps a
// template sentence and goes on to say more is written, so it is not a placeholder.
func templateText(text, placeholder string) bool {
	return strings.Join(strings.Fields(text), " ") == strings.Join(strings.Fields(placeholder), " ")
}

// Unfinished lists what a plan version lacks to be one a member is asked to approve, each as a sentence that
// says what is missing, and none for a version that is finished. A version is a placeholder when it
//
//   - holds the title the plan template starts with, or a goal with the template's title or success measure;
//   - holds, as the whole of the text of its plan, its goal, or one of its contracts, the template's own text;
//   - holds nothing at all: it names no goal, lists no document, drafts no decision, and lists no work order
//     in any contract, unless a contract records code that exists already.
//
// A text that keeps a template sentence and says more is written, so it is not a placeholder, and neither is an empty
// text. A plan that names a goal or lists a document is not empty: a member reads what is there and decides.
func Unfinished(in Finish) []string {
	var out []string
	if strings.TrimSpace(in.Title) == PlaceholderPlanTitle {
		out = append(out, fmt.Sprintf("The plan's title is still the template's, %q.", PlaceholderPlanTitle))
	}
	if in.Goal != nil {
		out = append(out, unfinishedGoal(*in.Goal)...)
	}
	if templateText(in.Body, PlaceholderPlanProse) {
		out = append(out, "The plan's text is still the template's, which says how the work is planned.")
	}
	for _, contract := range in.Contracts {
		if templateText(contract.Statement, PlaceholderContractProse) {
			out = append(out, fmt.Sprintf("Contract %s's text is still the template's, which says what the work must deliver.", contract.ID))
		}
	}
	if !in.GoalNamed && in.Documents == 0 && in.Decisions == 0 && !holdsWork(in.Contracts) {
		out = append(out, "The plan holds nothing yet: it names no goal, lists no document or draft decision, and no contract lists a work order.")
	}
	return out
}

func unfinishedGoal(goal DetailGoal) []string {
	var out []string
	if strings.TrimSpace(goal.Title) == PlaceholderGoalTitle {
		out = append(out, fmt.Sprintf("The goal's title is still the template's, %q.", PlaceholderGoalTitle))
	}
	for _, measure := range goal.SuccessMeasures {
		if strings.TrimSpace(measure) == PlaceholderMeasure {
			out = append(out, fmt.Sprintf("A success measure is still the template's, %q.", PlaceholderMeasure))
			break
		}
	}
	if templateText(goal.Statement, PlaceholderGoalProse) {
		out = append(out, "The goal's text is still the template's, which says why the goal matters.")
	}
	return out
}

// holdsWork says a plan lists a work order, or records a contract whose code exists already, which an
// adoption's plan does.
func holdsWork(contracts []FinishContract) bool {
	for _, contract := range contracts {
		if contract.WorkOrders > 0 || contract.Existing {
			return true
		}
	}
	return false
}
