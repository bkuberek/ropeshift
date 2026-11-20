package lanes

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A session has no document of the format, so a planning lane's brief carries it whole, and says
// what the ledger takes from it.
func TestAPlanningBriefCarriesTheFormat(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	for _, want := range []string{"Commit the files on this branch", "accepts your work", ToolLaneReport} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// The person approves a plan in one step, and the lane that wrote it cannot (FR-PLAN-12). It is told what
// that step does, so it does not wait for a review per work order, and that a change after it is a new
// version.
func TestAPlanningBriefSaysHowThePlanIsApproved(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	for _, want := range []string{
		"approves the plan in one step", "as their decisions", "contract review", "cannot approve the plan yourself", "new version of the plan",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// A planning lane writes every format it is shown, so it is shown all of them: each document kind,
// milestones, contracts with their work orders, and the draft decisions of a plan (FR-SESSION-5).
func TestAPlanningBriefShowsEveryFormat(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	if len(planning.FormatExamples) < 8 {
		t.Fatalf("%d examples", len(planning.FormatExamples))
	}
	for _, example := range planning.FormatExamples {
		if !strings.Contains(brief, example.Text) {
			t.Errorf("the brief leaves out the %s example", example.Kind)
		}
		if !strings.Contains(brief, example.Intro) {
			t.Errorf("the brief leaves out what the %s example is for", example.Kind)
		}
	}
	for _, want := range []string{
		"[[decision]]", "[[exit_criteria]]", "[[work_orders]]", "[[requirement]]", "assets", "depends_on", "shared", "uses", "documents",
		"kind:id", "binds nothing until", "no requirement", "version",
		"is not a milestone of its plan", "marked shared", "form a cycle", "wait for", "every contract of it names one",
		"recommend one of them", "recommendation_rationale", "a short title", "tradeoffs", "unblocks", "sent back",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief says nothing of %q", want)
		}
	}
}

func TestAReviewBriefCarriesThePlanAndAsksForEverything(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Review it", Kind: workorders.KindContractReview, Review: &ledger.DesiredReview{
		Plan: "The plan",
		Contracts: []ledger.ReviewedContract{
			{Title: "Alpha", Statement: "Retry twice.", Criteria: []string{"c1: It retries."}},
			{Title: "Beta", Statement: "Log it."},
		},
	}})
	for _, want := range []string{
		"Plan: The plan", "Contract: Alpha", "Retry twice.", "c1: It retries.", "Contract: Beta", "Log it.", ToolAsk, "blocking", "Do not change any file",
		"recommend one of them", "recommendation_rationale", "a short title", "tradeoffs", "unblocks", "sent back",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
}

// Work that plans nothing and reviews nothing reads as it always has.
func TestAChangeIsToldNeither(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange})
	for _, absent := range []string{"front matter", "contract review"} {
		if strings.Contains(brief, absent) {
			t.Errorf("a change's brief says %q", absent)
		}
	}
}

func reviewedAttempt() *ledger.DesiredAttempt {
	return &ledger.DesiredAttempt{
		WorkOrder: "wo-1", Title: "Add the retry budget", Branch: "lane/lane-1", Commit: "c0ffee0000000000000000000000000000000001",
		BaseRef: "origin/main", BaseCommit: "ba5e000000000000000000000000000000000002",
	}
}

// A review lane is told what it judges, that it may change nothing, where the diff is, and how to
// report: one verdict for each criterion.
func TestAReviewBriefSaysWhatIsJudgedAndWhatIsForbidden(t *testing.T) {
	brief := Brief(BriefSpec{
		WorkOrder: "rv-1", Title: "Review: Add the retry budget", Statement: "Retry twice.", Criteria: []string{"c1: It retries.", "c2: It logs."},
		Pack: true, Kind: workorders.KindReview, Attempt: reviewedAttempt(),
	})
	for _, want := range []string{
		"work order wo-1", "Add the retry budget", "c0ffee0000000000000000000000000000000001", "lane/lane-1",
		"ba5e000000000000000000000000000000000002", "origin/main", "You did not write it",
		"may not edit a file with a tool, commit, push, or move a branch", ReviewDiffFile, ReviewFilesFile,
		"c1: It retries.", "c2: It logs.", "requirements and documents the contract cites", "information and not evidence",
		"a verdict for every acceptance criterion", ToolLaneReport, "cannot_judge", "citations", "a report that does is refused",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, brief)
		}
	}
}

