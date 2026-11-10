package contract

import (
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TraceChecks hold a contract's criteria to the requirements of its plan: a criterion must serve a
// requirement the plan has, intake reports one that does not, and the definition of ready fails
// for the work under that contract while the problem stands (FR-DOC-5).
var TraceChecks = []Check{
	{"TR1", "a criterion that serves no requirement is a problem, and its work order does not reach ready", checkTraceUnmapped},
	{"TR2", "a criterion that names an unknown requirement is a problem, and its work order does not reach ready", checkTraceUnknownRequirement},
	{"TR3", "a contract whose criteria all serve a requirement reaches ready", checkTraceMapped},
	{"TR4", "the problem holds back only that contract's work, and a plan with no requirements document holds back none", checkTraceOnlyThatContract},
	{"TR5", "a requirements document that arrives later holds back a contract that did not change", checkTraceDocumentLater},
	{"TR6", "a requirement that goes away holds back the criteria that served it", checkTraceRequirementRemoved},
	{"TR7", "a problem is reported each time it stands and moves nothing when it repeats", checkTraceProblemRepeats},
}

const traceFile = "traced.md"

// served renders a contract in a plan whose criteria c1, c2, and so on serve the requirements listed
// for each. A criterion with an empty list serves none.
func served(id, plan string, criteria ...[]string) string {
	out := "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"The contract " + id + "\"\nplan = \"" + plan + "\"\n"
	for i, keys := range criteria {
		out += "\n[[criteria]]\nid = \"c" + string(rune('1'+i)) + "\"\ntext = \"Criterion " + string(rune('1'+i)) + "\"\n"
		if len(keys) != 0 {
			out += "requirements = [\"" + strings.Join(keys, `", "`) + "\"]\n"
		}
	}
	return out + "\n[[work_orders]]\nid = \"" + id + "-order\"\ntitle = \"The work order of " + id + "\"\n+++\n\nThe statement.\n"
}

// tracedSource holds a plan with a requirements document, PAY-12 and PAY-13.
func tracedSource() *fake.WorkSource {
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write("requirements.md", []byte(requirementsOf("payments", documentsPlan, "PAY-12", "PAY-13")))
	return source
}

// passReview passes a work order's review as a member does, and says how it came out.
func (r *Rig) passReview(s ports.Store, title string) (control.ReviewResult, error) {
	order, err := r.orderByTitle(s, title)
	if err != nil {
		return control.ReviewResult{}, err
	}
	return r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
}

// heldBackBy holds a review result to one failed check that names what a test expects.
func heldBackBy(result control.ReviewResult, check workorders.ReadyCheck, names ...string) error {
	if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != check {
		return Violatedf("review result %+v", result)
	}
	for _, name := range names {
		if !strings.Contains(result.Failures[0].Detail, name) {
			return Violatedf("the failure %q does not name %q", result.Failures[0].Detail, name)
		}
	}
	return nil
}

// problemNaming holds a report to one problem on a file that names what a test expects.
func problemNaming(report control.IntakeReport, path string, names ...string) error {
	if len(report.Problems) != 1 || report.Problems[0].Path != path {
		return Violatedf("problems %+v, want one on %s", report.Problems, path)
	}
	for _, name := range names {
		if !strings.Contains(report.Problems[0].Detail, name) {
			return Violatedf("the problem %q does not name %q", report.Problems[0].Detail, name)
		}
	}
	return nil
}

func checkTraceUnmapped(r *Rig) error {
	s := r.Store()
	source := tracedSource()
	source.Write(traceFile, []byte(served("alpha", documentsPlan, []string{"PAY-12"}, nil)))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	// The contract is recorded, because the definition of ready is what holds its work back.
	if report.Versions != 1 || report.WorkOrders != 1 {
		return Violatedf("report %+v", report)
	}
	if err := problemNaming(report, traceFile, "alpha", "c2", "no requirement"); err != nil {
		return err
	}
	if strings.Contains(report.Problems[0].Detail, "c1") {
		return Violatedf("the problem names the criterion that is served: %q", report.Problems[0].Detail)
	}
	result, err := r.passReview(s, "The work order of alpha")
	if err != nil {
		return err
	}
	if err := heldBackBy(result, workorders.CheckRequirements, "c2"); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

func checkTraceUnknownRequirement(r *Rig) error {
	s := r.Store()
	source := tracedSource()
	source.Write(traceFile, []byte(served("alpha", documentsPlan, []string{"PAY-12", "PAY-99"})))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 {
		return Violatedf("report %+v", report)
	}
	if err := problemNaming(report, traceFile, "alpha", "c1", "PAY-99"); err != nil {
		return err
	}
	result, err := r.passReview(s, "The work order of alpha")
	if err != nil {
		return err
	}
	return heldBackBy(result, workorders.CheckRequirements, "c1", "PAY-99")
}

func checkTraceMapped(r *Rig) error {
	s := r.Store()
	source := tracedSource()
	source.Write(traceFile, []byte(served("alpha", documentsPlan, []string{"PAY-12"}, []string{"PAY-13", "PAY-12"})))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		return Violatedf("report %+v", report)
	}
	result, err := r.passReview(s, "The work order of alpha")
	if err != nil {
		return err
	}
	if result.State != workorders.Ready || len(result.Failures) != 0 {
		return Violatedf("review result %+v", result)
	}
	return r.CheckProjection(s)
}

func checkTraceOnlyThatContract(r *Rig) error {
	s := r.Store()
	source := tracedSource()
	source.Write("a.md", []byte(served("good", documentsPlan, []string{"PAY-12"})))
	source.Write("b.md", []byte(served("bad", documentsPlan, nil)))
	// A contract in a plan with no requirements document serves nothing, and needs to.
	source.Write("other-plan.md", []byte(PlanText("other-plan", "Other", "")))
	source.Write("c.md", []byte(served("free", "other-plan", nil)))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 3 {
		return Violatedf("report %+v", report)
	}
	if err := problemNaming(report, "b.md", "bad", "c1"); err != nil {
		return err
	}
	for title, want := range map[string]workorders.State{
		"The work order of good": workorders.Ready,
		"The work order of free": workorders.Ready,
		"The work order of bad":  workorders.ContractReview,
	} {
		result, err := r.passReview(s, title)
		if err != nil {
			return err
		}
		if result.State != want {
			return Violatedf("%s: review result %+v, want %s", title, result, want)
		}
	}
	return nil
}

func checkTraceDocumentLater(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write(traceFile, []byte(served("alpha", documentsPlan, nil)))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		return Violatedf("a plan with no requirements document: %+v", report)
	}
	// The plan gets a requirements document, and the contract file does not change.
	source.Write("requirements.md", []byte(requirementsOf("payments", documentsPlan, "PAY-12")))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if report.Versions != 0 {
		return Violatedf("report %+v", report)
	}
	if err := problemNaming(report, traceFile, "alpha", "c1"); err != nil {
		return err
	}
	result, err := r.passReview(s, "The work order of alpha")
	if err != nil {
		return err
	}
	if err := heldBackBy(result, workorders.CheckRequirements, "c1"); err != nil {
		return err
	}
	// The next version of the contract serves a requirement, and the same work order passes.
	source.Write(traceFile, []byte(served("alpha", documentsPlan, []string{"PAY-12"})))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		return Violatedf("after the fix: %+v", report)
	}
	if result, err = r.passReview(s, "The work order of alpha"); err != nil {
		return err
	}
	if result.State != workorders.Ready {
		return Violatedf("review result %+v", result)
	}
	return r.CheckProjection(s)
}

