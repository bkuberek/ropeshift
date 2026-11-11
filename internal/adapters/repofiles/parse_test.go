package repofiles

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// What a planning lane is shown is what intake reads, so an agent that copies an example writes a
// file of its kind.
func TestEveryPlanningExampleIsAFile(t *testing.T) {
	kinds := map[planning.FileKind]bool{}
	for _, example := range planning.FormatExamples {
		doc, err := ParseFile([]byte(example.Text))
		if err != nil {
			t.Fatalf("the %s example: %v", example.Kind, err)
		}
		if doc.Kind != example.Kind {
			t.Errorf("the %s example reads as a %s", example.Kind, doc.Kind)
		}
		if strings.TrimSpace(doc.Statement) == "" {
			t.Errorf("the %s example has no prose", example.Kind)
		}
		if example.Intro == "" {
			t.Errorf("the %s example says nothing of itself", example.Kind)
		}
		kinds[example.Kind] = true
	}
	for _, kind := range []planning.FileKind{
		planning.FileGoal, planning.FilePlan, planning.FileRequirements, planning.FileSpec, planning.FileArchitecture,
		planning.FileExperienceDesign, planning.FileMilestone, planning.FileContract,
	} {
		if !kinds[kind] {
			t.Errorf("no example shows a %s file (FR-SESSION-5)", kind)
		}
	}
}

// The examples describe one project, so a planning lane that follows them writes files that name each
// other: a plan lists the documents that name it, and a contract cites what its plan holds.
func TestThePlanningExamplesNameEachOther(t *testing.T) {
	docs := map[planning.FileKind]planning.Document{}
	for _, example := range planning.FormatExamples {
		doc, err := ParseFile([]byte(example.Text))
		if err != nil {
			t.Fatal(err)
		}
		docs[example.Kind] = doc
	}
	plan := docs[planning.FilePlan].Plan
	if plan == nil || plan.Goal != docs[planning.FileGoal].Goal.ID || len(plan.Decisions) == 0 {
		t.Fatalf("plan %+v", plan)
	}
	known := map[string]bool{}
	for _, key := range docs[planning.FileRequirements].Requirements.Requirements {
		known[key.ID] = true
	}
	held := map[string]bool{}
	for kind, doc := range docs {
		head, ok := doc.Head()
		if !ok {
			continue
		}
		held[head.Ref()] = true
		if head.Plan != plan.ID {
			t.Errorf("the %s example names plan %q, not %q", kind, head.Plan, plan.ID)
		}
	}
	for _, ref := range plan.Documents {
		if !held[ref] {
			t.Errorf("the plan lists %s, which no example shows", ref)
		}
	}
	if len(plan.Documents) != len(held) {
		t.Errorf("the plan lists %v, and the examples show %d documents", plan.Documents, len(held))
	}
	for _, requirements := range [][]string{docs[planning.FileSpec].Spec.Requirements, docs[planning.FileArchitecture].Architecture.Requirements, docs[planning.FileExperienceDesign].Experience.Requirements} {
		for _, key := range requirements {
			if !known[key] {
				t.Errorf("a document addresses %s, which the requirements example lacks", key)
			}
		}
	}
	contract := docs[planning.FileContract].Contract
	if contract.Plan != plan.ID || contract.Milestone != docs[planning.FileMilestone].Milestone.ID || len(contract.Uses) == 0 || len(contract.WorkOrders) == 0 {
		t.Errorf("contract %+v", contract)
	}
	for _, ref := range contract.Documents {
		if !slices.Contains(plan.Documents, ref) {
			t.Errorf("the contract cites %s, which its plan does not list", ref)
		}
	}
	for _, criterion := range contract.Criteria {
		if len(criterion.Requirements) == 0 {
			t.Errorf("criterion %s serves no requirement", criterion.ID)
		}
		for _, key := range criterion.Requirements {
			if !known[key] {
				t.Errorf("criterion %s serves %s, which the requirements example lacks", criterion.ID, key)
			}
		}
	}
}