// A review of work that recorded no base has no diff, and the brief says so and sends the reviewer to
// the history instead of naming a file that holds nothing.
func TestAReviewBriefWithNoRecordedBaseHasNoDiff(t *testing.T) {
	attempt := reviewedAttempt()
	attempt.BaseRef, attempt.BaseCommit = "", ""
	brief := Brief(BriefSpec{WorkOrder: "rv-1", Title: "Review", Kind: workorders.KindReview, Attempt: attempt})
	if !strings.Contains(brief, "recorded no base") || !strings.Contains(brief, "git log") || strings.Contains(brief, ReviewFilesFile) {
		t.Errorf("the brief reads:\n%s", brief)
	}
}

// Every other lane reads as it always has.
func TestAChangeIsNotToldItReviews(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange})
	for _, absent := range []string{"This is a review", ReviewDiffFile, "you may not edit"} {
		if strings.Contains(brief, absent) {
			t.Errorf("a change's brief says %q", absent)
		}
	}
}

// ADR-0023: a lane that finishes someone's branch is told what its branch already holds, where the change is
// measured from, and that the branch itself stays as its author left it.
func TestABriefSaysWhichBranchTheWorkFinishes(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo-1", Title: "Finish the retry branch", Continues: &Continuation{Branch: "feature/retry", Tip: "tip123", Base: "fork456"}})
	for _, want := range []string{"finishes branch feature/retry", "tip123", "git log fork456..HEAD", "Never push to feature/retry"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	if plain := Brief(BriefSpec{WorkOrder: "wo-1", Title: "Add the budget"}); strings.Contains(plain, "finishes branch") {
		t.Errorf("a brief for new work speaks of a branch:\n%s", plain)
	}
}

// ADR-0022: a lane is told where each other repository of its project is checked out for it to read, at
// which commit, that it may change nothing there, and which ones it could not be given.
func TestABriefNamesTheRepositoriesTheLaneReads(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo-1", Title: "Plan it", Kind: workorders.KindPlan, Reads: []ReadTree{
		{Name: "api", Path: "/work/plan-lanes/lane-1.reads/api", Ref: "origin/main", Commit: "abc123"},
		{Name: "web", Detail: "no checkout of it on this machine"},
	}})
	for _, want := range []string{
		"/work/plan-lanes/lane-1.reads/api", "api", "abc123", "origin/main", "read-only",
		"web", "no checkout of it on this machine",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	if plain := Brief(BriefSpec{WorkOrder: "wo-1", Title: "Plan it", Kind: workorders.KindPlan}); strings.Contains(plain, "read-only") {
		t.Errorf("a brief with nothing to read speaks of it:\n%s", plain)
	}
}

