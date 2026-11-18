package control_test

import (
	"reflect"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// templatedOf lists each file of the plan that is written from a template, as "path from template".
func templatedOf(plan planning.AdoptionPlan) []string {
	var files []string
	for _, file := range plan.Files {
		if file.Template != "" {
			files = append(files, file.Path+" from "+file.Template)
		}
	}
	return files
}

// The files a layout lacks are the files of the proposed shape that the planning repository does not hold, each
// with the template it is written from: a single repository lacks only its instruction files, and they come from the
// monorepo template, because the repository holds the plan and the code.
func TestTheDryRunOfASingleRepositoryListsOnlyTheInstructionFilesItLacks(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m)
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Shape != planning.ShapeSingle || !plan.Offers() {
		t.Fatalf("a lone clone with no instruction file reads %s and offers a restructuring: %v", plan.Shape, plan.Offers())
	}
	if want := []string{"add AGENTS.md", "add CLAUDE.md"}; !reflect.DeepEqual(filesOf(plan), want) {
		t.Errorf("the single shape lists %v, want %v", filesOf(plan), want)
	}
	if want := []string{"AGENTS.md from monorepo/agents.md", "CLAUDE.md from claude.md"}; !reflect.DeepEqual(templatedOf(plan), want) {
		t.Errorf("the single shape writes %v, want %v", templatedOf(plan), want)
	}
	if len(plan.Steps) != 0 || len(plan.Moves) != 0 {
		t.Errorf("a lone clone has no code repository to take steps in: %+v %+v", plan.Steps, plan.Moves)
	}

	// Either instruction file is an instruction file, so a clone that holds one is left alone.
	for _, held := range []string{"AGENTS.md", "CLAUDE.md"} {
		other := newAdoptionMachine(t)
		workspaceOf(other, held)
		if got := other.mustPlan(control.AdoptionRequest{Root: "/work/ws"}); got.Offers() || len(got.Files) != 0 {
			t.Errorf("a lone clone that holds %s lists %v and offers %v", held, filesOf(got), got.Offers())
		}
	}
}

// Every shape but single also lacks a Makefile and an ignore file, each only when the planning repository holds
// none, and the instruction files come from the workspace template.
func TestTheDryRunOfEachShapeListsExactlyTheFilesTheLayoutLacks(t *testing.T) {
	t.Parallel()
	const (
		agents = "AGENTS.md from workspace/agents.md"
		claude = "CLAUDE.md from claude.md"
		make   = "Makefile from workspace/makefile"
		ignore = ".gitignore from workspace/gitignore"
	)
	planned := func(tracked ...string) fake.CloneLayout {
		return fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 2, Manifest: true}, Tracked: append([]string{"README.md", "plan/repositories.md"}, tracked...)}
	}
	beside := func(tracked ...string) fake.CloneLayout {
		return fake.CloneLayout{Tracked: append([]string{"README.md"}, tracked...)}
	}
	tests := []struct {
		name   string
		shape  planning.LayoutShape
		layout fake.CloneLayout
		want   []string
		all    []string
	}{
		{name: "planned, nothing held", shape: planning.ShapePlanned, layout: planned(), want: []string{ignore, make, agents, claude},
			all: []string{"add .gitignore", "add Makefile", "add AGENTS.md", "add CLAUDE.md"}},
		{name: "planned, a Makefile and an ignore file held", shape: planning.ShapePlanned, layout: planned("Makefile", ".gitignore"), want: []string{agents, claude},
			all: []string{"add AGENTS.md", "add CLAUDE.md"}},
		{name: "planned, a CLAUDE.md held", shape: planning.ShapePlanned, layout: planned("CLAUDE.md", "Makefile", ".gitignore")},
		{name: "workspace files, nothing held", shape: planning.ShapeWorkspaceFiles, layout: beside(), want: []string{ignore, make, agents, claude},
			all: []string{"add plan/repositories.md", "add .gitignore", "add Makefile", "add AGENTS.md", "add CLAUDE.md"}},
		{name: "workspace files, an AGENTS.md and a Makefile held", shape: planning.ShapeWorkspaceFiles, layout: beside("AGENTS.md", "Makefile", ".gitignore"),
			all: []string{"add plan/repositories.md", "append .gitignore"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newAdoptionMachine(t)
			m.open("/work/ws", tt.layout)
			m.checkouts.Ignore("/work/ws", planning.WorktreesFolder)
			request := control.AdoptionRequest{Root: "/work/ws"}
			if tt.shape == planning.ShapePlanned {
				m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod", "AGENTS.md"}})
			} else {
				m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod", "AGENTS.md"}})
				request.Paths = []string{"/work/api"}
			}
			plan := m.mustPlan(request)
			if plan.Shape != tt.shape {
				t.Fatalf("the layout reads %s, want %s", plan.Shape, tt.shape)
			}
			if got := templatedOf(plan); !slices.Equal(got, tt.want) {
				t.Errorf("the files written from templates are %v, want %v", got, tt.want)
			}
			if tt.all != nil && !slices.Equal(filesOf(plan), tt.all) {
				t.Errorf("the plan lists %v, want %v", filesOf(plan), tt.all)
			}
		})
	}
}

