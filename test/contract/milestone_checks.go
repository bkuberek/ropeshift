package contract

import (
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MilestoneChecks hold how intake places a contract among the others of its plan: the milestone it
// belongs to, the shared contracts it builds on, and the dependencies those turn into (FR-MILE-1 to
// FR-MILE-4).
var MilestoneChecks = []Check{
	{"ML1", "a contract's milestone and shared contracts resolve within its plan", checkPlacementResolves},
	{"ML2", "a contract that names a milestone its plan does not hold is a problem, and nothing is recorded from it", checkUnknownMilestone},
	{"ML3", "a contract that uses an unknown, foreign, or unshared contract is a problem, and nothing is recorded from it", checkUnresolvedUses},
	{"ML4", "a milestone that depends on an unknown or foreign milestone is a problem, and nothing is recorded from it", checkMilestoneUnresolvedDependency},
	{"ML5", "a reference resolves whatever order the files sort in", checkReferencesIgnoreFileOrder},
	{"ML6", "a plan whose milestones form a cycle is refused whole, and each file's problem names the cycle", checkMilestoneCycle},
	{"ML7", "a cycle through uses, work orders, or both kinds of dependency refuses its plan", checkOtherCycles},
	{"ML8", "a plan version with a cycle changes nothing the ledger holds, and the plan returns when the cycle is gone", checkCycleLeavesTheLedgerAlone},
	{"ML9", "a contract that names no plan is refused for a cycle among its own work orders", checkContractCycleWithoutAPlan},
	{"ML10", "a milestone dependency becomes a dependency from every work order of the milestone to every work order of the one it waits for", checkMilestoneEdges},
	{"ML11", "a contract that uses a shared contract waits for every work order of it", checkSharedContractEdges},
	{"ML12", "a derived dependency and one a file writes can name the same work order", checkDerivedAndWrittenEdgesCoexist},
	{"ML13", "a work order waits for the work its milestone and its shared contracts depend on", checkReadyWaitsForDerivedEdges},
	{"ML14", "a changed plan removes the derived dependencies it no longer implies and keeps the written ones", checkEdgesFollowThePlan},
	{"ML15", "a poll that changes nothing writes nothing, even when the source moved", checkEdgesAreQuiet},
	{"ML16", "ready work that gains a dependency goes back to review, and started work is left alone", checkReadyWorkGainingADependency},
	{"ML17", "ready work stays ready when the work it gains a dependency on is done", checkReadyWorkKeepsADoneDependency},
	{"ML18", "a contract in no milestone is a problem when its plan has milestones, and is fine when it has none", checkMilestoneRequired},
}

const milestonePlan = "launch"

// milestoneOf renders a milestone file under a plan.
func milestoneOf(id, plan string, priority int, dependsOn ...string) string {
	out := "+++\nkind = \"milestone\"\nid = \"" + id + "\"\ntitle = \"Milestone " + id + "\"\n"
	if plan != "" {
		out += "plan = \"" + plan + "\"\n"
	}
	out += "priority = " + strconv.Itoa(priority) + "\n"
	if len(dependsOn) != 0 {
		out += "depends_on = [\"" + strings.Join(dependsOn, `", "`) + "\"]\n"
	}
	return out + "+++\n\nWhat " + id + " delivers.\n"
}

// orderIn is one work order of a contract file.
type orderIn struct {
	ID        string
	Size      string
	DependsOn []string
}

// placedContract is a contract file that sits among others.
type placedContract struct {
	ID        string
	Plan      string
	Milestone string
	Shared    bool
	Uses      []string
	// Orders are the work orders. A contract that lists none gets one, named `<id>-order`.
	Orders []orderIn
}

func quoted(items []string) string {
	return "[\"" + strings.Join(items, `", "`) + "\"]"
}

func (c placedContract) text() string {
	out := "+++\nkind = \"contract\"\nid = \"" + c.ID + "\"\ntitle = \"The contract " + c.ID + "\"\n"
	if c.Plan != "" {
		out += "plan = \"" + c.Plan + "\"\n"
	}
	if c.Milestone != "" {
		out += "milestone = \"" + c.Milestone + "\"\n"
	}
	if c.Shared {
		out += "shared = true\n"
	}
	if len(c.Uses) != 0 {
		out += "uses = " + quoted(c.Uses) + "\n"
	}
	out += "\n[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n"
	orders := c.Orders
	if len(orders) == 0 {
		orders = []orderIn{{ID: c.ID + "-order"}}
	}
	for _, order := range orders {
		out += "\n[[work_orders]]\nid = \"" + order.ID + "\"\ntitle = \"The work order " + order.ID + "\"\n"
		if order.Size != "" {
			out += "size = \"" + order.Size + "\"\n"
		}
		if len(order.DependsOn) != 0 {
			out += "depends_on = " + quoted(order.DependsOn) + "\n"
		}
	}
	return out + "+++\n\nThe statement of " + c.ID + ".\n"
}

// contractTitled finds the newest version of the contract whose file named this ID.
func (r *Rig) contractTitled(s ports.Store, id string) (planning.Contract, bool, error) {
	contracts, err := r.readContracts(s)
	if err != nil {
		return planning.Contract{}, false, err
	}
	var found planning.Contract
	var held bool
	for _, c := range contracts {
		if c.Title == "The contract "+id && (!held || c.Version > found.Version) {
			found, held = c, true
		}
	}
	return found, held, nil
}

// problemsMention checks that a report holds exactly one problem for a file, and that it names the
// reference that could not be resolved.
func problemsMention(report []planning.SourceProblem, path string, mentions ...string) error {
	var found []planning.SourceProblem
	for _, problem := range report {
		if problem.Path == path {
			found = append(found, problem)
		}
	}
	if len(found) != 1 {
		return Violatedf("%d problems for %s in %+v", len(found), path, report)
	}
	for _, want := range mentions {
		if !strings.Contains(found[0].Detail, want) {
			return Violatedf("the problem for %s is %q, and it does not name %q", path, found[0].Detail, want)
		}
	}
	return nil
}

func (r *Rig) holdsNothingFrom(s ports.Store, ids ...string) error {
	for _, id := range ids {
		if _, held, err := r.contractTitled(s, id); err != nil || held {
			return Violatedf("the ledger holds contract %s (%v)", id, err)
		}
	}
	return nil
}

func checkPlacementResolves(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("client.md", []byte(placedContract{ID: "client", Plan: milestonePlan, Milestone: "m1", Shared: true}.text()))
	source.Write("checkout.md", []byte(placedContract{ID: "checkout", Plan: milestonePlan, Milestone: "m2", Uses: []string{"client"}}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.Versions != 2 || report.WorkOrders != 2 {
		return Violatedf("report %+v", report)
	}
	checkout, held, err := r.contractTitled(s, "checkout")
	if err != nil || !held || checkout.Milestone != "m2" || !slices.Equal(checkout.Uses, []string{"client"}) {
		return Violatedf("checkout %+v (%v): %v", checkout, held, err)
	}
	return r.CheckPlanning(s)
}

func checkUnknownMilestone(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("other.md", []byte(PlanText("elsewhere", "Elsewhere", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("foreign.md", []byte(milestoneOf("foreign", "elsewhere", 1)))
	source.Write("unknown.md", []byte(placedContract{ID: "unknown", Plan: milestonePlan, Milestone: "nowhere"}.text()))
	source.Write("crossplan.md", []byte(placedContract{ID: "crossplan", Plan: milestonePlan, Milestone: "foreign"}.text()))
	source.Write("planless.md", []byte(placedContract{ID: "planless", Milestone: "m1"}.text()))
	source.Write("fine.md", []byte(placedContract{ID: "fine", Plan: milestonePlan, Milestone: "m1"}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 3 {
		return Violatedf("report %+v", report)
	}
	for _, file := range [][2]string{{"unknown.md", "nowhere"}, {"crossplan.md", "foreign"}, {"planless.md", "m1"}} {
		if err := problemsMention(report.Problems, file[0], file[1]); err != nil {
			return err
		}
	}
	if err := r.holdsNothingFrom(s, "unknown", "crossplan", "planless"); err != nil {
		return err
	}
	// The file that names a milestone its plan holds is recorded, and a fixed file is read on the next poll.
	source.Write("unknown.md", []byte(placedContract{ID: "unknown", Plan: milestonePlan, Milestone: "m1"}.text()))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 2 {
		return Violatedf("after the fix %+v", report)
	}
	return r.CheckPlanning(s)
}

func checkUnresolvedUses(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("other.md", []byte(PlanText("elsewhere", "Elsewhere", "")))
	source.Write("shared.md", []byte(placedContract{ID: "shared", Plan: milestonePlan, Shared: true}.text()))
	source.Write("private.md", []byte(placedContract{ID: "private", Plan: milestonePlan}.text()))
	source.Write("foreign.md", []byte(placedContract{ID: "foreign", Plan: "elsewhere", Shared: true}.text()))
	source.Write("nowhere.md", []byte(placedContract{ID: "nowhere", Plan: milestonePlan, Uses: []string{"missing"}}.text()))
	source.Write("unshared.md", []byte(placedContract{ID: "unshared", Plan: milestonePlan, Uses: []string{"private"}}.text()))
	source.Write("crossplan.md", []byte(placedContract{ID: "crossplan", Plan: milestonePlan, Uses: []string{"foreign"}}.text()))
	source.Write("planless.md", []byte(placedContract{ID: "planless", Uses: []string{"shared"}}.text()))
	source.Write("fine.md", []byte(placedContract{ID: "fine", Plan: milestonePlan, Uses: []string{"shared"}}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 4 || len(report.Problems) != 4 {
		return Violatedf("report %+v", report)
	}
	for _, file := range [][2]string{{"nowhere.md", "missing"}, {"unshared.md", "private"}, {"crossplan.md", "foreign"}, {"planless.md", "shared"}} {
		if err := problemsMention(report.Problems, file[0], file[1]); err != nil {
			return err
		}
	}
	if err := r.holdsNothingFrom(s, "nowhere", "unshared", "crossplan", "planless"); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkMilestoneUnresolvedDependency(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("other.md", []byte(PlanText("elsewhere", "Elsewhere", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("foreign.md", []byte(milestoneOf("foreign", "elsewhere", 1)))
	source.Write("unknown.md", []byte(milestoneOf("unknown", milestonePlan, 2, "m1", "nowhere")))
	source.Write("crossplan.md", []byte(milestoneOf("crossplan", milestonePlan, 3, "foreign")))
	source.Write("fine.md", []byte(milestoneOf("fine", milestonePlan, 4, "m1")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 2 {
		return Violatedf("report %+v", report)
	}
	for _, file := range [][2]string{{"unknown.md", "nowhere"}, {"crossplan.md", "foreign"}} {
		if err := problemsMention(report.Problems, file[0], file[1]); err != nil {
			return err
		}
	}
	documents, err := r.documentRecords(s)
	if err != nil {
		return err
	}
	var held []string
	for _, document := range documents {
		held = append(held, document.ID)
	}
	slices.Sort(held)
	if want := []string{"milestone:fine", "milestone:foreign", "milestone:m1"}; !slices.Equal(held, want) {
		return Violatedf("documents %v, want %v", held, want)
	}
	return r.CheckPlanning(s)
}

func checkReferencesIgnoreFileOrder(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	// Each file sorts before the one it refers to.
	source.Write("a-plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("a-checkout.md", []byte(placedContract{ID: "checkout", Plan: milestonePlan, Milestone: "m2", Uses: []string{"client"}}.text()))
	source.Write("b-client.md", []byte(placedContract{ID: "client", Plan: milestonePlan, Milestone: "m1", Shared: true}.text()))
	source.Write("c-m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("a-m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.Versions != 2 {
		return Violatedf("report %+v", report)
	}
	return r.CheckPlanning(s)
}

const calmPlan = "calm"

func checkMilestoneCycle(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("calm-plan.md", []byte(PlanText(calmPlan, "Calm", "")))
	source.Write("calm.md", []byte(placedContract{ID: "calm", Plan: calmPlan}.text()))
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1, "m2")))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("contract.md", []byte(placedContract{ID: "inside", Plan: milestonePlan, Milestone: "m1"}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 4 || report.Versions != 1 || report.WorkOrders != 1 || report.Records != 1 {
		return Violatedf("report %+v", report)
	}
	for _, file := range []string{"plan.md", "m1.md", "m2.md", "contract.md"} {
		if err := problemsMention(report.Problems, file, "milestone:m1 -> milestone:m2 -> milestone:m1", milestonePlan); err != nil {
			return err
		}
	}
	if _, err := r.planID(s, "Launch"); err == nil {
		return Violatedf("the plan with a cycle was recorded")
	}
	documents, err := r.documentRecords(s)
	if err != nil || len(documents) != 0 {
		return Violatedf("documents %+v: %v", documents, err)
	}
	if err := r.holdsNothingFrom(s, "inside"); err != nil {
		return err
	}
	// The binding keeps one problem for each file, so a person sees the cycle where the files are.
	binding, err := r.binding(s, control.BindingID("fake", "memory"))
	if err != nil || len(binding.Problems) != 4 {
		return Violatedf("binding %+v: %v", binding, err)
	}
	return r.CheckPlanning(s)
}

func checkOtherCycles(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	// Contracts that use each other.
	source.Write("u-plan.md", []byte(PlanText("uses", "Uses", "")))
	source.Write("u-x.md", []byte(placedContract{ID: "ux", Plan: "uses", Shared: true, Uses: []string{"uy"}}.text()))
	source.Write("u-y.md", []byte(placedContract{ID: "uy", Plan: "uses", Shared: true, Uses: []string{"ux"}}.text()))
	// Work orders that depend on each other across contracts.
	source.Write("o-plan.md", []byte(PlanText("orders", "Orders", "")))
	source.Write("o-p.md", []byte(placedContract{ID: "op", Plan: "orders", Orders: []orderIn{{ID: "p1", DependsOn: []string{"q1"}}}}.text()))
	source.Write("o-q.md", []byte(placedContract{ID: "oq", Plan: "orders", Orders: []orderIn{{ID: "q1", DependsOn: []string{"p1"}}}}.text()))
	// Work that is earlier by milestone and later by its own dependency.
	source.Write("x-plan.md", []byte(PlanText("mixed", "Mixed", "")))
	source.Write("x-m1.md", []byte(milestoneOf("first", "mixed", 1)))
	source.Write("x-m2.md", []byte(milestoneOf("second", "mixed", 2, "first")))
	source.Write("x-early.md", []byte(placedContract{ID: "early", Plan: "mixed", Milestone: "first", Orders: []orderIn{{ID: "e1", DependsOn: []string{"l1"}}}}.text()))
	source.Write("x-late.md", []byte(placedContract{ID: "late", Plan: "mixed", Milestone: "second", Orders: []orderIn{{ID: "l1"}}}.text()))
	// Shared work in a milestone that waits for the milestone of the contract that uses it.
	source.Write("s-plan.md", []byte(PlanText("shared", "Shared", "")))
	source.Write("s-m1.md", []byte(milestoneOf("one", "shared", 1)))
	source.Write("s-m2.md", []byte(milestoneOf("two", "shared", 2, "one")))
	source.Write("s-app.md", []byte(placedContract{ID: "app", Plan: "shared", Milestone: "one", Uses: []string{"base"}}.text()))
	source.Write("s-base.md", []byte(placedContract{ID: "base", Plan: "shared", Milestone: "two", Shared: true}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 16 || report.Versions != 0 || report.WorkOrders != 0 || report.Records != 0 {
		return Violatedf("report %+v", report)
	}
	cycles := map[string]string{
		"u-": "contract:ux -> contract:uy -> contract:ux",
		"o-": "work_order:p1 -> work_order:q1 -> work_order:p1",
		"x-": "work_order:e1 -> work_order:l1 -> work_order:e1",
		"s-": "work_order:app-order -> work_order:base-order -> work_order:app-order",
	}
	for prefix, cycle := range cycles {
		for _, problem := range report.Problems {
			if strings.HasPrefix(problem.Path, prefix) && !strings.Contains(problem.Detail, cycle) {
				return Violatedf("the problem for %s is %q, and it does not name %q", problem.Path, problem.Detail, cycle)
			}
		}
	}
	return r.CheckPlanning(s)
}

func checkCycleLeavesTheLedgerAlone(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("contract.md", []byte(placedContract{ID: "inside", Plan: milestonePlan, Milestone: "m2"}.text()))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 || report.Versions != 1 {
		return Violatedf("report %+v: %v", report, err)
	}
	before, err := r.versionsHeld(s)
	if err != nil {
		return err
	}
	// A new version of a milestone closes a loop, and the contract changes beside it.
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1, "m2")))
	source.Write("contract.md", []byte(strings.Replace(placedContract{ID: "inside", Plan: milestonePlan, Milestone: "m2"}.text(), "The statement", "A new statement", 1)))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 4 || report.Versions != 0 || report.Records != 0 {
		return Violatedf("report %+v", report)
	}
	after, err := r.versionsHeld(s)
	if err != nil {
		return err
	}
	if !slices.Equal(before, after) {
		return Violatedf("the ledger held %v, and after a plan version with a cycle %v", before, after)
	}
	// Once the loop is gone the plan reads again, and a file that says what the ledger holds adds nothing.
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.Versions != 1 {
		return Violatedf("after the fix %+v", report)
	}
	return r.CheckPlanning(s)
}

// versionsHeld lists every contract, document, and plan version the ledger holds, as `id@version`.
func (r *Rig) versionsHeld(s ports.Store) ([]string, error) {
	var out []string
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		contracts, err := tx.Planning().Contracts()
		if err != nil {
			return err
		}
		for _, c := range contracts {
			out = append(out, "contract "+planning.ContractKey(c.ID, c.Version))
		}
		documents, err := tx.Planning().DocumentVersions()
		if err != nil {
			return err
		}
		for _, d := range documents {
			out = append(out, "document "+planning.ContractKey(d.DocumentID, d.Version))
		}
		plans, err := tx.Planning().PlanVersions()
		if err != nil {
			return err
		}
		for _, p := range plans {
			out = append(out, "plan "+planning.ContractKey(p.PlanID, p.Version))
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

func checkContractCycleWithoutAPlan(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("loop.md", []byte(placedContract{ID: "loopy", Orders: []orderIn{
		{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}},
	}}.text()))
	source.Write("line.md", []byte(placedContract{ID: "line", Orders: []orderIn{
		{ID: "c"}, {ID: "d", DependsOn: []string{"c"}},
	}}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 1 || report.Versions != 1 || report.WorkOrders != 2 {
		return Violatedf("report %+v", report)
	}
	if err := problemsMention(report.Problems, "loop.md", "work_order:a -> work_order:b -> work_order:a", "loopy"); err != nil {
		return err
	}
	if err := r.holdsNothingFrom(s, "loopy"); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

// edgeStrings lists every dependency as `<order> -> <target> [<kind>]`, naming work orders by the ID
// their file gave them, and sorted.
func (r *Rig) edgeStrings(s ports.Store) ([]string, error) {
	orders, err := r.readOrders(s)
	if err != nil {
		return nil, err
	}
	native := map[string]string{}
	for _, order := range orders {
		native[order.ID] = strings.TrimPrefix(order.Title, "The work order ")
	}
	var out []string
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().AllDependencies()
		for _, d := range all {
			if d.Adapter == "" || d.Instance == "" {
				return Violatedf("dependency %+v names no source", d)
			}
			out = append(out, native[d.WorkOrder]+" -> "+d.DependsOn+" ["+string(d.Kind)+"]")
		}
		return err
	})
	slices.Sort(out)
	return out, err
}

func wantEdges(r *Rig, s ports.Store, want ...string) error {
	got, err := r.edgeStrings(s)
	if err != nil {
		return err
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return Violatedf("dependencies\n%v\nwant\n%v", got, want)
	}
	return nil
}

func checkMilestoneEdges(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("a.md", []byte(placedContract{ID: "a", Plan: milestonePlan, Milestone: "m1", Orders: []orderIn{{ID: "a1"}, {ID: "a2"}}}.text()))
	source.Write("b.md", []byte(placedContract{ID: "b", Plan: milestonePlan, Milestone: "m1", Orders: []orderIn{{ID: "b1"}}}.text()))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Orders: []orderIn{{ID: "c1"}, {ID: "c2"}}}.text()))
	// A milestone that waits for nothing and that nothing waits for: its work stays out of the edges.
	source.Write("m3.md", []byte(milestoneOf("m3", milestonePlan, 3)))
	source.Write("d.md", []byte(placedContract{ID: "d", Plan: milestonePlan, Milestone: "m3", Orders: []orderIn{{ID: "d1"}}}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.WorkOrders != 6 {
		return Violatedf("report %+v", report)
	}
	if err := wantEdges(r, s,
		"c1 -> a1 [milestone]", "c1 -> a2 [milestone]", "c1 -> b1 [milestone]",
		"c2 -> a1 [milestone]", "c2 -> a2 [milestone]", "c2 -> b1 [milestone]",
	); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkSharedContractEdges(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("shared.md", []byte(placedContract{ID: "shared", Plan: milestonePlan, Shared: true, Orders: []orderIn{{ID: "s1"}, {ID: "s2"}}}.text()))
	source.Write("other.md", []byte(placedContract{ID: "other", Plan: milestonePlan, Shared: true, Orders: []orderIn{{ID: "o1"}}}.text()))
	source.Write("u.md", []byte(placedContract{ID: "u", Plan: milestonePlan, Uses: []string{"shared"}, Orders: []orderIn{{ID: "u1"}}}.text()))
	source.Write("v.md", []byte(placedContract{ID: "v", Plan: milestonePlan, Uses: []string{"shared", "other"}, Orders: []orderIn{{ID: "v1"}}}.text()))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	if err := wantEdges(r, s,
		"u1 -> s1 [shared_contract]", "u1 -> s2 [shared_contract]",
		"v1 -> s1 [shared_contract]", "v1 -> s2 [shared_contract]", "v1 -> o1 [shared_contract]",
	); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkDerivedAndWrittenEdgesCoexist(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("a.md", []byte(placedContract{ID: "a", Plan: milestonePlan, Milestone: "m1", Shared: true, Orders: []orderIn{{ID: "a1"}}}.text()))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Uses: []string{"a"}, Orders: []orderIn{{ID: "c1", DependsOn: []string{"a1"}}}}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.WorkOrders != 2 {
		return Violatedf("report %+v", report)
	}
	if err := wantEdges(r, s, "c1 -> a1 [blocking]", "c1 -> a1 [milestone]", "c1 -> a1 [shared_contract]"); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

// forceState puts a work order in a state the way a finished or started one would stand, with an event
// for the move.
func (r *Rig) forceState(s ports.Store, id string, to workorders.State) error {
	return s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		moved, err := tx.WorkOrders().Move(id, current.State, to)
		if err != nil || !moved {
			return Violatedf("%s did not move to %s (%v)", id, to, err)
		}
		_, err = tx.Append(r.noteDraft(r.ID("move")))
		return err
	})
}

// forceDone puts a work order in `done`.
func (r *Rig) forceDone(s ports.Store, id string) error {
	return r.forceState(s, id, workorders.Done)
}

func (r *Rig) reviewTitled(s ports.Store, title string) (control.ReviewResult, error) {
	order, err := r.orderByTitle(s, title)
	if err != nil {
		return control.ReviewResult{}, err
	}
	return r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
}

func checkReadyWaitsForDerivedEdges(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("a.md", []byte(placedContract{ID: "a", Plan: milestonePlan, Milestone: "m1", Shared: true, Orders: []orderIn{{ID: "a1"}}}.text()))
	source.Write("b.md", []byte(placedContract{ID: "b", Plan: milestonePlan, Milestone: "m1", Orders: []orderIn{{ID: "b1"}}}.text()))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Orders: []orderIn{{ID: "c1"}}}.text()))
	source.Write("u.md", []byte(placedContract{ID: "u", Plan: milestonePlan, Milestone: "m1", Uses: []string{"a"}, Orders: []orderIn{{ID: "u1"}}}.text()))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	// The work of a shared contract is built first, and the work of a milestone waits for its dependency.
	for _, title := range []string{"The work order u1", "The work order c1"} {
		result, err := r.reviewTitled(s, title)
		if err != nil {
			return err
		}
		if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != workorders.CheckDependencies {
			return Violatedf("%s: %+v", title, result)
		}
	}
	a1, err := r.orderByTitle(s, "The work order a1")
	if err != nil {
		return err
	}
	if err := r.forceDone(s, a1.ID); err != nil {
		return err
	}
	result, err := r.reviewTitled(s, "The work order u1")
	if err != nil {
		return err
	}
	if result.State != workorders.Ready || len(result.Failures) != 0 {
		return Violatedf("u1 after its shared contract is done: %+v", result)
	}
	// A milestone waits for all of the one before it: b1 is not done yet.
	if result, err = r.reviewTitled(s, "The work order c1"); err != nil || result.State != workorders.ContractReview || len(result.Failures) != 1 {
		return Violatedf("c1 while b1 is not done: %+v: %v", result, err)
	}
	b1, err := r.orderByTitle(s, "The work order b1")
	if err != nil {
		return err
	}
	if err := r.forceDone(s, b1.ID); err != nil {
		return err
	}
	// The milestone's work is all done when u1 is too.
	u1, err := r.orderByTitle(s, "The work order u1")
	if err != nil {
		return err
	}
	if err := r.forceDone(s, u1.ID); err != nil {
		return err
	}
	if result, err = r.reviewTitled(s, "The work order c1"); err != nil || result.State != workorders.Ready {
		return Violatedf("c1 after its milestone is done: %+v: %v", result, err)
	}
	return nil
}

func checkEdgesFollowThePlan(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("a.md", []byte(placedContract{ID: "a", Plan: milestonePlan, Milestone: "m1", Shared: true, Orders: []orderIn{{ID: "a1"}}}.text()))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Orders: []orderIn{{ID: "c1", DependsOn: []string{"a1"}}}}.text()))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	if err := wantEdges(r, s, "c1 -> a1 [blocking]", "c1 -> a1 [milestone]"); err != nil {
		return err
	}
	// The milestone stops waiting, and only the edge a file wrote is left.
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2)))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Edges != 1 {
		return Violatedf("report %+v", report)
	}
	if err := wantEdges(r, s, "c1 -> a1 [blocking]"); err != nil {
		return err
	}
	if err := r.CheckPlanning(s); err != nil {
		return err
	}
	// The contract now uses the shared one, and the milestone waits again: both derived edges stand.
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Uses: []string{"a"}, Orders: []orderIn{{ID: "c1", DependsOn: []string{"a1"}}}}.text()))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	if err := wantEdges(r, s, "c1 -> a1 [blocking]", "c1 -> a1 [milestone]", "c1 -> a1 [shared_contract]"); err != nil {
		return err
	}
	// The contract moves to a milestone that waits for nothing, and its edge to the old milestone's
	// dependency goes with it.
	source.Write("m3.md", []byte(milestoneOf("m3", milestonePlan, 3)))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m3", Uses: []string{"a"}, Orders: []orderIn{{ID: "c1", DependsOn: []string{"a1"}}}}.text()))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	if err := wantEdges(r, s, "c1 -> a1 [blocking]", "c1 -> a1 [shared_contract]"); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkEdgesAreQuiet(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	source.Write("a.md", []byte(placedContract{ID: "a", Plan: milestonePlan, Milestone: "m1", Shared: true}.text()))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Uses: []string{"a"}}.text()))
	first, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if first.Edges != 2 {
		return Violatedf("first report %+v", first)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	// The source moves with a file that is not ours, so the whole tree is read again.
	source.Write("notes.md", []byte("# Prose\n"))
	second, err := r.intake(s, source)
	if err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before || second.Edges != 0 || second.Versions != 0 {
		return Violatedf("a poll that found nothing new moved the revision from %d to %d: %+v", before, after, second)
	}
	return r.CheckRevisions(s, r.Scope)
}

// twoMilestones writes a plan whose second milestone waits for the first only when it is told to.
func twoMilestones(source *fake.WorkSource, secondWaits bool) {
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	if secondWaits {
		source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	} else {
		source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2)))
	}
	source.Write("a.md", []byte(placedContract{ID: "a", Plan: milestonePlan, Milestone: "m1", Orders: []orderIn{{ID: "a1"}}}.text()))
	source.Write("c.md", []byte(placedContract{ID: "c", Plan: milestonePlan, Milestone: "m2", Orders: []orderIn{{ID: "c1"}, {ID: "c2"}}}.text()))
}

func (r *Rig) stateOfTitled(s ports.Store, title string) (workorders.WorkOrder, error) {
	return r.orderByTitle(s, "The work order "+title)
}

func checkReadyWorkGainingADependency(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	twoMilestones(source, false)
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	for _, title := range []string{"The work order c1", "The work order c2"} {
		if result, err := r.reviewTitled(s, title); err != nil || result.State != workorders.Ready {
			return Violatedf("%s: %+v: %v", title, result, err)
		}
	}
	// One of them is claimed and running by the time the plan changes.
	running, err := r.stateOfTitled(s, "c2")
	if err != nil {
		return err
	}
	if err := r.forceState(s, running.ID, workorders.Running); err != nil {
		return err
	}
	twoMilestones(source, true)
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Edges != 2 {
		return Violatedf("report %+v", report)
	}
	held, err := r.stateOfTitled(s, "c1")
	if err != nil {
		return err
	}
	if held.State != workorders.ContractReview || held.ReviewedBy == "" {
		return Violatedf("c1 gained a dependency on work that is not done and is in %s, reviewed by %q", held.State, held.ReviewedBy)
	}
	if started, err := r.stateOfTitled(s, "c2"); err != nil || started.State != workorders.Running {
		return Violatedf("c2 was running and is %+v: %v", started, err)
	}
	// The review stays: the work returns to ready when the work it waits for is done, with no new review.
	if moved, err := r.NewOps(s).Promote(r.Ctx, r.Scope, r.Actor); err != nil || len(moved) != 0 {
		return Violatedf("work moved to ready while it waits: %v: %v", moved, err)
	}
	first, err := r.stateOfTitled(s, "a1")
	if err != nil {
		return err
	}
	if err := r.forceDone(s, first.ID); err != nil {
		return err
	}
	if moved, err := r.NewOps(s).Promote(r.Ctx, r.Scope, r.Actor); err != nil || !slices.Equal(moved, []string{held.ID}) {
		return Violatedf("after its dependency is done %v moved: %v", moved, err)
	}
	return r.CheckPlanning(s)
}

func checkReadyWorkKeepsADoneDependency(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	twoMilestones(source, false)
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	first, err := r.stateOfTitled(s, "a1")
	if err != nil {
		return err
	}
	if err := r.forceDone(s, first.ID); err != nil {
		return err
	}
	if result, err := r.reviewTitled(s, "The work order c1"); err != nil || result.State != workorders.Ready {
		return Violatedf("c1: %+v: %v", result, err)
	}
	twoMilestones(source, true)
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Edges == 0 {
		return Violatedf("report %+v", report)
	}
	if kept, err := r.stateOfTitled(s, "c1"); err != nil || kept.State != workorders.Ready {
		return Violatedf("c1 gained a dependency on work that is done and is %+v: %v", kept, err)
	}
	return r.CheckPlanning(s)
}

func checkMilestoneRequired(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("placed.md", []byte(placedContract{ID: "placed", Plan: milestonePlan, Milestone: "m1"}.text()))
	source.Write("bare.md", []byte(placedContract{ID: "bare", Plan: milestonePlan}.text()))
	// A plan with no milestones keeps its contracts as earlier files wrote them.
	source.Write("free-plan.md", []byte(PlanText("free", "Free", "")))
	source.Write("free.md", []byte(placedContract{ID: "free", Plan: "free"}.text()))
	// A contract that names no plan has a plan of its own, with no milestones.
	source.Write("alone.md", []byte(placedContract{ID: "alone"}.text()))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 3 || len(report.Problems) != 1 {
		return Violatedf("report %+v", report)
	}
	if err := problemsMention(report.Problems, "bare.md", "bare", "milestone", milestonePlan); err != nil {
		return err
	}
	if err := r.holdsNothingFrom(s, "bare"); err != nil {
		return err
	}
	// The plan's milestone is in the ledger and not in this read, and the plan still has it.
	source.Write("m1.md", []byte("# Prose, not a milestone any more\n"))
	report, err = r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 0 || len(report.Problems) != 1 {
		return Violatedf("with the milestone file gone %+v", report)
	}
	// Naming a milestone of the plan fixes it.
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("bare.md", []byte(placedContract{ID: "bare", Plan: milestonePlan, Milestone: "m1"}.text()))
	if report, err = r.intake(s, source); err != nil || len(report.Problems) != 0 || report.Versions != 1 {
		return Violatedf("after the fix %+v: %v", report, err)
	}
	return r.CheckPlanning(s)
}
