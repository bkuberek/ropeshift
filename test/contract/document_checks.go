package contract

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// DocumentChecks hold what intake keeps of the planning documents: a version for each committed change,
// the plan a document belongs to, and the versions a contract cites (FR-DOC-1, FR-DOC-2, FR-DOC-4).
var DocumentChecks = []Check{
	{"DC1", "spec, architecture, experience design, milestone, and requirements files become documents with versions that replay", checkDocumentsRecorded},
	{"DC2", "a committed change is a new version, and every version keeps its text", checkDocumentVersions},
	{"DC3", "a document that names an unknown plan is a problem, and nothing is recorded from it", checkDocumentUnknownPlan},
	{"DC4", "a plan changes by its digest, and each version holds its documents and draft decisions", checkPlanVersions},
	{"DC5", "one document in two files is a problem", checkDocumentInTwoFiles},
	{"DC6", "a contract pins each cited document to the version at its commit, and a new document version moves nothing", checkContractPinsDocuments},
	{"DC7", "a contract that cites an unknown document is a problem, and nothing is recorded from it", checkContractUnknownDocument},
	{"DC8", "a contract keeps its milestone, shared flag, uses, and the requirement of each criterion", checkContractPlacement},
	{"DC9", "a requirements file is a document, and its requirements keep their own versions", checkRequirementsDocument},
	{"DC10", "a document arrives with the plan it names whatever order the files sort in", checkDocumentBatchOrder},
	{"DC11", "the files a planning lane is shown are a valid set: intake reads them with no problem, and their work reaches ready", checkFormatExamplesAreASet},
}

const (
	documentsPlan = "payments-v2"
	specFile      = "spec.md"
)

// planWith renders a plan file that lists documents and drafts decisions.
func planWith(id, title string, documents []string, answer string) string {
	listed := ""
	if len(documents) != 0 {
		listed = `documents = ["` + strings.Join(documents, `", "`) + `"]` + "\n"
	}
	return `+++
kind = "plan"
id = "` + id + `"
title = "` + title + `"
` + listed + `
[[decision]]
id = "d1"
question = "Which provider first?"
answer = "` + answer + `"
rationale = "It is the one with an account."
blocking = true
+++

How.
`
}

// requirementsOf renders a requirements file under a plan.
func requirementsOf(id, plan string, keys ...string) string {
	out := "+++\nkind = \"requirements\"\nid = \"" + id + "\"\ntitle = \"Requirements " + id + "\"\n"
	if plan != "" {
		out += "plan = \"" + plan + "\"\n"
	}
	for _, key := range keys {
		out += "\n[[requirement]]\nid = \"" + key + "\"\ntext = \"The text of " + key + "\"\n"
	}
	return out + "+++\n\nProse about " + id + ".\n"
}

// citing renders a contract that names a plan and cites documents.
func citing(id, plan, statement string, documents ...string) string {
	return citingFor(id, plan, statement, "", documents...)
}

// citingFor renders the same contract with its one criterion serving a requirement.
func citingFor(id, plan, statement, requirement string, documents ...string) string {
	out := "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"The contract " + id + "\"\n"
	if plan != "" {
		out += "plan = \"" + plan + "\"\n"
	}
	if len(documents) != 0 {
		out += "documents = [\"" + strings.Join(documents, `", "`) + "\"]\n"
	}
	serving := ""
	if requirement != "" {
		serving = "requirements = [\"" + requirement + "\"]\n"
	}
	return out + `
[[criteria]]
id = "c1"
text = "It works"
` + serving + `
[[work_orders]]
id = "` + id + `-order"
title = "The work order of ` + id + `"
+++

` + statement + "\n"
}

func (r *Rig) documentRecords(s ports.Store) ([]planning.DocumentRecord, error) {
	var out []planning.DocumentRecord
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Planning().Documents()
		return err
	})
	return out, err
}