func checkTraceRequirementRemoved(r *Rig) error {
	s := r.Store()
	source := tracedSource()
	source.Write(traceFile, []byte(served("alpha", documentsPlan, []string{"PAY-13"})))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	result, err := r.passReview(s, "The work order of alpha")
	if err != nil {
		return err
	}
	if result.State != workorders.Ready {
		return Violatedf("review result %+v", result)
	}
	// PAY-13 leaves the requirements document. The ready work order is not undone, and the next
	// review of the contract finds the criterion unserved.
	source.Write("requirements.md", []byte(requirementsOf("payments", documentsPlan, "PAY-12")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if err := problemNaming(report, traceFile, "alpha", "c1", "PAY-13"); err != nil {
		return err
	}
	order, err := r.orderByTitle(s, "The work order of alpha")
	if err != nil {
		return err
	}
	result, err = r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.Ready {
		return Violatedf("a work order already ready moved: %+v", result)
	}
	return nil
}

func checkTraceProblemRepeats(r *Rig) error {
	s := r.Store()
	source := tracedSource()
	source.Write(traceFile, []byte(served("alpha", documentsPlan, nil)))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	source.Write("notes.md", []byte("# Prose\n"))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if err := problemNaming(report, traceFile, "alpha", "c1"); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("the same problem moved the revision from %d to %d", before, after)
	}
	binding, err := r.binding(s, control.BindingID("fake", "memory"))
	if err != nil {
		return err
	}
	if binding.Health != planning.HealthBadFiles || len(binding.Problems) != 1 || binding.Problems[0].Path != traceFile {
		return Violatedf("binding %+v", binding)
	}
	source.Write(traceFile, []byte(served("alpha", documentsPlan, []string{"PAY-12"})))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	if binding, err = r.binding(s, control.BindingID("fake", "memory")); err != nil {
		return err
	}
	if binding.Health != planning.HealthOK || len(binding.Problems) != 0 {
		return Violatedf("after the fix: binding %+v", binding)
	}
	return nil
}