// ADR-0023: an adopt lane is a planning lane told to onboard what the project already holds. Its brief says
// where the inventory is, gives each rule of adoption, and shows every record it writes for existing work,
// with an existing document before and after onboarding, so a real agent has an example of each.
func TestAnAdoptBriefCarriesTheRulesAndAnExampleOfEachRecord(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true})
	if len(planning.AdoptionExamples) < 5 {
		t.Fatalf("%d examples", len(planning.AdoptionExamples))
	}
	for _, example := range planning.AdoptionExamples {
		if !strings.Contains(brief, example.Text) || !strings.Contains(brief, example.Intro) {
			t.Errorf("the brief leaves out the %s example of existing work", example.Kind)
		}
	}
	if !strings.Contains(brief, planning.OnboardedExample.Before) || !strings.Contains(brief, planning.OnboardedExample.After) {
		t.Error("the brief leaves out the document before and after onboarding")
	}
	if !strings.Contains(brief, planning.OnboardedDecisionExample.Before) || !strings.Contains(brief, planning.OnboardedDecisionExample.After) {
		t.Error("the brief leaves out the decision record before and after onboarding")
	}
	for _, want := range []string{"status line", "proposed when it states none"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the adopt brief says nothing of %q", want)
		}
	}
	for _, want := range []string{
		InventoryFile, "Onboard, do not rewrite", "keep its prose", "existing IDs", "git mv",
		"belongs to another tool", "contradict", "draft decision",
		"status = \"met\"", "evidence", "existing = true", "no work orders",
		"shared contracts first", "continues", "[[left_out]]", "reason",
		"Write nothing else", "your own branch", "default branch",
		// The planning lane's own rules still hold.
		"Commit the files on this branch", "approves the plan in one step",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the adopt brief says nothing of %q", want)
		}
	}
}

// The inventory's shape in the brief is what the worker writes, so the agent reads the file it is shown.
func TestAnAdoptBriefShowsTheInventoryAsTheWorkerWritesIt(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true})
	raw, err := planning.RenderInventory(inventoryExample)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, string(raw)) {
		t.Errorf("the brief does not show the inventory as RenderInventory writes it:\n%s", brief)
	}
	if len(inventoryExample.Repositories) == 0 || len(inventoryExample.Repositories[0].Branches) == 0 || len(inventoryExample.Repositories[0].Documents) == 0 {
		t.Errorf("the example inventory shows no document or branch: %+v", inventoryExample)
	}
}

func aRestructured() *Restructured {
	return &Restructured{
		Commit: "9fceb02d0ae598e95dc970b74767f19372d61af8", Subject: planning.WorkspaceCommitSubject,
		Files:    []string{".gitignore", "Makefile", "plan/repositories.md"},
		Manifest: "repositories:shop",
		Moves: []planning.Move{
			{Name: "api", From: "/work/api", To: "/work/ws/repos/api", Commands: []string{"mv /work/api /work/ws/repos/api", "ropeshift repository add /work/ws/repos/api"}},
			{Name: "web", From: "/work/web", To: "/work/ws/repos/web", Commands: []string{"mv /work/web /work/ws/repos/web", "ropeshift repository add /work/ws/repos/web"}},
		},
	}
}

// An adopt lane whose repository the worker restructured is told which commit holds the workspace files, which
// files they are, that the plan lists the manifest in its documents by the reference the brief gives, and that
// the moves are the person's commands and never its own.
func TestAnAdoptBriefTellsTheCommitTheFilesAndTheMovesTheWorkerLeft(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true, Restructured: aRestructured()})
	for _, want := range []string{
		"9fceb02d0ae598e95dc970b74767f19372d61af8", planning.WorkspaceCommitSubject, ".gitignore", "Makefile", "plan/repositories.md",
		"`repositories:shop`", "documents", "mv /work/api /work/ws/repos/api", "ropeshift repository add /work/ws/repos/web", "never run them",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, brief)
		}
	}
}

// When the repository held every file already, the brief says the worker made no commit, names no manifest it did
// not write, and still lists the moves.
func TestAnAdoptBriefWithNoCommitSaysTheWorkerMadeNoneAndNamesNoManifest(t *testing.T) {
	left := aRestructured()
	left.Commit, left.Files, left.Manifest = "", nil, ""
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true, Restructured: left})
	for _, want := range []string{"made no commit", "mv /work/api /work/ws/repos/api"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q", want)
		}
	}
	for _, unwanted := range []string{"repositories:shop", planning.WorkspaceCommitSubject} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("a brief with no commit holds %q", unwanted)
		}
	}
}

const startingAgents = "# AGENTS.md\n\n> Started for the shop project. Replace each placeholder, and delete every line that starts with `>`.\n\n## The project\n"

