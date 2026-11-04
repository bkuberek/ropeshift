package apitest

import (
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The documents a detailed world's source holds, so a page reads what an attacker could commit.
const (
	WorldSpecPath    = "docs/specs/retry.md"
	WorldHostileURI  = "javascript:alert(3)"
	WorldHostileSpec = "# The retry spec\n\nA lane retries " + WorldHostileBit + " twice.\n\n<script>alert(4)</script>\n\n" +
		"## Links and pictures\n\n[Sign in again](javascript:alert(5)) and ![a pixel](http://attacker.example/pixel.png)\n\n" +
		"<iframe src=\"http://attacker.example\"></iframe>\n\n- The first rule\n- A rule with `code` in it\n\n" +
		"```html\n<svg onload=alert(6)></svg>\n```\n"
	worldReview = "review-1"
)

// Documents lays out a source's documents: one of each kind, and a second version of one.
func Documents(knowledge interface{ Write(path, body string) }) {
	knowledge.Write("design.md", "# The retry budget\n")
	knowledge.Write("design.md", "# The retry budget\n\nA budget bounds a lane's retries.\n")
	knowledge.Write("docs/requirements.md", "# What retries must do\n\nA lane must stop retrying at its budget.\n")
	knowledge.Write(WorldSpecPath, WorldHostileSpec)
	knowledge.Write("notes.md", "# Notes from the first run\n")
}

// Requirement adds one version of a requirement, as intake would.
func (w *World) Requirement(key, text string) {
	w.t.Helper()
	err := w.Daemon.Store.Write(w.ctx, Scope, "test.requirement", func(tx ports.WriteTx) error {
		requirement := planning.Requirement{
			ID: "requirement-" + key, Version: 1, Key: key, Text: text, Source: "docs/requirements.md@1", Digest: planning.RequirementDigest(key, text),
		}
		if err := tx.Planning().InsertRequirement(requirement); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.requirement", Actor: worldMember, Subject: ledger.Subject{Kind: "requirement", ID: key}})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// Binding records a source, as intake would.
func (w *World) Binding(binding planning.SourceBinding) {
	w.t.Helper()
	err := w.Daemon.Store.Write(w.ctx, Scope, "test.binding", func(tx ports.WriteTx) error {
		if err := tx.Planning().SaveBinding(binding); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.binding", Actor: worldMember, Subject: ledger.Subject{Kind: "binding", ID: binding.ID}})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// PlanOf names the plan a work order's contract belongs to.
func (w *World) PlanOf(id string) string {
	w.t.Helper()
	plan := ""
	err := w.Daemon.Store.Read(w.ctx, Scope, func(tx ports.ReadTx) error {
		order, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		contract, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
		plan = contract.PlanID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return plan
}

// Finish reports a lane's whole result.
func (l *Lane) Finish(result workorders.LaneResult) {
	l.w.t.Helper()
	l.Upload(ledger.UploadLaneEnded, result)
}

// Detail adds what Populate leaves out and the planning and outcome views read: a requirement, a
// contract that names its context, a review with what it left open, a chain of three decisions, the
// ways work ends, a source, and an orchestrator. Text an agent or a repository writes carries markup,
// so every view is tested on what an attacker could put there. It adds nothing that needs a person.
func Detail(w *World, clock *Clock) {
	w.t.Helper()
	client := w.Daemon.Client
	clock.Advance(time.Minute)

	w.Requirement("FR-RETRY-1", "A lane must stop retrying at its budget.")
	w.Requirement("FR-RETRY-2", "A person can read why "+WorldHostileBit+" stopped.")
	w.ChangeContract("wo-running", func(next *planning.Contract) {
		next.Statement = "Write the docs, and say what " + WorldHostileBit + " means."
		next.Requirements = []string{"FR-RETRY-1", "FR-MISSING-9"}
		next.Decisions = []string{"decision-2"}
		next.Source = "plans/docs.md@abc123"
	})
	w.SaveGoal(planning.Goal{
		ID: "goal-1", Title: "Ship solo mode", Status: planning.StatusActive, Statement: "One machine runs the whole product.",
		SuccessMeasures: []string{"A lane survives a restart", "A person reads " + WorldHostileBit},
	}, "wo-running")
	plan := w.PlanOf("wo-running")
	if _, err := client.ReviewPlan(w.ctx, plan, api.ReviewPlan{ID: worldReview}); err != nil {
		w.t.Fatal(err)
	}
	_, err := w.Questions.Ask(w.ctx, Scope, questions.Ask{
		ID: "question-review", Text: "Does the plan cover a restart?", Categories: []string{questions.CategorySecurity},
		SubjectKind: planning.SubjectPlan, SubjectID: plan, Lane: "lane-asking", Generation: 1,
	}, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"})
	if err != nil {
		w.t.Fatal(err)
	}

	_, err = client.RecordDecision(w.ctx, api.RecordDecision{
		ID: "decision-3", Status: questions.DecisionAssumed, Statement: "Retry until the budget runs out.",
		Rationale: "A count cannot know the outage: " + WorldHostileBit, Scope: "retries", Citations: []string{"requirement:FR-RETRY-1"},
		Categories: []string{questions.CategoryProduct, questions.CategoryOperations}, Reversibility: questions.Reversible, Supersedes: "decision-2",
	})
	if err != nil {
		w.t.Fatal(err)
	}

	clock.Advance(time.Minute)
	w.Running("wo-done", "Ship the first cut").Finish(workorders.LaneResult{
		Report: workorders.Report{
			Outcome: workorders.OutcomeCompleted, Artifacts: []string{WorldHostileURI},
			Checks: []workorders.CheckResult{{Name: "go test", Passed: true, Criterion: "c1"}},
		},
		Branch: "lane/lane-wo-done", Head: "abc123",
	})
	if _, err := client.Accept(w.ctx, "wo-done"); err != nil {
		w.t.Fatal(err)
	}
	clock.Advance(time.Minute)
	w.Running("wo-failed", "Try the impossible").Finish(workorders.LaneResult{
		Report: workorders.Report{
			Outcome: workorders.OutcomeFailed, Reason: "It gave up on " + WorldHostileBit,
			Checks: []workorders.CheckResult{{Name: "go vet", Passed: false, Criterion: "c1"}},
		},
		Branch: "lane/lane-wo-failed", Head: "def456",
	})
	clock.Advance(time.Minute)
	w.Recorded("wo-canceled", "Drop the old plan")
	w.Cancel("wo-canceled")
	w.Created("wo-loose", "Not planned yet")

	w.Binding(planning.SourceBinding{
		ID: "source-1", Port: planning.PortWork, Adapter: "repofiles", Instance: "repository-1", Health: planning.HealthOK,
		Problems: []planning.SourceProblem{{Path: "plans/bad.md", Detail: "no kind in " + WorldHostileBit}},
	})
	if _, err := client.SetOrchestrator(w.ctx, WorldWorker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning}); err != nil {
		w.t.Fatal(err)
	}
}
