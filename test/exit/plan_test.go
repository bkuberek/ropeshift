package exit

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
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

// runLane takes one contract through a real daemon to a running lane on a fixture. A shim
// outlives its daemon, so a session that never ends is reaped by the IDs its state names.
func (in *install) runLane(fixture string) (string, *daemon) {
	in.t.Helper()
	repo := gittest.New(in.t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(in.t, fixture)
	doubles.ReapShims(in.t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	return id, d
}

func (in *install) questions() []questions.Question {
	in.t.Helper()
	var list api.QuestionList
	if err := json.Unmarshal([]byte(in.must("question", "list", "--json")), &list); err != nil {
		in.t.Fatal(err)
	}
	return list.Questions
}

// TestALaneAsksAndGoesOn runs `ask` from a session through `ropeshift mcp` and the worker to
// the ledger: the first call records, the second finds it, and neither holds the lane.
func TestALaneAsksAndGoesOn(t *testing.T) {
	in := newInstall(t)
	id, _ := in.runLane("lane-asks")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	asked := in.questions()
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	row := asked[0]
	if row.Text != "What is the retry budget?" || row.AskedBy != "lane-1" || row.SubjectID != id || !row.Blocking {
		t.Errorf("question %+v", row)
	}
}

// TestAQuestionToolCallBecomesALedgerQuestion runs the conversion on a headless lane: the
// hook defers the call, and what it asked reaches the ledger with the lane attached.
func TestAQuestionToolCallBecomesALedgerQuestion(t *testing.T) {
	in := newInstall(t)
	id, _ := in.runLane("question-tool")
	in.waitFor("the question", func() bool { return len(in.questions()) == 1 })
	row := in.questions()[0]
	if row.Text != "Which retry budget?" || !row.Blocking || row.SubjectID != id || strings.Join(row.Options, ",") != "Three,Five" {
		t.Errorf("question %+v", row)
	}
}

// TestABlockingQuestionHoldsContractReview runs FR-PLAN-5 through the CLI: the review is on
// record, the definition of ready names the question, and the tick moves the work once the
// question closes.
func TestABlockingQuestionHoldsContractReview(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "One."))
	in.waitReady(in.start("--source", repo.Dir, "--intake-tick", "20ms"))
	id := in.onlyWorkOrder()
	in.must("question", "record", "--id", "q1", "--text", "Is this right?", "--blocking", "--subject", id, "--subject-kind", "work_order")
	code, _, stderr := in.run("work", "review", id)
	if code != cli.ExitNotReady || !strings.Contains(stderr, "not ready") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	in.must("question", "dismiss", "q1")
	in.waitFor("the move to ready", func() bool { return in.show(id).State == workorders.Ready })
}