// An adopt brief for a repository with no instruction file shows the starting AGENTS.md the template renders, and
// says to write it from what the project holds, with a CLAUDE.md that imports it, whether or not the worker
// restructured the repository.
func TestAnAdoptBriefWithNoInstructionFileHoldsTheStartingTemplate(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true, InstructionsTemplate: startingAgents})
	for _, want := range []string{
		"holds no instruction file", "AGENTS.md", "CLAUDE.md", "@AGENTS.md", "```markdown\n" + startingAgents + "```", "delete every line that starts with `>`",
		"the only files you write besides the plan files",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "The worker wrote") {
		t.Errorf("a brief for a repository nobody restructured says the worker wrote the file:\n%s", brief)
	}

	// When the worker wrote AGENTS.md in its commit, the lane edits the committed file, and the brief shows no template.
	written := aRestructured()
	written.Files = []string{".gitignore", "AGENTS.md", "CLAUDE.md", "Makefile", "plan/repositories.md"}
	again := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true, Restructured: written})
	for _, want := range []string{"The worker wrote AGENTS.md, with a CLAUDE.md that imports it, in the commit above", "Edit the committed AGENTS.md", "delete every line that starts with `>`"} {
		if !strings.Contains(again, want) {
			t.Errorf("the brief of a restructured repository leaves out %q:\n%s", want, again)
		}
	}
	if strings.Contains(again, "```markdown\n# AGENTS.md") || strings.Contains(again, "the starting file below") {
		t.Errorf("the brief shows a template beside the file the worker wrote:\n%s", again)
	}
}

// With an instruction file in the repository, with no adopt work, or with no template, the brief holds none.
func TestABriefHoldsTheStartingTemplateOnlyForAnAdoptLaneThatWasGivenOne(t *testing.T) {
	for name, spec := range map[string]BriefSpec{
		"no template":           {Kind: workorders.KindPlan, Adopt: true},
		"not an adopt":          {Kind: workorders.KindPlan, InstructionsTemplate: startingAgents},
		"not planning":          {Kind: workorders.KindChange, Adopt: true, InstructionsTemplate: startingAgents},
		"written, not an adopt": {Kind: workorders.KindPlan, Restructured: &Restructured{Files: []string{"AGENTS.md"}}},
	} {
		spec.WorkOrder, spec.Title = "wo", "Adopt the project"
		if brief := Brief(spec); strings.Contains(brief, "holds no instruction file") || strings.Contains(brief, "The worker wrote AGENTS.md") || strings.Contains(brief, startingAgents) {
			t.Errorf("%s: the brief holds the starting template:\n%s", name, brief)
		}
	}
}

func aConsolidated() *Restructured {
	return &Restructured{
		Commit: "5d3c0e8a9f1b2c4d6e7f8091a2b3c4d5e6f70812", Subject: planning.ImportCommitSubject("design"), Manifest: "repositories:shop",
		Consolidated: &Consolidated{
			Design: "design", SecondParent: "c0ffee1234567890abcdef1234567890abcdef12",
			Collisions: []planning.Collision{
				{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "AGENTS.md", Kept: planning.KeptWorkspace}, {Path: "README.md", Kept: planning.KeptWorkspace},
				{Path: "Makefile", DesignPath: "makefile", Kept: planning.KeptWorkspace}, {Path: ".GITIGNORE", DesignPath: ".gitignore", Kept: planning.KeptUnion},
			},
			LeftOut: []string{"tools"}, Instructions: []string{"AGENTS.md", "CLAUDE.md"},
		},
	}
}