func TestParseFile(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr error
		check   func(*testing.T, planning.Document)
	}{
		{
			name:    "a contract in the D1 format",
			fixture: "valid.md",
			check: func(t *testing.T, doc planning.Document) {
				c := doc.Contract
				if doc.Kind != planning.FileContract || c == nil {
					t.Fatalf("got %+v", doc)
				}
				if c.ID != "checkout-retry" || c.Title != "Retry failed checkout calls" {
					t.Errorf("header %+v", c)
				}
				if len(c.Requirements) != 1 || c.Requirements[0] != "PAY-12" {
					t.Errorf("requirements %v", c.Requirements)
				}
				if len(c.Criteria) != 1 || c.Criteria[0].ID != "c1" {
					t.Errorf("criteria %v", c.Criteria)
				}
				if len(c.WorkOrders) != 1 {
					t.Fatalf("work orders %v", c.WorkOrders)
				}
				order := c.WorkOrders[0]
				if order.ID != "retry-budget" || order.Repo != "." || order.Priority != 2 || order.Size != "medium" {
					t.Errorf("work order %+v", order)
				}
				if order.Needs["driver"] != "claude-code" {
					t.Errorf("needs %v", order.Needs)
				}
				if doc.Statement != "The statement of what this work must deliver.\n" {
					t.Errorf("statement %q", doc.Statement)
				}
			},
		},
		{
			name:    "no criteria parses, and the definition of ready refuses it",
			fixture: "no-criteria.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Contract == nil || len(doc.Contract.Criteria) != 0 {
					t.Fatalf("got %+v", doc)
				}
				if got := doc.Contract.WorkOrders[0].Size; got != planning.SizeUnknown {
					t.Errorf("an absent size reads %q, want %q (D27)", got, planning.SizeUnknown)
				}
				if got := doc.Contract.WorkOrders[0].Repo; got != "." {
					t.Errorf("an absent repo reads %q", got)
				}
			},
		},
		{
			name:    "a goal",
			fixture: "goal.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FileGoal || doc.Goal == nil {
					t.Fatalf("got %+v", doc)
				}
				if doc.Goal.ID != "reliable-checkout" || len(doc.Goal.SuccessMeasures) != 1 {
					t.Errorf("goal %+v", doc.Goal)
				}
				if doc.Statement != "Why this matters.\n" {
					t.Errorf("statement %q", doc.Statement)
				}
			},
		},
		{
			name:    "a plan names its goal",
			fixture: "plan.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FilePlan || doc.Plan == nil {
					t.Fatalf("got %+v", doc)
				}
				if doc.Plan.ID != "checkout-plan" || doc.Plan.Goal != "reliable-checkout" {
					t.Errorf("plan %+v", doc.Plan)
				}
			},
		},
		{
			name:    "requirements come from tables, and the body stays prose",
			fixture: "requirements.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FileRequirements || doc.Requirements == nil {
					t.Fatalf("got %+v", doc)
				}
				tables := doc.Requirements.Requirements
				if len(tables) != 2 || tables[0].ID != "PAY-12" || tables[1].Text != "A retry budget bounds every call" {
					t.Errorf("requirements %+v", tables)
				}
			},
		},
		{
			name:    "a requirements file names its plan",
			fixture: "requirements-planned.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Requirements == nil || doc.Requirements.Plan != "payments-v2" {
					t.Fatalf("got %+v", doc.Requirements)
				}
			},
		},
		{
			name:    "a plan lists its documents and holds its draft decisions",
			fixture: "plan-full.md",
			check: func(t *testing.T, doc planning.Document) {
				plan := doc.Plan
				if plan == nil || plan.Goal != "reliable-checkout" || len(plan.Documents) != 5 || plan.Documents[1] != "spec:checkout-api" {
					t.Fatalf("plan %+v", plan)
				}
				if len(plan.Decisions) != 2 {
					t.Fatalf("decisions %+v", plan.Decisions)
				}
				first := plan.Decisions[0]
				if first.ID != "d1" || !first.Blocking || first.Rationale == "" || first.Answer == "" || first.Question == "" {
					t.Errorf("first decision %+v", first)
				}
				if plan.Decisions[1].Blocking || plan.Decisions[1].Rationale != "" {
					t.Errorf("second decision %+v", plan.Decisions[1])
				}
			},
		},
		{
			name:    "a spec addresses requirements",
			fixture: "spec.md",
			check: func(t *testing.T, doc planning.Document) {
				spec := doc.Spec
				if doc.Kind != planning.FileSpec || spec == nil || spec.ID != "checkout-api" || spec.Plan != "payments-v2" {
					t.Fatalf("got %+v", doc)
				}
				if len(spec.Requirements) != 2 || spec.Requirements[1] != "PAY-13" {
					t.Errorf("requirements %v", spec.Requirements)
				}
				if !strings.Contains(doc.Statement, "checkout service") {
					t.Errorf("statement %q", doc.Statement)
				}
			},
		},
		{
			name:    "an architecture document addresses requirements",
			fixture: "architecture.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FileArchitecture || doc.Architecture == nil || doc.Architecture.ID != "payments" || len(doc.Architecture.Requirements) != 1 {
					t.Fatalf("got %+v", doc)
				}
			},
		},
		{
			name:    "an experience design also lists its assets",
			fixture: "experience-design.md",
			check: func(t *testing.T, doc planning.Document) {
				design := doc.Experience
				if doc.Kind != planning.FileExperienceDesign || design == nil || design.ID != "checkout-flow" {
					t.Fatalf("got %+v", doc)
				}
				if len(design.Assets) != 2 || design.Assets[0] != "docs/design/checkout-flow.png" || len(design.Requirements) != 1 {
					t.Errorf("design %+v", design)
				}
			},
		},
		{
			name:    "a milestone holds a priority, dependencies, and exit criteria",
			fixture: "milestone.md",
			check: func(t *testing.T, doc planning.Document) {
				m := doc.Milestone
				if doc.Kind != planning.FileMilestone || m == nil || m.ID != "m1-reliable-checkout" || m.Priority != 1 || m.Plan != "payments-v2" {
					t.Fatalf("got %+v", doc)
				}
				if len(m.DependsOn) != 1 || m.DependsOn[0] != "m0-payments-client" {
					t.Errorf("depends_on %v", m.DependsOn)
				}
				if len(m.ExitCriteria) != 1 || m.ExitCriteria[0].ID != "e1" {
					t.Errorf("exit criteria %+v", m.ExitCriteria)
				}
			},
		},
		{
			name:    "a contract names its milestone, shared contracts, documents, and the requirement each criterion serves",
			fixture: "contract-full.md",
			check: func(t *testing.T, doc planning.Document) {
				c := doc.Contract
				if c == nil || c.Plan != "payments-v2" || c.Milestone != "m1-reliable-checkout" || !c.Shared {
					t.Fatalf("contract %+v", c)
				}
				if len(c.Uses) != 1 || c.Uses[0] != "payments-client" || len(c.Documents) != 2 || c.Documents[1] != "architecture:payments" {
					t.Errorf("uses %v, documents %v", c.Uses, c.Documents)
				}
				if len(c.Criteria) != 2 || len(c.Criteria[0].Requirements) != 1 || c.Criteria[1].Requirements[0] != "PAY-13" {
					t.Errorf("criteria %+v", c.Criteria)
				}
			},
		},
		{
			name:    "a work order may name the branch it starts from",
			fixture: "contract-base.md",
			check: func(t *testing.T, doc planning.Document) {
				if c := doc.Contract; c == nil || len(c.WorkOrders) != 1 || c.WorkOrders[0].Base != "release/1.2" {
					t.Fatalf("contract %+v", c)
				}
			},
		},
		{
			name:    "a work order that names no base follows its repository's",
			fixture: "valid.md",
			check: func(t *testing.T, doc planning.Document) {
				if base := doc.Contract.WorkOrders[0].Base; base != "" {
					t.Errorf("base %q", base)
				}
			},
		},
		{
			name:    "a contract written for phase 1 keeps its contract-level requirements",
			fixture: "valid.md",
			check: func(t *testing.T, doc planning.Document) {
				if c := doc.Contract; c.Milestone != "" || c.Shared || len(c.Documents) != 0 || len(c.Criteria[0].Requirements) != 0 {
					t.Errorf("contract %+v", c)
				}
			},
		},
		{
			name:    "an existing shared contract has criteria and no work orders",
			fixture: "contract-existing.md",
			check: func(t *testing.T, doc planning.Document) {
				if c := doc.Contract; c == nil || !c.Existing || !c.Shared || len(c.WorkOrders) != 0 || len(c.Criteria) != 1 {
					t.Errorf("contract %+v", doc.Contract)
				}
			},
		},
		{
			name:    "a work order continues an in-flight branch",
			fixture: "contract-continues.md",
			check: func(t *testing.T, doc planning.Document) {
				if c := doc.Contract; c == nil || c.WorkOrders[0].Continues != "feature/retry" || c.WorkOrders[0].Base != "" {
					t.Errorf("contract %+v", doc.Contract)
				}
			},
		},
		{
			name:    "a requirement the code meets cites its evidence",
			fixture: "requirements-met.md",
			check: func(t *testing.T, doc planning.Document) {
				r := doc.Requirements
				if r == nil || len(r.Requirements) != 2 {
					t.Fatalf("requirements %+v", r)
				}
				met, open := r.Requirements[0], r.Requirements[1]
				if !met.Met() || len(met.Evidence) != 2 || met.Evidence[0] != "api:internal/client.go" || open.Met() || open.Status != planning.RequirementOpen {
					t.Errorf("requirements %+v", r.Requirements)
				}
			},
		},
		{
			name:    "a milestone the code delivers",
			fixture: "milestone-existing.md",
			check: func(t *testing.T, doc planning.Document) {
				if m := doc.Milestone; m == nil || !m.Existing {
					t.Errorf("milestone %+v", doc.Milestone)
				}
				head, _ := doc.Head()
				if !strings.Contains(string(head.Fields), `"existing":true`) {
					t.Errorf("the ledger's fields %s", head.Fields)
				}
			},
		},
		{
			name:    "a plan records what it left out",
			fixture: "plan-left-out.md",
			check: func(t *testing.T, doc planning.Document) {
				if p := doc.Plan; p == nil || len(p.LeftOut) != 1 || p.LeftOut[0].Ref != "branch:spike/old-ui" || p.LeftOut[0].Reason != "An abandoned experiment" {
					t.Errorf("plan %+v", doc.Plan)
				}
			},
		},
		{name: "an existing contract with work orders", fixture: "contract-existing-orders.md", wantErr: planning.ErrBadFile},
		{name: "a met requirement with no evidence", fixture: "requirements-met-no-evidence.md", wantErr: planning.ErrBadFile},
		{name: "something left out with no reason", fixture: "plan-left-out-no-reason.md", wantErr: planning.ErrBadFile},
		{name: "one requirement twice", fixture: "repeated-requirement.md", wantErr: planning.ErrBadFile},
		{name: "a citation with no kind", fixture: "bad-document-ref.md", wantErr: planning.ErrBadFile},
		{name: "a citation of a kind that is no document", fixture: "unknown-document-kind.md", wantErr: planning.ErrBadFile},
		{name: "a milestone that depends on itself", fixture: "milestone-self-dependency.md", wantErr: planning.ErrBadFile},
		{name: "a milestone with one exit criterion twice", fixture: "repeated-exit-criterion.md", wantErr: planning.ErrBadFile},
		{name: "a spec with no title", fixture: "spec-untitled.md", wantErr: planning.ErrBadFile},
		{name: "an asset outside the repository", fixture: "bad-asset.md", wantErr: planning.ErrBadFile},
		{name: "a draft decision with no answer", fixture: "draft-decision-no-answer.md", wantErr: planning.ErrBadFile},
		{name: "a criterion mapped to an empty requirement", fixture: "empty-criterion-mapping.md", wantErr: planning.ErrBadFile},
		{name: "a goal with no title", fixture: "untitled-goal.md", wantErr: planning.ErrBadFile},
		{name: "an unknown kind", fixture: "unknown-kind.md", wantErr: planning.ErrUnknownKind},
		{name: "bad TOML", fixture: "bad-toml.md", wantErr: planning.ErrBadFile},
		{name: "a front matter with no end", fixture: "unclosed.md", wantErr: planning.ErrBadFile},
		{name: "a size the format does not name", fixture: "bad-size.md", wantErr: planning.ErrBadFile},
		{name: "a base git would take as an option", fixture: "bad-base.md", wantErr: planning.ErrBadFile},
		{name: "one work order twice", fixture: "repeated-order.md", wantErr: planning.ErrBadFile},
		{name: "a plain document", fixture: "no-front-matter.md", wantErr: planning.ErrNotOurs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseFile(readFixture(t, tt.fixture))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("got %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, doc)
		})
	}
}

