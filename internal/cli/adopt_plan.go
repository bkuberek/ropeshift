package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// shapeSummary says what a shape is, and what --restructure does in it.
func shapeSummary(shape planning.LayoutShape) string {
	switch shape {
	case planning.ShapeSingle:
		return "one repository; --restructure adds the instruction files it lacks"
	case planning.ShapePlanned:
		return "the repository holds plan content and clones were found; --restructure adds the manifest of the clones when none exists, and the files the layout lacks"
	case planning.ShapeConsolidate:
		return "a workspace whose ignored folder holds the design repository; --restructure imports its history into the workspace, and adds the files the merged tree lacks"
	case planning.ShapeWorkspaceFiles:
		return "clones with no design repository to import; --restructure adds the manifest, the ignore file, a Makefile, and the instruction files the layout lacks, and lists the moves"
	}
	return ""
}

// adoptionPlanText prints the plan of an adoption, one line to a fact: the layout detected, the shape, the
// repositories, and every change and move.
func adoptionPlanText(plan planning.AdoptionPlan) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Layout\t%s\n", plan.Layout.Root)
	for _, clone := range plan.Layout.Clones {
		var held []string
		if clone.Plan {
			held = append(held, "plan content")
		}
		held = append(held, fmt.Sprintf("%d Markdown documents", clone.Documents))
		if clone.Instructions {
			held = append(held, "instruction file")
		}
		fmt.Fprintf(&out, "Clone\t%s\t%s\t%s\t%s\n", clone.Name, clone.Path, foundWhere(clone.Where), strings.Join(held, ", "))
	}
	fmt.Fprintf(&out, "Shape\t%s\t%s\n", plan.Shape, shapeSummary(plan.Shape))
	if plan.Planning.Undecided {
		fmt.Fprintf(&out, "Planning repository\tundecided\t%s\n", plan.Planning.Why)
	} else {
		fmt.Fprintf(&out, "Planning repository\t%s\t%s\n", plan.Planning.Name, plan.Planning.Why)
	}
	if plan.Design != nil {
		fmt.Fprintf(&out, "Design repository\t%s\t%s\n", plan.Design.Name, plan.Design.Why)
	}
	for _, repository := range plan.Repositories {
		word := map[string]string{
			string(ledger.Registered): "would register", string(ledger.AlreadyRegistered): "already registered",
			string(ledger.RegistrationRefused): "refused", planning.OutcomeNotRegistered: "not registered",
		}[repository.Outcome]
		if word == "" {
			word = repository.Outcome
		}
		if repository.Outcome == string(ledger.RegistrationRefused) {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", word, repository.Path, repository.Reason)
			continue
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s", word, repository.Name, shortRepository(repository.ID), repository.Path)
		if repository.Reason != "" {
			fmt.Fprintf(&out, "\t(%s)", repository.Reason)
		}
		out.WriteString("\n")
	}
	var lacking []planning.PlannedFile
	for _, file := range plan.Files {
		if file.Template != "" {
			lacking = append(lacking, file)
			continue
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\n", file.Action, file.Path, file.Detail)
	}
	if len(lacking) != 0 {
		out.WriteString("Files the layout lacks\n")
		for _, file := range lacking {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", file.Action, file.Path, file.Detail)
		}
	}
	for _, path := range plan.Takes {
		fmt.Fprintf(&out, "take\t%s\n", path)
	}
	for _, collision := range plan.Collisions {
		fmt.Fprintf(&out, "collision\t%s\t%s\n", collision.Path, collision.Kept)
	}
	for _, tree := range plan.CodeTrees {
		fmt.Fprintf(&out, "code tree\t%s\t%s", tree.Path, tree.BuildFile)
		switch {
		case tree.LeftOut:
			out.WriteString("\tleft out")
		case tree.Whole:
			out.WriteString("\tthe whole repository, which cannot be left out")
		}
		out.WriteString("\n")
	}
	if len(plan.MakefileLeftOut) != 0 {
		out.WriteString("Left out of the Makefile\n")
		for _, left := range plan.MakefileLeftOut {
			fmt.Fprintf(&out, "left out\t%s\t%s\t%s\n", left.Name, left.Path, left.Reason)
		}
	}
	for _, move := range plan.Moves {
		fmt.Fprintf(&out, "move\t%s\t%s\t%s\n", move.Name, move.From, move.To)
		for _, command := range move.Commands {
			fmt.Fprintf(&out, "run\t%s\n", command)
		}
	}
	if len(plan.Steps) != 0 {
		out.WriteString("Steps for you\n")
		for _, step := range plan.Steps {
			fmt.Fprintf(&out, "step\t%s\t%s\n", step.Name, step.Path)
			for _, command := range step.Commands {
				fmt.Fprintf(&out, "run\t%s\n", command)
			}
		}
	}
	for _, note := range plan.Notes {
		fmt.Fprintf(&out, "note\t%s\n", note)
	}
	fmt.Fprintf(&out, "Digest\t%s\n", plan.Digest)
	if plan.Offers() {
		fmt.Fprintf(&out, "Run '%s adopt --restructure' to restructure as listed, or '%s adopt' to adopt without restructuring. A dry run records nothing.\n", program, program)
	} else {
		fmt.Fprintf(&out, "A dry run records nothing.\n")
	}
	return out.String()
}

// foundWhere says how the detection found a clone.
func foundWhere(where string) string {
	switch where {
	case planning.WhereHere:
		return "where adopt runs"
	case planning.WhereIgnored:
		return "in a folder the clone ignores"
	}
	return "named by a path"
}
