package control

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

func planRecord(path, id string) ports.RecordItem {
	return ports.RecordItem{Path: path, Source: path + "@1", Plan: &planning.PlanFile{Kind: planning.FilePlan, ID: id, Title: id}}
}

func milestoneRecord(path, id, plan string, dependsOn ...string) ports.RecordItem {
	file := &planning.MilestoneFile{Kind: planning.FileMilestone, ID: id, Title: id, Plan: plan, DependsOn: dependsOn}
	return ports.RecordItem{Path: path, Source: path + "@1", File: planning.Document{Kind: planning.FileMilestone, Milestone: file}}
}

func contractItem(path, id, plan, milestone string, uses []string, orders ...planning.WorkOrderSpec) ports.WorkItem {
	return ports.WorkItem{Path: path, Source: path + "@1", Contract: planning.ContractFile{
		Kind: planning.FileContract, ID: id, Title: id, Plan: plan, Milestone: milestone, Uses: uses, WorkOrders: orders,
	}}
}

func orderSpec(id string, dependsOn ...string) planning.WorkOrderSpec {
	return planning.WorkOrderSpec{ID: id, Title: id, DependsOn: dependsOn}
}

func pathsOf(batch ports.ChangeBatch) []string {
	var paths []string
	for _, r := range batch.Records {
		paths = append(paths, r.Path)
	}
	for _, i := range batch.Items {
		paths = append(paths, i.Path)
	}
	slices.Sort(paths)
	return paths
}

func TestRefuseCyclesTakesEveryFileOfAPlanWithACycle(t *testing.T) {
	batch := ports.ChangeBatch{
		Cursor:   "7",
		Problems: []planning.SourceProblem{{Path: "bad.md", Detail: "unreadable"}},
		Records: []ports.RecordItem{
			planRecord("calm-plan.md", "calm"),
			planRecord("loop-plan.md", "loop"),
			milestoneRecord("m1.md", "m1", "loop", "m2"),
			milestoneRecord("m2.md", "m2", "loop", "m1"),
			milestoneRecord("calm-m.md", "m", "calm"),
			milestoneRecord("alone-1.md", "x", "", "y"),
			milestoneRecord("alone-2.md", "y", ""),
		},
		Items: []ports.WorkItem{
			contractItem("in-loop.md", "in-loop", "loop", "m1", nil, orderSpec("w1")),
			contractItem("in-calm.md", "in-calm", "calm", "m", nil, orderSpec("w2")),
			contractItem("planless.md", "planless", "", "", nil, orderSpec("w3")),
		},
	}
	kept, refused := refuseCycles(batch)
	if got, want := pathsOf(kept), []string{"alone-1.md", "alone-2.md", "calm-m.md", "calm-plan.md", "in-calm.md", "planless.md"}; !slices.Equal(got, want) {
		t.Errorf("kept %v, want %v", got, want)
	}
	if kept.Cursor != "7" || len(kept.Problems) != 1 {
		t.Errorf("the batch lost its cursor or its problems: %+v", kept)
	}
	var gone []string
	for _, file := range refused {
		gone = append(gone, file.path)
		if file.source != file.path+"@1" || !strings.Contains(file.detail, "plan loop has a dependency cycle: milestone:m1 -> milestone:m2 -> milestone:m1") {
			t.Errorf("refusal %+v", file)
		}
	}
	slices.Sort(gone)
	if want := []string{"in-loop.md", "loop-plan.md", "m1.md", "m2.md"}; !slices.Equal(gone, want) {
		t.Errorf("refused %v, want %v", gone, want)
	}
}

func TestRefuseCyclesReadsWorkOrdersAndSharedContractsTogether(t *testing.T) {
	batch := ports.ChangeBatch{
		Records: []ports.RecordItem{milestoneRecord("m1.md", "m1", "p"), milestoneRecord("m2.md", "m2", "p", "m1")},
		Items: []ports.WorkItem{
			contractItem("app.md", "app", "p", "m1", []string{"base"}, orderSpec("a1")),
			contractItem("base.md", "base", "p", "m2", nil, orderSpec("b1")),
		},
	}
	kept, refused := refuseCycles(batch)
	if len(kept.Records)+len(kept.Items) != 0 || len(refused) != 4 {
		t.Fatalf("kept %v, refused %+v", pathsOf(kept), refused)
	}
	if want := "work_order:a1 -> work_order:b1 -> work_order:a1"; !strings.Contains(refused[0].detail, want) {
		t.Errorf("detail %q does not name %q", refused[0].detail, want)
	}
}

func TestRefuseCyclesKeepsABatchWithoutOne(t *testing.T) {
	batch := ports.ChangeBatch{
		Records: []ports.RecordItem{planRecord("p.md", "p"), milestoneRecord("m1.md", "m1", "p"), milestoneRecord("m2.md", "m2", "p", "m1")},
		Items:   []ports.WorkItem{contractItem("c.md", "c", "p", "m2", nil, orderSpec("w1"), orderSpec("w2", "w1"))},
	}
	kept, refused := refuseCycles(batch)
	if len(refused) != 0 || len(kept.Records) != 3 || len(kept.Items) != 1 {
		t.Fatalf("kept %v, refused %+v", pathsOf(kept), refused)
	}
}