// A brief that onboards existing code shows these beside the formats, so each one is a file intake reads,
// and together they show every field the format has for existing work (ADR-0023).
func TestEveryAdoptionExampleIsAFile(t *testing.T) {
	var met, existingMilestone, existingContract, continues, leftOut bool
	for _, example := range planning.AdoptionExamples {
		doc, err := ParseFile([]byte(example.Text))
		if err != nil {
			t.Fatalf("the %s example: %v", example.Kind, err)
		}
		if doc.Kind != example.Kind || example.Intro == "" || strings.TrimSpace(doc.Statement) == "" {
			t.Errorf("the %s example reads as %s, says %q of itself, with prose %q", example.Kind, doc.Kind, example.Intro, doc.Statement)
		}
		switch {
		case doc.Requirements != nil:
			met = slices.ContainsFunc(doc.Requirements.Requirements, func(r planning.RequirementSpec) bool { return r.Met() && len(r.Evidence) > 0 })
		case doc.Milestone != nil:
			existingMilestone = doc.Milestone.Existing
		case doc.Contract != nil && doc.Contract.Existing:
			existingContract = doc.Contract.Shared && len(doc.Contract.WorkOrders) == 0
		case doc.Contract != nil:
			continues = slices.ContainsFunc(doc.Contract.WorkOrders, func(w planning.WorkOrderSpec) bool { return w.Continues != "" })
		case doc.Plan != nil:
			leftOut = len(doc.Plan.LeftOut) > 0
		}
	}
	if !met || !existingMilestone || !existingContract || !continues || !leftOut {
		t.Errorf("met %v, existing milestone %v, existing contract %v, continues %v, left out %v", met, existingMilestone, existingContract, continues, leftOut)
	}
}

