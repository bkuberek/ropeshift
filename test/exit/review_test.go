package exit

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

func (in *install) planView(id string) api.PlanView {
	in.t.Helper()
	var view api.PlanView
	if err := json.Unmarshal([]byte(in.must("plan", "show", id, "--json")), &view); err != nil {
		in.t.Fatal(err)
	}
	return view
}

func (in *install) plans() []planning.Plan {
	in.t.Helper()
	var list api.PlanList
	if err := json.Unmarshal([]byte(in.must("plan", "list", "--json")), &list); err != nil {
		in.t.Fatal(err)
	}
	return list.Plans
}

func (in *install) sources() []planning.SourceBinding {
	in.t.Helper()
	var list api.SourceList
	if err := json.Unmarshal([]byte(in.must("source", "list", "--json")), &list); err != nil {
		in.t.Fatal(err)
	}
	return list.Sources
}

func (in *install) deliveries() int {
	in.t.Helper()
	delivered := 0
	err := in.ledgerOf().Read(in.t.Context(), soloScope, func(tx ports.ReadTx) error {
		events, err := tx.Events(0, 10_000)
		for _, event := range events {
			if event.Type == questions.EventBatchDelivered {
				delivered++
			}
		}
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
	return delivered
}

// runReviewer starts a daemon on one planned contract, and has a member start a review of it.
func (in *install) runReviewer(t *testing.T, repo *gittest.Repo) (string, string) {
	t.Helper()
	in.path = doubles.InstallFakeClaude(t, "review-lane")
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--batch-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.kill)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	var plan, order string
	in.waitFor("the plan's work", func() bool {
		for _, row := range in.plans() {
			if view := in.planView(row.ID); row.Title == "The plan" && len(view.WorkOrders) == 1 {
				plan, order = row.ID, view.WorkOrders[0].ID
				return true
			}
		}
		return false
	})
	return plan, order
}

// TestExitE2ReviewBatch is E2 with a scripted review lane on the real daemon, driver, and MCP
// server: a member starts a review of a plan in repository files, the lane asks three questions and
// ends, and those reach a person as one batch and one interruption. The plan's work stays out of
// `ready` until the two blocking ones close.
func TestExitE2ReviewBatch(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("plan.md", contract.PlanText("a-plan", "The plan", ""))
	repo.Commit("contract.md", strings.Replace(contract.ContractText("alpha", "Alpha", "One."), `title = "Alpha"`, `title = "Alpha"
plan = "a-plan"`, 1))
	plan, order := in.runReviewer(t, repo)
	in.must("plan", "review", plan, "--id", "review-1")
	if got := in.show("review-1"); got.State != workorders.Ready || got.Kind != workorders.KindContractReview {
		t.Fatalf("a review starts at %+v", got)
	}
	in.must("work", "claim", "review-1", "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", "review-1", "--lane", "lane-1")
	in.waitFor("the review's end", func() bool { return in.show("review-1").State == workorders.Acceptance })
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, "Contract: Alpha") || !strings.Contains(prompts, "Plan: The plan") {
		t.Errorf("the lane was told %q", prompts)
	}

	asked := in.questions()
	if len(asked) != 3 {
		t.Fatalf("questions %+v", asked)
	}
	store := in.ledgerOf()
	for _, row := range asked {
		if row.SubjectKind != planning.SubjectPlan || row.SubjectID != plan {
			t.Errorf("a review's question is about %s %s", row.SubjectKind, row.SubjectID)
		}
		escalate(t, t.Context(), store, row.ID, questions.CategoryProduct)
	}
	in.waitFor("the batch", func() bool { return len(in.batches().Batches) == 1 })
	if batch := in.batches().Batches[0]; len(batch.Items) != 3 {
		t.Fatalf("items %+v", batch.Items)
	}
	if got := in.deliveries(); got != 1 {
		t.Fatalf("%d interruptions", got)
	}

	in.must("work", "accept", "review-1")
	if got := in.show(order); got.State != workorders.ContractReview || got.ReviewedBy == "" {
		t.Fatalf("after a member accepted the review: %+v", got)
	}
	worker := in.workers()[0].ID
	if code, _, stderr := in.run("work", "claim", order, "--worker", worker); code != cli.ExitConflict {
		t.Fatalf("work with blocking questions open: exit %d: %s", code, stderr)
	}
	blocking := []string{}
	for _, row := range asked {
		if row.Blocking {
			blocking = append(blocking, row.ID)
		}
	}
	if len(blocking) != 2 {
		t.Fatalf("blocking %v", blocking)
	}
	in.must("question", "answer", blocking[0], "--answer", "Two retries.")
	if got := in.show(order); got.State != workorders.ContractReview {
		t.Fatalf("one blocking question is still open, and the work reads %s", got.State)
	}
	in.must("question", "answer", blocking[1], "--answer", "It keeps it.")
	in.waitFor("the move to ready", func() bool { return in.show(order).State == workorders.Ready })
	in.must("work", "claim", order, "--worker", worker)
	if got := in.deliveries(); got != 1 {
		t.Fatalf("%d interruptions", got)
	}
}

