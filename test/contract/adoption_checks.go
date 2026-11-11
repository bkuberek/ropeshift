package contract

import (
	"slices"
	"strconv"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// AdoptionChecks hold what the format says of work that already exists (ADR-0013, ADR-0023): a requirement
// the code meets keeps its status and its evidence, an existing contract or milestone resolves every
// dependency on it at once, and a plan keeps what an adoption left out with its reason.
var AdoptionChecks = []Check{
	{"AD1", "a requirement the code meets is a version with its status and its evidence, and saying so of an open one is a new version", checkMetRequirements},
	{"AD2", "work that uses an existing shared contract, or is in a milestone after an existing one, waits for nothing on their account", checkExistingWorkResolvesAtOnce},
	{"AD3", "a contract that becomes existing stops holding back the work that uses it", checkAContractThatBecomesExisting},
	{"AD4", "a contract with work orders in an existing milestone is a problem, and nothing is recorded from it", checkNoNewWorkInAnExistingMilestone},
	{"AD5", "an existing contract's version says so, a plan version keeps what the plan left out with each reason, and a rebuild keeps both", checkExistingAndLeftOutReplay},
}

const adoptionPlan = "adopted"

// existingMilestoneOf renders a milestone the code already delivers.
func existingMilestoneOf(id, plan string, priority int) string {
	return "+++\nkind = \"milestone\"\nid = \"" + id + "\"\ntitle = \"Milestone " + id + "\"\nplan = \"" + plan + "\"\npriority = " +
		strconv.Itoa(priority) + "\nexisting = true\n+++\n\nWhat the code delivers.\n"
}

// existingContractOf renders a shared contract the code already delivers, with no work orders.
func existingContractOf(id, plan, milestone string) string {
	return "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"The contract " + id + "\"\nplan = \"" + plan + "\"\nmilestone = \"" +
		milestone + "\"\nshared = true\nexisting = true\n\n[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n+++\n\nWhat the code offers.\n"
}

func (r *Rig) requirementOf(s ports.Store, key string) (planning.Requirement, error) {
	var requirement planning.Requirement
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		requirement, err = tx.Planning().Requirement(key)
		return err
	})
	return requirement, err
}

func checkMetRequirements(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	file := func(secondMet bool) string {
		out := "+++\nkind = \"requirements\"\nid = \"payments\"\n\n[[requirement]]\nid = \"PAY-1\"\ntext = \"One client\"\nstatus = \"met\"\n" +
			"evidence = [\"api:client.go\", \"api:client_test.go\"]\n\n[[requirement]]\nid = \"PAY-2\"\ntext = \"It retries\"\n"
		if secondMet {
			out += "status = \"met\"\nevidence = [\"api:retry.go\"]\n"
		}
		return out + "+++\n"
	}
	source.Write("requirements.md", []byte(file(false)))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	met, err := r.requirementOf(s, "PAY-1")
	if err != nil || met.Status != planning.RequirementMet || !slices.Equal(met.Evidence, []string{"api:client.go", "api:client_test.go"}) {
		return Violatedf("a met requirement reads %+v: %v", met, err)
	}
	open, err := r.requirementOf(s, "PAY-2")
	if err != nil || open.Status == planning.RequirementMet || len(open.Evidence) != 0 || open.Digest != planning.RequirementDigest("PAY-2", "It retries") {
		return Violatedf("an open requirement reads %+v: %v", open, err)
	}
	source.Write("requirements.md", []byte(file(true)))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	now, err := r.requirementOf(s, "PAY-2")
	if err != nil || now.Version != 2 || now.Status != planning.RequirementMet || !slices.Equal(now.Evidence, []string{"api:retry.go"}) {
		return Violatedf("a requirement the code came to meet reads %+v: %v", now, err)
	}
	if again, err := r.requirementOf(s, "PAY-1"); err != nil || again.Version != 1 {
		return Violatedf("a requirement that did not change reads %+v: %v", again, err)
	}
	return r.CheckPlanning(s)
}