// The files are listed against the tree the planning repository holds once the design repository's history is imported,
// so a file the merge brings is not listed beside itself, and an ignore file it brings is appended to, not written.
func TestAConsolidationListsTheFilesTheMergedTreeLacks(t *testing.T) {
	t.Parallel()
	bare := func(m *adoptionMachine) {
		m.open("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md"}})
		m.checkouts.Ignore("/work/ws", planning.WorktreesFolder)
	}
	tests := []struct {
		name   string
		design []string
		want   []string
	}{
		{name: "it brings an AGENTS.md", design: []string{"AGENTS.md"},
			want: []string{".gitignore from workspace/gitignore", "Makefile from workspace/makefile"}},
		{name: "it brings a CLAUDE.md", design: []string{"CLAUDE.md"},
			want: []string{".gitignore from workspace/gitignore", "Makefile from workspace/makefile"}},
		{name: "it brings an ignore file, a Makefile, and the instructions", design: []string{".gitignore", "Makefile", "AGENTS.md"}},
		{name: "it brings none of them", design: []string{"docs/a.md"},
			want: []string{".gitignore from workspace/gitignore", "Makefile from workspace/makefile", "AGENTS.md from workspace/agents.md", "CLAUDE.md from claude.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newAdoptionMachine(t)
			bare(m)
			m.clone("/work/ws/repos/design", fake.CloneLayout{Plan: ports.PlanContent{Files: 3}, Tracked: append([]string{"plan/goal.md"}, tt.design...)})
			plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
			if plan.Shape != planning.ShapeConsolidate {
				t.Fatalf("the layout reads %s", plan.Shape)
			}
			var got []string
			for _, file := range plan.Files {
				if file.Template != "" && file.Path != "plan/repositories.md" {
					got = append(got, file.Path+" from "+file.Template)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("the plan writes %v, want %v", got, tt.want)
			}
		})
	}

	// An ignore file the merge brings takes the lines that leave the folders out, as the file it merges with would.
	m := newAdoptionMachine(t)
	m.open("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md", "AGENTS.md", "Makefile"}})
	m.clone("/work/ws/repos/design", fake.CloneLayout{Plan: ports.PlanContent{Files: 3}, Tracked: []string{"plan/goal.md", ".gitignore"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if got := ignoreLinesOf(plan); !slices.Equal(got, []string{planning.WorktreesLine}) || slices.Contains(filesOf(plan), "add .gitignore") {
		t.Errorf("a brought ignore file is %v, and the plan lists %v", got, filesOf(plan))
	}
}

// A code repository whose clone holds no instruction file is a step for the person, with the command that writes the
// repository template to it, because adoption writes to no clone but the planning repository's. The planning
// repository, the design repository a consolidation imports, and a clone that holds one are none.
func TestTheDryRunListsEachCodeRepositoryThatLacksInstructionsAsAStepForThePerson(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m, "AGENTS.md")
	m.clone("/work/ws/repos/web", fake.CloneLayout{Tracked: []string{"package.json", "CLAUDE.md"}})
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/with space", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/design", fake.CloneLayout{Plan: ports.PlanContent{Files: 3}, Tracked: []string{"plan/goal.md"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Shape != planning.ShapeConsolidate {
		t.Fatalf("the layout reads %s", plan.Shape)
	}
	want := []planning.Step{
		{Name: "api", Path: "/work/ws/repos/api", Commands: []string{"ropeshift template show repository/agents.md > /work/ws/repos/api/AGENTS.md"}},
		{Name: "with space", Path: "/work/ws/repos/with space", Commands: []string{"ropeshift template show repository/agents.md > '/work/ws/repos/with space/AGENTS.md'"}},
	}
	if !reflect.DeepEqual(plan.Steps, want) {
		t.Errorf("the steps read %+v, want %+v", plan.Steps, want)
	}

	// Without a design repository to import, the design repository is a code repository like the rest, and the
	// planning repository is still none.
	other := newAdoptionMachine(t)
	workspaceOf(other)
	other.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod", "AGENTS.md"}})
	other.clone("/work/ws/repos/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	got := other.mustPlan(control.AdoptionRequest{Root: "/work/ws", Planning: "ws", Design: "ws"})
	if len(got.Steps) != 1 || got.Steps[0].Name != "web" {
		t.Errorf("the steps read %+v, want one for web", got.Steps)
	}

	// The steps are part of the plan, so a layout whose clone gained its instruction file reads another plan.
	other.layouts.Set("/work/ws/repos/web", fake.CloneLayout{Tracked: []string{"package.json", "AGENTS.md"}})
	after := other.mustPlan(control.AdoptionRequest{Root: "/work/ws", Planning: "ws", Design: "ws"})
	if len(after.Steps) != 0 || after.Digest == got.Digest || !reflect.DeepEqual(after.DiffersIn(got.Parts()), []string{planning.PartLayout, planning.PartSteps}) {
		t.Errorf("a clone that gained its file reads steps %+v, digest %s, and differs in %v", after.Steps, after.Digest, after.DiffersIn(got.Parts()))
	}
}