func (r *Rig) documentVersionsOf(s ports.Store, id string) ([]planning.DocumentVersion, error) {
	var out []planning.DocumentVersion
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().DocumentVersions()
		for _, v := range all {
			if v.DocumentID == id {
				out = append(out, v)
			}
		}
		return err
	})
	return out, err
}

// planID finds the ledger's ID for the plan a file names.
func (r *Rig) planID(s ports.Store, title string) (string, error) {
	var found string
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		plans, err := tx.Planning().Plans()
		for _, plan := range plans {
			if plan.Title == title {
				found = plan.ID
			}
		}
		return err
	})
	if err == nil && found == "" {
		return "", Violatedf("no plan titled %q", title)
	}
	return found, err
}

func checkDocumentsRecorded(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(planWith(documentsPlan, "Payments v2", nil, "Stripe")))
	source.Write("requirements.md", []byte(requirementsOf("payments", documentsPlan, "PAY-12")))
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", documentsPlan, "The calls.")))
	source.Write("architecture.md", []byte(DocumentText("architecture", "payments", "Payments", documentsPlan, "The services.")))
	source.Write("design.md", []byte(DocumentText("experience-design", "checkout-flow", "Checkout flow", documentsPlan, "The screens.")))
	source.Write("milestone.md", []byte(DocumentText("milestone", "m1", "First", documentsPlan, "Why first.")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	// The plan, the requirements document and its requirement, and four more documents.
	if report.Records != 7 || len(report.Problems) != 0 {
		return Violatedf("report %+v", report)
	}
	plan, err := r.planID(s, "Payments v2")
	if err != nil {
		return err
	}
	documents, err := r.documentRecords(s)
	if err != nil {
		return err
	}
	var refs []string
	for _, d := range documents {
		refs = append(refs, d.ID)
		if d.PlanID != plan || d.Title == "" || d.Kind == "" {
			return Violatedf("document %+v, want plan %s", d, plan)
		}
	}
	want := []string{"architecture:payments", "experience-design:checkout-flow", "milestone:m1", "requirements:payments", "spec:checkout-api"}
	if !slices.Equal(refs, want) {
		return Violatedf("documents %v, want %v", refs, want)
	}
	versions, err := r.documentVersionsOf(s, "spec:checkout-api")
	if err != nil {
		return err
	}
	if len(versions) != 1 || versions[0].Version != 1 || !strings.HasPrefix(versions[0].Digest, planning.DigestSHA256) ||
		versions[0].Body != "The calls.\n" || versions[0].Source == "" || versions[0].Title != "Checkout API" {
		return Violatedf("versions %+v", versions)
	}
	var spec planning.SpecFile
	if err := versions[0].Decode(&spec); err != nil || spec.ID != "checkout-api" || spec.Plan != documentsPlan {
		return Violatedf("fields %s decode to %+v: %v", versions[0].Fields, spec, err)
	}
	var inPlan []string
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		held, err := tx.Planning().PlanDocuments(plan)
		for _, d := range held {
			inPlan = append(inPlan, d.ID)
		}
		return err
	})
	if err != nil || len(inPlan) != 5 {
		return Violatedf("the plan holds %v: %v", inPlan, err)
	}
	return r.CheckPlanning(s)
}