func checkExistingWorkResolvesAtOnce(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(adoptionPlan, "Adopted", "")))
	source.Write("m0.md", []byte(existingMilestoneOf("m0", adoptionPlan, 0)))
	source.Write("m1.md", []byte(milestoneOf("m1", adoptionPlan, 1, "m0")))
	source.Write("client.md", []byte(existingContractOf("client", adoptionPlan, "m0")))
	source.Write("retry.md", []byte(placedContract{ID: "retry", Plan: adoptionPlan, Milestone: "m1", Uses: []string{"client"}, Orders: []orderIn{{ID: "r1"}}}.text()))
	report, err := r.intake(s, source)
	if err != nil || len(report.Problems) != 0 || report.WorkOrders != 1 {
		return Violatedf("report %+v: %v", report, err)
	}
	if err := wantEdges(r, s); err != nil {
		return err
	}
	order, err := r.orderByTitle(s, "The work order r1")
	if err != nil {
		return err
	}
	result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
	if err != nil || result.State != workorders.Ready {
		return Violatedf("work that builds on what exists reads %+v: %v", result, err)
	}
	if client, held, err := r.contractTitled(s, "client"); err != nil || !held || !client.Existing || len(client.Orders) != 0 {
		return Violatedf("the existing contract reads %+v, %v: %v", client, held, err)
	}
	return r.CheckPlanning(s)
}

func checkAContractThatBecomesExisting(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(adoptionPlan, "Adopted", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", adoptionPlan, 1)))
	source.Write("client.md", []byte(placedContract{ID: "client", Plan: adoptionPlan, Milestone: "m1", Shared: true, Orders: []orderIn{{ID: "k1"}}}.text()))
	source.Write("retry.md", []byte(placedContract{ID: "retry", Plan: adoptionPlan, Milestone: "m1", Uses: []string{"client"}, Orders: []orderIn{{ID: "r1"}}}.text()))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	if err := wantEdges(r, s, "r1 -> k1 [shared_contract]"); err != nil {
		return err
	}
	// The person finds the code delivers the client, and the plan says so.
	source.Write("client.md", []byte(existingContractOf("client", adoptionPlan, "m1")))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	if err := wantEdges(r, s); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkNoNewWorkInAnExistingMilestone(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(adoptionPlan, "Adopted", "")))
	source.Write("m0.md", []byte(existingMilestoneOf("m0", adoptionPlan, 0)))
	source.Write("legacy.md", []byte(placedContract{ID: "legacy", Plan: adoptionPlan, Milestone: "m0", Orders: []orderIn{{ID: "l1"}}}.text()))
	source.Write("client.md", []byte(existingContractOf("client", adoptionPlan, "m0")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if err := problemsMention(report.Problems, "legacy.md", "legacy", "m0", "exists"); err != nil {
		return err
	}
	if _, err := r.orderByTitle(s, "The work order l1"); err == nil {
		return Violatedf("new work in an existing milestone was recorded")
	}
	if _, held, err := r.contractTitled(s, "client"); err != nil || !held {
		return Violatedf("an existing contract in an existing milestone was not recorded: %v", err)
	}
	return nil
}

func checkExistingAndLeftOutReplay(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \""+adoptionPlan+"\"\ntitle = \"Adopted\"\n\n"+
		"[[left_out]]\nref = \"branch:spike/old-ui\"\nreason = \"An abandoned experiment\"\n\n"+
		"[[left_out]]\nref = \"branch:wip/docs\"\nreason = \"The person finishes it by hand\"\n+++\n\nHow.\n"))
	source.Write("m0.md", []byte(existingMilestoneOf("m0", adoptionPlan, 0)))
	source.Write("client.md", []byte(existingContractOf("client", adoptionPlan, "m0")))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 || report.PlanVersions != 1 {
		return Violatedf("report %+v: %v", report, err)
	}
	client, held, err := r.contractTitled(s, "client")
	if err != nil || !held || !client.Existing {
		return Violatedf("the existing contract reads %+v: %v", client, err)
	}
	versions, err := r.planVersionsOf(s, client.PlanID)
	if err != nil || len(versions) != 1 {
		return Violatedf("plan versions %+v: %v", versions, err)
	}
	want := []planning.LeftOut{{Ref: "branch:spike/old-ui", Reason: "An abandoned experiment"}, {Ref: "branch:wip/docs", Reason: "The person finishes it by hand"}}
	if !slices.Equal(versions[0].LeftOut, want) {
		return Violatedf("the plan version left out %+v, want %+v", versions[0].LeftOut, want)
	}
	if err := r.CheckPlanning(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if rebuilt, _, err := r.contractTitled(s, "client"); err != nil || !rebuilt.Existing {
		return Violatedf("a rebuild left the contract %+v: %v", rebuilt, err)
	}
	if versions, err := r.planVersionsOf(s, client.PlanID); err != nil || len(versions) != 1 || !slices.Equal(versions[0].LeftOut, want) {
		return Violatedf("a rebuild left the plan versions %+v: %v", versions, err)
	}
	return nil
}