// An adopt lane whose repository the worker consolidated is told the merge commit and its second parent, every
// colliding path with the version kept, every tree left out, and the design repository's instruction files at the
// second parent, and is told to merge those instructions into the workspace's own as one commit, keeping both sets of
// rules and asking the person about each contradiction.
func TestAnAdoptBriefTellsTheMergeItsSecondParentTheCollisionsAndTheInstructionsToMerge(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true, Restructured: aConsolidated()})
	for _, want := range []string{
		"5d3c0e8a9f1b2c4d6e7f8091a2b3c4d5e6f70812", planning.ImportCommitSubject("design"), "second parent", "c0ffee1234567890abcdef1234567890abcdef12",
		"- .gitignore, the lines of both files", "- AGENTS.md, this repository's version", "- README.md, this repository's version",
		"- Makefile, this repository's version, which the design repository spells makefile: `git show c0ffee1234567890abcdef1234567890abcdef12:makefile`",
		"- .GITIGNORE, the lines of both files, this repository's first, which the design repository spells .gitignore: `git show c0ffee1234567890abcdef1234567890abcdef12:.gitignore`",
		"- tools", "git show c0ffee1234567890abcdef1234567890abcdef12:AGENTS.md", "git show c0ffee1234567890abcdef1234567890abcdef12:CLAUDE.md",
		"one commit", "keeping both sets of rules", "ask the person about each contradiction", "`repositories:shop`",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "holds these files") {
		t.Errorf("a consolidation's brief lists the files of a merge:\n%s", brief)
	}
}

// A consolidation that left no tree out, collided nowhere, and found no instruction file in the design repository says
// none of those, and asks for no merge of instructions there are none of.
func TestAnAdoptBriefOfAConsolidationWithNothingToSettleSaysNoneOfIt(t *testing.T) {
	bare := aConsolidated()
	bare.Manifest = ""
	bare.Consolidated.Collisions, bare.Consolidated.LeftOut, bare.Consolidated.Instructions = nil, nil, nil
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true, Restructured: bare})
	if !strings.Contains(brief, "c0ffee1234567890abcdef1234567890abcdef12") {
		t.Errorf("the brief names no second parent:\n%s", brief)
	}
	for _, unwanted := range []string{"Both repositories held these paths", "left out of the merge", "keeping both sets of rules", "repositories:shop"} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("a consolidation with nothing to settle holds %q:\n%s", unwanted, brief)
		}
	}
}

// An adopt lane that was not restructured is told nothing of it.
func TestAnAdoptBriefThatWasNotRestructuredSaysNothingOfIt(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true})
	for _, unwanted := range []string{"restructured this repository", planning.WorkspaceCommitSubject} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("an adopt brief with no restructuring holds %q", unwanted)
		}
	}
}

// Planning work that does not adopt is told nothing of adoption.
func TestAPlanningBriefThatDoesNotAdoptSaysNothingOfIt(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan})
	for _, unwanted := range []string{InventoryFile, "Onboard, do not rewrite", planning.OnboardedExample.After, planning.OnboardedDecisionExample.After} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("a planning brief that does not adopt holds %q", unwanted)
		}
	}
}

// FR-PLAN-15: a planning lane, and an adopt lane with it, finds each repository's files that no commit should hold,
// lists them in the plan, and plans their .gitignore patterns. A change is told none of it.
func TestAPlanningBriefPlansEachRepositorysIgnoredFiles(t *testing.T) {
	for _, adopt := range []bool{false, true} {
		brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan, Adopt: adopt})
		for _, want := range []string{
			"no commit should hold", "`.atl/`", "`.claude/settings.local.json`", "`.idea/`", "`.vscode/`", "`node_modules/`",
			"`dist/`", "`.env*`", "Files no commit holds", ".gitignore",
		} {
			if !strings.Contains(brief, want) {
				t.Errorf("a planning brief (adopt %v) leaves out %q", adopt, want)
			}
		}
		if adopt && !strings.Contains(brief, "hold in each repository too") {
			t.Error("an adopt brief does not plan each repository's ignored files")
		}
	}
	if change := Brief(BriefSpec{WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange}); strings.Contains(change, "no commit should hold") {
		t.Error("a change's brief plans ignored files")
	}
}
