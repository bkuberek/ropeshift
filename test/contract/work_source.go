package contract

import (
	"context"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// SourceRig is one work source and the way a check changes what it holds.
type SourceRig struct {
	Source ports.WorkSource
	// Commit stands for a person committing one file.
	Commit func(name, content string)
	// Rename stands for a person moving one file.
	Rename func(from, to string)
	// Fork stands for a branch cut here: a rig on that branch, and what merges it back with one file
	// as a person resolved it.
	Fork func() (SourceRig, func(name, content string))
}

// ContractText renders a small contract file.
func ContractText(id, title, statement string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "` + title + `"
requirements = ["PAY-12"]

[[criteria]]
id = "c1"
text = "It works"

[[work_orders]]
id = "` + id + `-order"
title = "The work order of ` + id + `"
priority = 2
size = "small"
+++

` + statement + "\n"
}

// GoalText renders a small goal file.
func GoalText(id, title string) string {
	return `+++
kind = "goal"
id = "` + id + `"
title = "` + title + `"
success_measures = ["It holds"]
+++

Why.
`
}

// PlanText renders a small plan file under a goal.
func PlanText(id, title, goal string) string {
	return `+++
kind = "plan"
id = "` + id + `"
title = "` + title + `"
goal = "` + goal + `"
+++

How.
`
}

// RequirementsText renders a requirements file with one requirement.
func RequirementsText(id, key, text string) string {
	return `+++
kind = "requirements"
id = "` + id + `"

[[requirement]]
id = "` + key + `"
text = "` + text + `"
+++
`
}

// RunWorkSource runs one contract on one source. The adapter and its fake both
// pass it, so a fake cannot drift from the source it stands for.
func RunWorkSource(t *testing.T, fresh func(*testing.T) SourceRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, SourceRig)
	}{
		{"a poll reads a contract", checkPollReads},
		{"an unchanged source returns nothing", checkPollQuiet},
		{"a changed file gets a new digest and keeps its key", checkPollChanged},
		{"a moved file keeps its key", checkPollRenamed},
		{"a bad file is a problem and stops no other file", checkPollBadFile},
		{"every file kind is read, with goals first", checkPollKinds},
		{"read returns one item by key", checkRead},
		{"apply reports unsupported", checkApply},
		{"no webhooks and no write capability", checkCapabilities},
		{"health and fields answer", checkHealthAndFields},
		{"a later commit stands ahead of an earlier one", checkLineageOfOneLine},
		{"a branch that left a file alone stands behind what moved it", checkLineageOfAStaleBranch},
		{"two sides that changed a file have diverged", checkLineageOfTwoSides},
		{"a squash keeps what its text knew", checkLineageOfASquash},
		{"a merge holds both sides", checkLineageOfAMerge},
		{"a version nothing holds any more has nothing to compare", checkLineageOfALostVersion},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

// lineageFile is the one file the lineage checks read.
const lineageFile = "contract.md"

func lineageText(statement string) string { return ContractText("alpha", "Alpha", statement) }

// sourceOf is where a rig's contract stands now.
func sourceOf(t *testing.T, ctx context.Context, rig SourceRig) string {
	t.Helper()
	batch, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	return onlyItem(t, batch).Source
}

func wantLineage(t *testing.T, ctx context.Context, rig SourceRig, read, recorded string, want ports.Lineage) {
	t.Helper()
	got, err := rig.Source.Relate(ctx, read, recorded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s against %s reads %s, want %s", read, recorded, got, want)
	}
}

func checkLineageOfOneLine(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit(lineageFile, lineageText("One."))
	earlier := sourceOf(t, ctx, rig)
	rig.Commit(lineageFile, lineageText("Two."))
	later := sourceOf(t, ctx, rig)
	wantLineage(t, ctx, rig, later, earlier, ports.LineageAhead)
	wantLineage(t, ctx, rig, earlier, later, ports.LineageBehind)
	rig.Commit(lineageFile, lineageText("One."))
	wantLineage(t, ctx, rig, sourceOf(t, ctx, rig), later, ports.LineageAhead)
}

func checkLineageOfAStaleBranch(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit(lineageFile, lineageText("One."))
	branch, _ := rig.Fork()
	rig.Commit(lineageFile, lineageText("Two."))
	branch.Commit("other.md", "# Ordinary prose\n")
	wantLineage(t, ctx, rig, sourceOf(t, ctx, branch), sourceOf(t, ctx, rig), ports.LineageBehind)
	wantLineage(t, ctx, rig, sourceOf(t, ctx, rig), sourceOf(t, ctx, branch), ports.LineageAhead)
}

func checkLineageOfTwoSides(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit(lineageFile, lineageText("One."))
	branch, _ := rig.Fork()
	rig.Commit(lineageFile, lineageText("On main."))
	branch.Commit(lineageFile, lineageText("On the branch."))
	wantLineage(t, ctx, rig, sourceOf(t, ctx, branch), sourceOf(t, ctx, rig), ports.LineageDiverged)
	wantLineage(t, ctx, rig, sourceOf(t, ctx, rig), sourceOf(t, ctx, branch), ports.LineageDiverged)
}

// A squash or a rebase keeps a branch's text and loses its commits, so a later edit on main still
// knew what the branch said.
func checkLineageOfASquash(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit(lineageFile, lineageText("One."))
	branch, _ := rig.Fork()
	branch.Commit(lineageFile, lineageText("From the branch."))
	recorded := sourceOf(t, ctx, branch)
	rig.Commit(lineageFile, lineageText("From the branch."))
	rig.Commit(lineageFile, lineageText("Then edited."))
	wantLineage(t, ctx, rig, sourceOf(t, ctx, rig), recorded, ports.LineageAhead)
}

func checkLineageOfAMerge(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit(lineageFile, lineageText("One."))
	branch, merge := rig.Fork()
	rig.Commit(lineageFile, lineageText("On main."))
	onMain := sourceOf(t, ctx, rig)
	branch.Commit(lineageFile, lineageText("On the branch."))
	onBranch := sourceOf(t, ctx, branch)
	merge(lineageFile, lineageText("Both."))
	merged := sourceOf(t, ctx, rig)
	wantLineage(t, ctx, rig, merged, onMain, ports.LineageAhead)
	wantLineage(t, ctx, rig, merged, onBranch, ports.LineageAhead)
}

func checkLineageOfALostVersion(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit(lineageFile, lineageText("One."))
	wantLineage(t, ctx, rig, sourceOf(t, ctx, rig), lineageFile+"@0000000000000000000000000000000000000000", ports.LineageAhead)
}

func onlyItem(t *testing.T, batch ports.ChangeBatch) ports.WorkItem {
	t.Helper()
	if len(batch.Items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(batch.Items), batch)
	}
	return batch.Items[0]
}

func checkPollReads(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "The statement."))
	rig.Commit("notes.md", "# Ordinary prose\n")
	batch, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	item := onlyItem(t, batch)
	description, err := rig.Source.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if item.Key.Adapter != description.Adapter || item.Key.Instance != description.Instance || item.Key.NativeID != "alpha" {
		t.Errorf("key %+v under %+v", item.Key, description)
	}
	if item.Digest == "" || item.Source == "" {
		t.Errorf("item %+v", item)
	}
	if item.Contract.Title != "Alpha" || len(item.Contract.WorkOrders) != 1 || item.Statement != "The statement.\n" {
		t.Errorf("item %+v", item)
	}
}

func checkPollQuiet(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	first, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := rig.Source.Poll(ctx, first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 0 || second.Cursor != first.Cursor {
		t.Errorf("an unchanged source returned %+v", second)
	}
}

func checkPollChanged(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	first, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "Two."))
	second, err := rig.Source.Poll(ctx, first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	before, after := onlyItem(t, first), onlyItem(t, second)
	if before.Key != after.Key {
		t.Errorf("the key moved from %+v to %+v", before.Key, after.Key)
	}
	if before.Digest == after.Digest {
		t.Errorf("a changed file kept digest %s", after.Digest)
	}
	if second.Cursor == first.Cursor {
		t.Error("the cursor stayed")
	}
}

func checkPollRenamed(t *testing.T, ctx context.Context, rig SourceRig) {
	text := ContractText("alpha", "Alpha", "One.")
	rig.Commit("old.md", text)
	first, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	rig.Rename("old.md", "moved/new.md")
	second, err := rig.Source.Poll(ctx, first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	before, after := onlyItem(t, first), onlyItem(t, second)
	if before.Key != after.Key || before.Digest != after.Digest {
		t.Errorf("a move changed %+v to %+v", before, after)
	}
}

func checkPollBadFile(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("bad.md", "+++\nkind = \"contract\"\nid = unquoted\n+++\n")
	rig.Commit("good.md", ContractText("alpha", "Alpha", "One."))
	batch, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	onlyItem(t, batch)
	if len(batch.Problems) != 1 || batch.Problems[0].Path != "bad.md" {
		t.Errorf("problems %+v", batch.Problems)
	}
}

func checkPollKinds(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("a-requirements.md", RequirementsText("payments", "PAY-12", "It retries"))
	rig.Commit("b-plan.md", PlanText("checkout-plan", "The plan", "checkout-goal"))
	rig.Commit("c-goal.md", GoalText("checkout-goal", "The goal"))
	rig.Commit("d-contract.md", ContractText("alpha", "Alpha", "One."))
	batch, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	onlyItem(t, batch)
	if len(batch.Records) != 3 {
		t.Fatalf("records %+v", batch.Records)
	}
	goal, plan, requirements := batch.Records[0], batch.Records[1], batch.Records[2]
	if goal.Goal == nil || goal.Key.NativeID != "checkout-goal" || goal.Source == "" || goal.Statement != "Why.\n" {
		t.Errorf("goal %+v", goal)
	}
	if plan.Plan == nil || plan.Plan.Goal != "checkout-goal" || plan.Key.NativeID != "checkout-plan" {
		t.Errorf("plan %+v", plan)
	}
	if requirements.Requirements == nil || requirements.Requirements.Requirements[0].ID != "PAY-12" {
		t.Errorf("requirements %+v", requirements)
	}
	description, err := rig.Source.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if goal.Key.Adapter != description.Adapter || goal.Key.Instance != description.Instance {
		t.Errorf("key %+v under %+v", goal.Key, description)
	}
}

func checkRead(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	batch, err := rig.Source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	key := onlyItem(t, batch).Key
	item, err := rig.Source.Read(ctx, key)
	if err != nil || item.Key != key {
		t.Fatalf("read %+v: %v", item, err)
	}
	key.NativeID = "absent"
	if _, err := rig.Source.Read(ctx, key); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an absent key: %v", err)
	}
}

func checkApply(t *testing.T, ctx context.Context, rig SourceRig) {
	result, err := rig.Source.Apply(ctx, ports.OutboxItem{ID: "item"})
	if err != nil || result.Status != ports.WriteUnsupported {
		t.Errorf("apply: %+v, %v", result, err)
	}
}

func checkCapabilities(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	description, err := rig.Source.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if description.Capabilities.Write || description.Capabilities.Webhooks {
		t.Errorf("capabilities %+v", description.Capabilities)
	}
	if len(description.APIVersions) == 0 {
		t.Error("no API version declared")
	}
	if _, err := rig.Source.VerifyDelivery(ctx, ports.InboundDelivery{ID: "d"}); !errors.Is(err, ports.ErrUnsupported) {
		t.Errorf("verify delivery: %v", err)
	}
}

func checkHealthAndFields(t *testing.T, ctx context.Context, rig SourceRig) {
	rig.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	health, err := rig.Source.Health(ctx)
	if err != nil || health.State != planning.HealthOK {
		t.Errorf("health %+v, %v", health, err)
	}
	fields, err := rig.Source.Fields(ctx, ports.ScopeRef{})
	if err != nil || len(fields) == 0 {
		t.Errorf("fields %+v, %v", fields, err)
	}
}