func checkDocumentVersions(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", "", "The calls.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	source.Write("notes.md", []byte("# Prose\n"))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a change elsewhere moved the revision from %d to %d", before, after)
	}
	// The prose is the document, so a change to it alone is a new version.
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", "", "The calls, and how each fails.")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Records != 1 {
		return Violatedf("report %+v", report)
	}
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API v2", "", "The calls, and how each fails.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	versions, err := r.documentVersionsOf(s, "spec:checkout-api")
	if err != nil {
		return err
	}
	if len(versions) != 3 {
		return Violatedf("%d versions, want 3: %+v", len(versions), versions)
	}
	for i, v := range versions {
		if v.Version != int64(i+1) || (i > 0 && v.Digest == versions[i-1].Digest) {
			return Violatedf("version %d is %+v after %+v", i+1, v, versions[:i])
		}
	}
	if versions[0].Body != "The calls.\n" || versions[1].Body != "The calls, and how each fails.\n" || versions[1].Title != "Checkout API" || versions[2].Title != "Checkout API v2" {
		return Violatedf("a version lost its text: %+v", versions)
	}
	documents, err := r.documentRecords(s)
	if err != nil {
		return err
	}
	if len(documents) != 1 || documents[0].Title != "Checkout API v2" {
		return Violatedf("documents %+v", documents)
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkDocumentUnknownPlan(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", "later-plan", "The calls.")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Records != 0 || len(report.Problems) != 1 || report.Problems[0].Path != specFile || !strings.Contains(report.Problems[0].Detail, "later-plan") {
		return Violatedf("a spec on an unknown plan: %+v", report)
	}
	documents, err := r.documentRecords(s)
	if err != nil || len(documents) != 0 {
		return Violatedf("documents %+v: %v", documents, err)
	}
	source.Write("plan.md", []byte(PlanText("later-plan", "The plan", "")))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if report.Records != 2 || len(report.Problems) != 0 {
		return Violatedf("after the plan arrived: %+v", report)
	}
	return r.CheckPlanning(s)
}