// The document an adopt brief shows onboarded is a requirements file intake reads, under the IDs the document
// already gave, with every line of the document as it was below the front matter.
func TestTheOnboardedExampleKeepsTheDocument(t *testing.T) {
	example := planning.OnboardedExample
	if _, err := ParseFile([]byte(example.Before)); !errors.Is(err, planning.ErrNotOurs) {
		t.Errorf("the document before onboarding reads as ours: %v", err)
	}
	doc, err := ParseFile([]byte(example.After))
	if err != nil || doc.Requirements == nil {
		t.Fatalf("the onboarded document: %+v: %v", doc, err)
	}
	var ids []string
	for _, requirement := range doc.Requirements.Requirements {
		ids = append(ids, requirement.ID)
		if !strings.Contains(example.Before, requirement.ID) {
			t.Errorf("requirement %s is not an ID the document gave", requirement.ID)
		}
	}
	if !slices.Equal(ids, []string{"PAY-11", "PAY-12"}) || !doc.Requirements.Requirements[0].Met() {
		t.Errorf("the onboarded requirements are %+v", doc.Requirements.Requirements)
	}
	if !strings.HasSuffix(example.After, "+++\n\n"+example.Before) || strings.TrimSpace(doc.Statement) != strings.TrimSpace(example.Before) {
		t.Errorf("onboarding changed the document's prose:\n%s", doc.Statement)
	}
}
