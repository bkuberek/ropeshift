package contract

import (
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
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