// TestOnlyReadyCanBeClaimed puts one work order in every state and tries a member's claim on each.
// Ready is the one that takes it, whatever else the claim could say for itself.
func TestOnlyReadyCanBeClaimed(t *testing.T) {
	in := newInstall(t)
	d := in.start("--heartbeat", "30ms")
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	worker := in.workers()[0].ID
	store := in.ledgerOf()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	for _, state := range workorders.States() {
		t.Run(string(state), func(t *testing.T) {
			id := "wo-" + string(state)
			record := workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "Work", State: state}
			err := store.Write(t.Context(), soloScope, "a state to claim from", func(tx ports.WriteTx) error {
				if err := tx.WorkOrders().Insert(record); err != nil {
					return err
				}
				event, err := workorders.CreatedEvent(record, member)
				if err != nil {
					return err
				}
				_, err = tx.Append(event)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			code, _, stderr := in.run("work", "claim", id, "--worker", worker, "--claim-id", "claim-"+id)
			if state == workorders.Ready {
				if code != cli.ExitOK {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				return
			}
			if code != cli.ExitConflict {
				t.Fatalf("a claim from %s: exit %d: %s", state, code, stderr)
			}
			if got := in.show(id); got.State != state || got.Fence != 0 {
				t.Fatalf("a refused claim moved it: %+v", got)
			}
		})
	}
}

// TestAPlanningLaneReachesTheLedgerAtItsAcceptedCommit is D17 through the real daemon: a lane on a
// `plan` work order commits repository files on its branch, nothing it wrote is a record until a
// member accepts, and then intake reads that commit. A file it could not read is reported.
func TestAPlanningLaneReachesTheLedgerAtItsAcceptedCommit(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	in.path = doubles.InstallFakeClaude(t, "planning-lane")
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	in.waitFor("the source", func() bool { return len(in.sources()) == 1 })
	in.must("work", "create", "--id", "plan-1", "--kind", "plan", "--repository", in.sources()[0].Instance,
		"--title", "Plan the retry budget", "--statement", "Write a contract for the retry budget.", "--criterion", "A contract is on the branch.")
	in.must("work", "review", "plan-1")
	in.must("work", "claim", "plan-1", "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", "plan-1", "--lane", "lane-1")
	in.waitFor("the plan's end", func() bool { return in.show("plan-1").State == workorders.Acceptance })
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, `kind = \"contract\"`) && !strings.Contains(prompts, `kind = "contract"`) {
		t.Errorf("the lane was told %q", prompts)
	}

	var standing workorders.AcceptanceView
	if err := json.Unmarshal([]byte(in.must("work", "acceptance", "plan-1", "--json")), &standing); err != nil {
		t.Fatal(err)
	}
	branch := -1
	for i, artifact := range standing.Artifacts {
		if artifact.Kind == ledger.ArtifactBranch {
			branch = i
		}
	}
	if branch < 0 {
		t.Fatalf("no branch on record: %+v", standing.Artifacts)
	}
	commit := standing.Artifacts[branch].Digest
	before := len(in.plans())
	titles := func() []string {
		var out []string
		for _, row := range in.plans() {
			out = append(out, row.Title)
		}
		return out
	}
	if slices.Contains(titles(), "Add a retry budget") {
		t.Fatalf("a record before anyone accepted it: %v", titles())
	}

	in.must("work", "accept", "plan-1")
	in.waitFor("the plan's records", func() bool { return slices.Contains(titles(), "Add a retry budget") })
	if got := len(in.plans()); got != before+1 {
		t.Errorf("%d plans, from %d", got, before)
	}
	var contractPlan api.PlanView
	for _, row := range in.plans() {
		if row.Title == "Add a retry budget" {
			contractPlan = in.planView(row.ID)
		}
	}
	if len(contractPlan.Contracts) != 1 || !strings.HasSuffix(contractPlan.Contracts[0].Source, "@"+commit) {
		t.Fatalf("contracts %+v, want a source at %s", contractPlan.Contracts, commit)
	}
	if len(contractPlan.WorkOrders) != 1 || contractPlan.WorkOrders[0].State != workorders.ContractReview {
		t.Fatalf("work %+v", contractPlan.WorkOrders)
	}
	in.waitFor("the mark on the work order", func() bool { return in.show("plan-1").PlanCommit == commit })
	in.waitFor("the file nobody could read", func() bool {
		for _, source := range in.sources() {
			for _, problem := range source.Problems {
				if problem.Path == "plans/broken.md" {
					return source.Health == planning.HealthBadFiles
				}
			}
		}
		return false
	})
}
