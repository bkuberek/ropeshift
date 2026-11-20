package planning_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/templates"
)

func finished() planning.Finish {
	return planning.Finish{
		Title:     "Self-service accounts",
		GoalNamed: true,
		Goal:      &planning.DetailGoal{Title: "Customers manage their own accounts", Statement: "Why: support load.", SuccessMeasures: []string{"Support requests fall by half"}},
		Body:      "Build the API first, then the page.",
		Contracts: []planning.FinishContract{{ID: "accounts-api", Statement: "Serve the endpoints.", WorkOrders: 2}},
	}
}

func TestUnfinishedAcceptsAPlanThatSaysWhatItIsFor(t *testing.T) {
	if got := planning.Unfinished(finished()); len(got) != 0 {
		t.Fatalf("a finished plan reads as unfinished: %v", got)
	}
}

func TestUnfinishedNamesWhatIsMissing(t *testing.T) {
	cases := []struct {
		name string
		edit func(*planning.Finish)
		want string
	}{
		{"template title", func(f *planning.Finish) { f.Title = planning.PlaceholderPlanTitle }, "title is still the template's"},
		{"template goal title", func(f *planning.Finish) { f.Goal.Title = planning.PlaceholderGoalTitle }, "goal's title is still the template's"},
		{"template measure", func(f *planning.Finish) { f.Goal.SuccessMeasures = []string{planning.PlaceholderMeasure} }, "success measure is still the template's"},
		{"template goal prose", func(f *planning.Finish) {
			f.Goal.Statement = "Why this goal matters, who it serves, and what it leaves out, in prose."
		}, "goal's text is still the template's"},
		{"template plan prose", func(f *planning.Finish) { f.Body = "  How the work is planned, in prose: the order of the milestones." }, "plan's text is still the template's"},
		{"template contract prose", func(f *planning.Finish) {
			f.Contracts[0].Statement = "What this work must deliver, in prose: the behavior."
		}, "accounts-api's text is still the template's"},
		{"nothing at all", func(f *planning.Finish) {
			*f = planning.Finish{Title: "Retry budget"}
		}, "holds nothing yet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := finished()
			c.edit(&in)
			got := strings.Join(planning.Unfinished(in), "\n")
			if !strings.Contains(got, c.want) {
				t.Fatalf("unfinished = %q, want a line holding %q", got, c.want)
			}
		})
	}
}

// A plan a member can read something of is a plan to decide on, so only a plan with nothing in it is empty.
func TestUnfinishedLeavesAPartialPlanToTheMember(t *testing.T) {
	for name, edit := range map[string]func(*planning.Finish){
		"no goal":           func(f *planning.Finish) { f.GoalNamed, f.Goal = false, nil },
		"a goal nobody has": func(f *planning.Finish) { f.Goal = nil },
		"no work order":     func(f *planning.Finish) { f.Contracts = nil },
		"empty prose":       func(f *planning.Finish) { f.Body, f.Goal.Statement = "", "" },
		"no measure":        func(f *planning.Finish) { f.Goal.SuccessMeasures = nil },
	} {
		t.Run(name, func(t *testing.T) {
			in := finished()
			in.Documents = 1
			edit(&in)
			if got := planning.Unfinished(in); len(got) != 0 {
				t.Fatalf("a plan with a title and a document reads as unfinished: %v", got)
			}
		})
	}
}

// An adoption records code that exists already, so a contract marked existing is the plan's work.
func TestUnfinishedCountsAnExistingContractAsWork(t *testing.T) {
	in := finished()
	in.Contracts = []planning.FinishContract{{ID: "billing", Statement: "Delivered already.", Existing: true}}
	if got := planning.Unfinished(in); len(got) != 0 {
		t.Fatalf("an adoption's plan reads as unfinished: %v", got)
	}
}

func TestUnfinishedReportsEveryGapOfAFreshTemplate(t *testing.T) {
	got := planning.Unfinished(planning.Finish{
		Title:     planning.PlaceholderPlanTitle,
		GoalNamed: true,
		Goal:      &planning.DetailGoal{Title: planning.PlaceholderGoalTitle, SuccessMeasures: []string{planning.PlaceholderMeasure}},
		Body:      "How the work is planned, in prose: the order of the milestones.",
	})
	if len(got) < 4 {
		t.Fatalf("a copied template reads as finished in parts: %v", got)
	}
}

// The words that mark a placeholder are the words the project templates write, so a template that changes
// them fails here and not in a member's approval.
func TestPlaceholderWordsAreTheTemplates(t *testing.T) {
	render := func(name string) string {
		out, err := templates.Render(name, templates.Data{Project: "harbor"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	plan, goal := render("workspace/plan/plan.md"), render("workspace/plan/goal.md")
	contract := render("workspace/plan/contract.md")
	for file, words := range map[string][]string{
		plan:     {`"` + planning.PlaceholderPlanTitle + `"`, planning.PlaceholderPlanProse},
		goal:     {`"` + planning.PlaceholderGoalTitle + `"`, `"` + planning.PlaceholderMeasure + `"`, planning.PlaceholderGoalProse},
		contract: {planning.PlaceholderContractProse},
	} {
		for _, word := range words {
			if !strings.Contains(file, word) {
				t.Errorf("a template no longer holds %q", word)
			}
		}
	}
}
