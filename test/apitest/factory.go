package apitest

import (
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The names a factory world's planned project uses, so a test can find what it reads.
const (
	FactoryPlanTitle = "The factory floor"
	FactorySpecPath  = "plans/factory/spec-api.md"
	// FactoryReview is work recorded with no plan, whose contract waits for a member's review.
	FactoryReview = "wo-review"
)

const factoryGoal = `+++
kind = "goal"
id = "factory"
title = "Run the factory"
success_measures = ["A plan is approved in one step", "Shared work is built first"]
+++

One person plans, and the factory builds.
`

// factoryPlan writes the plan's file. The first versions draft no decision, and a later commit drafts
// two and lists an architecture document, which the version waiting for approval carries.
func factoryPlan(later bool) string {
	documents := `"requirements:factory", "spec:factory-api", "milestone:build", "milestone:ship", "milestone:guide"`
	if later {
		documents += `, "architecture:factory"`
	}
	out := `+++
kind = "plan"
id = "factory"
title = "` + FactoryPlanTitle + `"
goal = "factory"
documents = [` + documents + `]
`
	if later {
		out += `
[[decision]]
id = "d1"
question = "Which agent CLI runs the first lanes?"
answer = "The one already installed on the machine."
rationale = "It needs no new sign-in."

[[decision]]
id = "d2"
question = "Does a lane retry a failed check?"
answer = "Once, and then it asks ` + WorldHostileBit + `."
`
	}
	return out + "+++\n\nHow the factory floor is built.\n"
}

const factoryArchitecture = `+++
kind = "architecture"
id = "factory"
title = "How the factory is built"
plan = "factory"
requirements = ["FAC-2"]
+++

The planning core sits behind the API client.
`

const factoryRequirements = `+++
kind = "requirements"
id = "factory"
title = "What the factory must do"
plan = "factory"

[[requirement]]
id = "FAC-1"
text = "A person approves a whole plan in one step."

[[requirement]]
id = "FAC-2"
text = "Shared work is built before the work that uses it."

[[requirement]]
id = "FAC-3"
text = "A person sees which work can run at the same time."

[[requirement]]
id = "FAC-4"
text = "A reviewer's verdict names each criterion."
+++

What the factory must do, and for whom.
`

func factorySpec(body string) string {
	return `+++
kind = "spec"
id = "factory-api"
title = "The factory API"
plan = "factory"
requirements = ["FAC-1", "FAC-3"]
+++

# The factory API

` + body
}

func factoryMilestone(id, title, priority, dependsOn, exit string) string {
	out := "+++\nkind = \"milestone\"\nid = \"" + id + "\"\ntitle = \"" + title + "\"\nplan = \"factory\"\npriority = " + priority + "\n"
	if dependsOn != "" {
		out += "depends_on = [\"" + dependsOn + "\"]\n"
	}
	return out + "\n[[exit_criteria]]\nid = \"e1\"\ntext = \"" + exit + "\"\n+++\n\nWhat " + title + " delivers.\n"
}

// factoryContract writes a contract of the plan. Each criterion is "<id>|<text>|<requirement>" and each
// work order "<id>|<title>|<size>".
func factoryContract(id, title, milestone string, shared bool, uses string, criteria, orders []string) string {
	out := "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"" + title + "\"\nplan = \"factory\"\nmilestone = \"" + milestone + "\"\n"
	if shared {
		out += "shared = true\n"
	}
	if uses != "" {
		out += "uses = [\"" + uses + "\"]\n"
	}
	for _, criterion := range criteria {
		parts := strings.Split(criterion, "|")
		out += "\n[[criteria]]\nid = \"" + parts[0] + "\"\ntext = \"" + parts[1] + "\"\nrequirements = [\"" + parts[2] + "\"]\n"
	}
	for _, order := range orders {
		parts := strings.Split(order, "|")
		out += "\n[[work_orders]]\nid = \"" + parts[0] + "\"\ntitle = \"" + parts[1] + "\"\nsize = \"" + parts[2] + "\"\n"
	}
	return out + "+++\n\nWhat " + title + " is for.\n"
}

// Project is a planned project a world laid out, which a later commit changes.
type Project struct {
	w      *World
	source *fake.WorkSource
}

// Factory lays out a planned project as intake reads it from a repository: a goal, a plan, a requirements
// document, a spec, three milestones where one depends on another, a shared contract that another uses,
// and work in layers. A member approves the plan and the shared work is done. Nothing waits for a person,
// so it can come before the rest of a world, whose waits then keep their ages.
//
// The ledger names a contract and a work order with an ID that sorts at random within one transaction, so
// each contract arrives in a commit of its own, with time between them, and each holds one work order. A
// decision a member approves takes its ID from the plan's, which the ledger generates, so no version a
// member approves here drafts one.
func Factory(w *World, clock *Clock) *Project {
	w.t.Helper()
	source := fake.NewWorkSource()
	clock.Advance(time.Minute)
	source.Write("plans/factory/goal.md", []byte(factoryGoal))
	source.Write("plans/factory/plan.md", []byte(factoryPlan(false)))
	source.Write("plans/factory/requirements.md", []byte(factoryRequirements))
	source.Write(FactorySpecPath, []byte(factorySpec("A client reads the roadmap and approves a plan.\n")))
	source.Write("plans/factory/m-build.md", []byte(factoryMilestone("build", "Build the core", "1", "", "The client and the core run")))
	source.Write("plans/factory/m-ship.md", []byte(factoryMilestone("ship", "Ship the approval page", "2", "build", "A member approves a plan from a browser")))
	source.Write("plans/factory/m-guide.md", []byte(factoryMilestone("guide", "Guide a newcomer", "3", "", "A newcomer follows the guide")))
	w.Planned(source)

	contracts := []struct{ name, file string }{
		{"client", factoryContract("client", "The API client", "build", true, "",
			[]string{"c1|Every call retries once|FAC-2"}, []string{"client-1|Write the API client|large"})},
		{"core", factoryContract("core", "The planning core", "build", false, "client",
			[]string{"c1|A plan version has one digest|FAC-1"}, []string{"core-1|Build the planning core|medium"})},
		{"ui", factoryContract("ui", "The approval page", "ship", false, "",
			[]string{"c1|A member approves a plan in one press|FAC-1", "c2|The roadmap shows parallel work|FAC-3"},
			[]string{"ui-1|Build the approval page|small"})},
		{"guide", factoryContract("guide", "The newcomer guide", "guide", false, "",
			[]string{"c1|The guide shows which work runs at once|FAC-3"}, []string{"guide-1|Write the guide|small"})},
	}
	for _, contract := range contracts {
		clock.Advance(time.Second)
		source.Write("plans/factory/"+contract.name+".md", []byte(contract.file))
		w.Planned(source)
	}

	clock.Advance(time.Minute)
	w.ApprovePlan(FactoryPlanTitle)
	clock.Advance(time.Minute)
	client := w.WorkTitled("Write the API client")
	w.Run(client).End(workorders.OutcomeCompleted, "")
	if _, err := w.Daemon.Client.Accept(w.ctx, client); err != nil {
		w.t.Fatal(err)
	}
	return &Project{w: w, source: source}
}

// Change commits what waits for a member: a commit that drafts two decisions, adds an architecture
// document, and changes the spec, a criterion, and a contract's work, so a second version waits for
// approval with its difference, and beside it work whose contract waits for a member's review because no
// plan approval covers it. The clock stays where it is, so what waits stands last.
func (p *Project) Change() {
	w := p.w
	w.t.Helper()
	p.source.Write("plans/factory/plan.md", []byte(factoryPlan(true)))
	p.source.Write("plans/factory/architecture.md", []byte(factoryArchitecture))
	p.source.Write(FactorySpecPath, []byte(factorySpec("A client reads the roadmap and approves a plan.\n\n## Approvals\n\nAn approval binds to the plan's digest.\n")))
	p.source.Write("plans/factory/core.md", []byte(factoryContract("core", "The planning core", "build", false, "client",
		[]string{"c1|A plan version has one digest, over every record|FAC-1", "c2|A changed criterion asks again|FAC-1"},
		[]string{"core-1|Build the planning core|medium"})))
	p.source.Write("plans/factory/ui.md", []byte(factoryContract("ui", "The approval page", "ship", false, "",
		[]string{"c1|A member approves a plan in one press|FAC-1", "c2|The roadmap shows parallel work|FAC-3"},
		[]string{"ui-1|Build the approval page|small", "ui-2|Show the critical path|small"})))
	w.Planned(p.source)
	w.Recorded(FactoryReview, "Document the retry budget")
}

// ApprovePlan approves the open request for a plan, by its title, as a member would from Needs you.
func (w *World) ApprovePlan(title string) {
	w.t.Helper()
	var view api.NeedsYou
	if err := w.Daemon.Client.ReadView(w.ctx, api.ViewNeedsYou, "", &view); err != nil {
		w.t.Fatal(err)
	}
	for _, item := range view.Items {
		if item.PlanApproval == nil || item.PlanApproval.Title != title {
			continue
		}
		decided, err := w.Daemon.Client.DecideApproval(w.ctx, item.ID, api.DecideApproval{Digest: item.PlanApproval.Approval.SubjectDigest, Allow: true})
		if err != nil {
			w.t.Fatal(err)
		}
		if decided.State != authority.ApprovalApproved {
			w.t.Fatalf("the plan approval is %s", decided.State)
		}
		return
	}
	w.t.Fatalf("no plan approval for %q among %+v", title, view.Items)
}

// WorkTitled names the work order with a title, because intake gives a file's work order an ID of the
// ledger's own.
func (w *World) WorkTitled(title string) string {
	w.t.Helper()
	found := ""
	err := w.Daemon.Store.Read(w.ctx, Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		for _, row := range rows {
			if row.Title == title {
				found = row.ID
			}
		}
		return err
	})
	if err != nil || found == "" {
		w.t.Fatalf("no work order titled %q: %v", title, err)
	}
	return found
}
