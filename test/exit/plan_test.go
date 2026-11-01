package exit

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/gittest"
)

// TestARepositoryIsReadAsPlansAndDocuments runs U20 on the real daemon: every file kind in
// one commit becomes records, a new version sends ready work back, and a document reads at
// the commit intake read.
func TestARepositoryIsReadAsPlansAndDocuments(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("goal.md", contract.GoalText("a-goal", "The goal"))
	repo.Commit("plan.md", contract.PlanText("a-plan", "The plan", "a-goal"))
	repo.Commit("requirements.md", contract.RequirementsText("payments", "PAY-12", "It retries"))
	repo.Commit("contract.md", strings.Replace(contract.ContractText("alpha", "Alpha", "One."), `title = "Alpha"`, `title = "Alpha"
plan = "a-plan"`, 1))
	repo.Commit("design.md", "# The design\n")
	in.waitReady(in.start("--source", repo.Dir, "--intake-tick", "50ms"))

	var plans api.PlanList
	in.waitFor("the plan", func() bool {
		if err := json.Unmarshal([]byte(in.must("plan", "list", "--json")), &plans); err != nil {
			t.Fatal(err)
		}
		return len(plans.Plans) == 1
	})
	var view api.PlanView
	in.waitFor("the plan's work", func() bool {
		if err := json.Unmarshal([]byte(in.must("plan", "show", plans.Plans[0].ID, "--json")), &view); err != nil {
			t.Fatal(err)
		}
		return len(view.WorkOrders) == 1
	})
	if view.Goal == nil || view.Goal.Title != "The goal" || view.Contracts[0].Title != "Alpha" {
		t.Errorf("view %+v", view)
	}
	if text := in.must("requirement", "get", "PAY-12"); !strings.Contains(text, "It retries") {
		t.Errorf("requirement %q", text)
	}
	if text := in.must("document", "show", "design.md"); text != "# The design\n" {
		t.Errorf("document %q", text)
	}

	id := view.WorkOrders[0].ID
	in.must("work", "review", id)
	repo.Commit("contract.md", strings.Replace(contract.ContractText("alpha", "Alpha", "Two."), `title = "Alpha"`, `title = "Alpha"
plan = "a-plan"`, 1))
	in.waitFor("the move back to review", func() bool { return in.show(id).State == workorders.ContractReview })
	if row := in.show(id); row.ContractVersion != 2 || row.ReviewedBy != "" {
		t.Errorf("after a new version: %+v", row)
	}
}