func checkPlanVersions(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	listed := []string{"requirements:payments", "spec:checkout-api"}
	source.Write("plan.md", []byte(planWith(documentsPlan, "Payments v2", listed, "Stripe")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a poll with no change moved the revision from %d to %d", before, after)
	}
	// Only a draft decision changes: the title and the goal stay, and the plan still has a new version.
	source.Write("plan.md", []byte(planWith(documentsPlan, "Payments v2", listed, "Adyen")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Records != 1 {
		return Violatedf("report %+v", report)
	}
	plan, err := r.planID(s, "Payments v2")
	if err != nil {
		return err
	}
	var versions []planning.PlanVersion
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().PlanVersions()
		versions = all
		return err
	})
	if err != nil {
		return err
	}
	if len(versions) != 2 || versions[0].PlanID != plan || versions[0].Version != 1 || versions[1].Version != 2 || versions[0].Digest == versions[1].Digest {
		return Violatedf("versions %+v", versions)
	}
	if !slices.Equal(versions[0].Documents, listed) || len(versions[0].Decisions) != 1 || versions[0].Decisions[0].Answer != "Stripe" ||
		versions[1].Decisions[0].Answer != "Adyen" || !versions[1].Decisions[0].Blocking || versions[1].Decisions[0].Rationale == "" {
		return Violatedf("versions hold %+v", versions)
	}
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		latest, err := tx.Planning().LatestPlanVersion(plan)
		if err != nil || latest.Version != 2 {
			return Violatedf("the newest plan version is %+v: %v", latest, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// A draft decision is plan data and nothing else: it is no decision of the ledger yet.
	var decisions int
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Questions().Decisions(ports.RecordFilter{})
		decisions = len(all)
		return err
	})
	if err != nil || decisions != 0 {
		return Violatedf("%d decisions after a plan with a draft: %v", decisions, err)
	}
	return r.CheckPlanning(s)
}

func checkDocumentInTwoFiles(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("a.md", []byte(DocumentText("spec", "checkout-api", "First", "", "One.")))
	source.Write("b.md", []byte(DocumentText("spec", "checkout-api", "Second", "", "Two.")))
	// The same ID under another kind is another document.
	source.Write("c.md", []byte(DocumentText("architecture", "checkout-api", "Third", "", "Three.")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 1 || report.Problems[0].Path != "b.md" || report.Records != 2 {
		return Violatedf("one document in two files: %+v", report)
	}
	versions, err := r.documentVersionsOf(s, "spec:checkout-api")
	if err != nil || len(versions) != 1 || versions[0].Title != "First" {
		return Violatedf("versions %+v: %v", versions, err)
	}
	return nil
}

// contractVersion reads one version of the contract whose title names an ID.
func (r *Rig) contractVersion(s ports.Store, id string, version int64) (planning.Contract, error) {
	contracts, err := r.readContracts(s)
	if err != nil {
		return planning.Contract{}, err
	}
	for _, c := range contracts {
		if c.Version == version && strings.Contains(c.Title, id) {
			return c, nil
		}
	}
	return planning.Contract{}, Violatedf("no version %d of contract %s in %+v", version, id, contracts)
}

func checkContractPinsDocuments(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", documentsPlan, "The calls.")))
	source.Write("architecture.md", []byte(DocumentText("architecture", "payments", "Payments", documentsPlan, "The services.")))
	source.Write(intakeFile, []byte(citing("alpha", documentsPlan, "One.", "architecture:payments", "spec:checkout-api")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		return Violatedf("report %+v", report)
	}
	first, err := r.contractVersion(s, "alpha", 1)
	if err != nil {
		return err
	}
	want := []planning.DocumentPin{{Document: "architecture:payments", Version: 1}, {Document: "spec:checkout-api", Version: 1}}
	if !slices.Equal(first.Documents, want) {
		return Violatedf("pins %+v, want %+v (in the order the file lists them)", first.Documents, want)
	}
	// The spec moves on. The contract file did not change, so the contract has no new version and its
	// pins stay on what it read.
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", documentsPlan, "The calls, changed.")))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if report.Versions != 0 || report.Records != 1 {
		return Violatedf("a new document version: %+v", report)
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	if len(contracts) != 1 || !slices.Equal(contracts[0].Documents, want) {
		return Violatedf("contracts %+v moved with the document", contracts)
	}
	order, err := r.orderByTitle(s, "The work order of alpha")
	if err != nil || order.ContractVersion != 1 {
		return Violatedf("work order %+v: %v", order, err)
	}
	// A new version of the contract reads the documents as they stand then.
	source.Write(intakeFile, []byte(citing("alpha", documentsPlan, "Two.", "architecture:payments", "spec:checkout-api")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	second, err := r.contractVersion(s, "alpha", 2)
	if err != nil {
		return err
	}
	want = []planning.DocumentPin{{Document: "architecture:payments", Version: 1}, {Document: "spec:checkout-api", Version: 2}}
	if !slices.Equal(second.Documents, want) {
		return Violatedf("version 2 pins %+v, want %+v", second.Documents, want)
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkContractUnknownDocument(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(intakeFile, []byte(citing("alpha", "", "One.", "spec:checkout-api")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 0 || len(report.Problems) != 1 || report.Problems[0].Path != intakeFile || !strings.Contains(report.Problems[0].Detail, "spec:checkout-api") {
		return Violatedf("a contract citing an unknown document: %+v", report)
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	orders, err := r.readOrders(s)
	if err != nil {
		return err
	}
	if len(contracts) != 0 || len(orders) != 0 {
		return Violatedf("%d contracts and %d work orders from a file with an unresolved citation", len(contracts), len(orders))
	}
	source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", "", "The calls.")))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		return Violatedf("after the document arrived: %+v", report)
	}
	first, err := r.contractVersion(s, "alpha", 1)
	if err != nil {
		return err
	}
	if !slices.Equal(first.Documents, []planning.DocumentPin{{Document: "spec:checkout-api", Version: 1}}) {
		return Violatedf("pins %+v", first.Documents)
	}
	return nil
}

func checkContractPlacement(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write("milestone.md", []byte(milestoneOf("m1", documentsPlan, 1)))
	source.Write("payments-client.md", []byte(placedContract{ID: "payments-client", Plan: documentsPlan, Milestone: "m1", Shared: true}.text()))
	source.Write("ledger-client.md", []byte(placedContract{ID: "ledger-client", Plan: documentsPlan, Milestone: "m1", Shared: true}.text()))
	source.Write(intakeFile, []byte(`+++
kind = "contract"
id = "alpha"
title = "The contract alpha"
plan = "payments-v2"
milestone = "m1"
shared = true
uses = ["payments-client", "ledger-client"]
requirements = ["PAY-1"]

[[criteria]]
id = "c1"
text = "It works"
requirements = ["PAY-12", "PAY-13"]

[[criteria]]
id = "c2"
text = "It is quick"

[[work_orders]]
id = "alpha-order"
title = "The work order of alpha"
+++
`))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	contract, err := r.contractVersion(s, "alpha", 1)
	if err != nil {
		return err
	}
	if contract.Milestone != "m1" || !contract.Shared || !slices.Equal(contract.Uses, []string{"payments-client", "ledger-client"}) {
		return Violatedf("contract %+v", contract)
	}
	if len(contract.Criteria) != 2 || !slices.Equal(contract.Criteria[0].Requirements, []string{"PAY-12", "PAY-13"}) || len(contract.Criteria[1].Requirements) != 0 {
		return Violatedf("criteria %+v", contract.Criteria)
	}
	// A pack reads the keys at the contract's level together with the criteria's, each once.
	if keys := contract.RequirementKeys(); !slices.Equal(keys, []string{"PAY-1", "PAY-12", "PAY-13"}) {
		return Violatedf("requirement keys %v", keys)
	}
	return r.CheckPlanning(s)
}

func checkRequirementsDocument(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write("requirements.md", []byte(requirementsOf("payments", documentsPlan, "PAY-12", "PAY-13")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	// Only the prose changes: the document has a new version, and no requirement does.
	changed := strings.Replace(requirementsOf("payments", documentsPlan, "PAY-12", "PAY-13"), "Prose about", "More prose about", 1)
	source.Write("requirements.md", []byte(changed))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	versions, err := r.documentVersionsOf(s, "requirements:payments")
	if err != nil {
		return err
	}
	if len(versions) != 2 || !strings.HasPrefix(versions[1].Body, "More prose") {
		return Violatedf("versions %+v", versions)
	}
	var requirements []planning.Requirement
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		requirements, err = tx.Planning().Requirements()
		return err
	})
	if err != nil || len(requirements) != 2 {
		return Violatedf("requirements %+v: %v", requirements, err)
	}
	// One requirement changes: it has a version of its own, and the document has another.
	source.Write("requirements.md", []byte(strings.Replace(changed, "The text of PAY-13", "Other text", 1)))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	if versions, err = r.documentVersionsOf(s, "requirements:payments"); err != nil || len(versions) != 3 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	var file planning.RequirementsFile
	if err := versions[2].Decode(&file); err != nil || len(file.Requirements) != 2 || file.Requirements[1].Text != "Other text" || file.Plan != documentsPlan {
		return Violatedf("the newest version holds %+v: %v", file, err)
	}
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().Requirements()
		if err != nil || len(all) != 3 {
			return Violatedf("requirement versions %+v: %v", all, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkDocumentBatchOrder(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	// Sorted by name, the milestone and the spec come before the plan and the requirements they name.
	source.Write("a-milestone.md", []byte(DocumentText("milestone", "m1", "First", documentsPlan, "Why first.")))
	source.Write("b-spec.md", []byte(DocumentText("spec", "checkout-api", "Checkout API", documentsPlan, "The calls.")))
	source.Write("c-plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write("d-requirements.md", []byte(requirementsOf("payments", documentsPlan, "PAY-12")))
	source.Write("e-contract.md", []byte(citingFor("alpha", documentsPlan, "One.", "PAY-12", "spec:checkout-api", "milestone:m1", "requirements:payments")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.Records != 5 || report.Versions != 1 {
		return Violatedf("report %+v", report)
	}
	return r.CheckPlanning(s)
}

// checkDocumentsReplay compares the document and plan tables with what the log replays to.
func checkDocumentsReplay(planned ports.PlanningReader, book planning.Book) error {
	documents, err := planned.Documents()
	if err != nil {
		return err
	}
	if len(documents) != len(book.Documents) {
		return Violatedf("%d documents, the log replays to %d", len(documents), len(book.Documents))
	}
	for _, d := range documents {
		if want, ok := book.Documents[d.ID]; !ok || want != d {
			return Violatedf("document %s disagrees with its log", d.ID)
		}
	}
	versions, err := planned.DocumentVersions()
	if err != nil {
		return err
	}
	if len(versions) != len(book.DocumentVersions) {
		return Violatedf("%d document versions, the log replays to %d", len(versions), len(book.DocumentVersions))
	}
	for _, v := range versions {
		want, ok := book.DocumentVersions[planning.ContractKey(v.DocumentID, v.Version)]
		if !ok || want.Digest != v.Digest || want.Source != v.Source || want.Body != v.Body || want.Title != v.Title || string(want.Fields) != string(v.Fields) {
			return Violatedf("document %s version %d disagrees with its log", v.DocumentID, v.Version)
		}
	}
	planVersions, err := planned.PlanVersions()
	if err != nil {
		return err
	}
	if len(planVersions) != len(book.PlanVersions) {
		return Violatedf("%d plan versions, the log replays to %d", len(planVersions), len(book.PlanVersions))
	}
	for _, v := range planVersions {
		want, ok := book.PlanVersions[planning.ContractKey(v.PlanID, v.Version)]
		if !ok || want.Digest != v.Digest || want.Source != v.Source || !slices.Equal(want.Documents, v.Documents) ||
			!reflect.DeepEqual(append([]planning.DraftDecision(nil), want.Decisions...), append([]planning.DraftDecision(nil), v.Decisions...)) {
			return Violatedf("plan %s version %d disagrees with its log", v.PlanID, v.Version)
		}
	}
	return nil
}

func checkFormatExamplesAreASet(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	for i, example := range planning.FormatExamples {
		source.Write(fmt.Sprintf("%02d-%s.md", i, example.Kind), []byte(example.Text))
	}
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.Versions != 2 || report.WorkOrders != 2 || report.Conflicts != 0 {
		return Violatedf("report %+v", report)
	}
	// The shared contract is built first: the work that uses it waits until its work is done.
	client, err := r.reviewTitled(s, "Build the payments client")
	if err != nil {
		return err
	}
	if client.State != workorders.Ready || len(client.Failures) != 0 {
		return Violatedf("the shared work did not reach ready: %+v", client)
	}
	waiting, err := r.reviewTitled(s, "Add the retry budget")
	if err != nil {
		return err
	}
	if waiting.State != workorders.ContractReview || len(waiting.Failures) != 1 || waiting.Failures[0].Check != workorders.CheckDependencies {
		return Violatedf("the work that uses it did not wait: %+v", waiting)
	}
	base, err := r.orderByTitle(s, "Build the payments client")
	if err != nil {
		return err
	}
	if err := r.forceDone(s, base.ID); err != nil {
		return err
	}
	ready, err := r.reviewTitled(s, "Add the retry budget")
	if err != nil {
		return err
	}
	if ready.State != workorders.Ready || len(ready.Failures) != 0 {
		return Violatedf("the work the examples describe did not reach ready: %+v", ready)
	}
	contracts, err := r.readContracts(s)
	if err != nil || len(contracts) != 2 {
		return Violatedf("contracts %+v: %v", contracts, err)
	}
	// Every citation of the contract found the document it names, and it sits where it says.
	i := slices.IndexFunc(contracts, func(c planning.Contract) bool { return c.Title == "Retry failed checkout calls" })
	if i < 0 {
		return Violatedf("no contract checkout-retry in %+v", contracts)
	}
	retry := contracts[i]
	if len(retry.Documents) != 2 || retry.Milestone != "m1-reliable-checkout" || !slices.Equal(retry.Uses, []string{"payments-client"}) {
		return Violatedf("contract %+v", retry)
	}
	return r.CheckPlanning(s)
}
