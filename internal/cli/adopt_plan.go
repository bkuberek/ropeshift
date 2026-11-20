package cli

import (
	"cmp"
	"fmt"
	"path"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
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
	fmt.Fprintf(&out, "Layout\t%s\n", plainPath(plan.Layout.Root))
	for _, clone := range plan.Layout.Clones {
		var held []string
		if clone.Plan {
			held = append(held, "plan content")
		}
		held = append(held, fmt.Sprintf("%d Markdown documents", clone.Documents))
		if clone.Instructions {
			held = append(held, "instruction file")
		}
		fmt.Fprintf(&out, "Clone\t%s\t%s\t%s\t%s\n", plainLine(clone.Name), plainPath(clone.Path), foundWhere(clone.Where), strings.Join(held, ", "))
	}
	fmt.Fprintf(&out, "Shape\t%s\t%s\n", plan.Shape, shapeSummary(plan.Shape))
	if plan.Planning.Undecided {
		fmt.Fprintf(&out, "Planning repository\tundecided\t%s\n", plainLine(plan.Planning.Why))
	} else {
		fmt.Fprintf(&out, "Planning repository\t%s\t%s\n", plainLine(plan.Planning.Name), plainLine(plan.Planning.Why))
	}
	if plan.Design != nil {
		fmt.Fprintf(&out, "Design repository\t%s\t%s\n", plainLine(plan.Design.Name), plainLine(plan.Design.Why))
	}
	for _, repository := range plan.Repositories {
		word := map[string]string{
			string(ledger.Registered): "would register", string(ledger.AlreadyRegistered): "already registered",
			string(ledger.RegistrationRefused): "refused", planning.OutcomeNotRegistered: "not registered",
		}[repository.Outcome]
		if word == "" {
			word = plainLine(repository.Outcome)
		}
		if repository.Outcome == string(ledger.RegistrationRefused) {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", word, plainPath(repository.Path), plainLine(repository.Reason))
			continue
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s", word, plainLine(repository.Name), shortRepository(plainLine(repository.ID)), plainPath(repository.Path))
		if repository.Reason != "" {
			fmt.Fprintf(&out, "\t(%s)", plainLine(repository.Reason))
		}
		out.WriteString("\n")
		out.WriteString(plannedPractices(repository))
	}
	var lacking []planning.PlannedFile
	for _, file := range plan.Files {
		if file.Template != "" {
			lacking = append(lacking, file)
			continue
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\n", file.Action, plainPath(file.Path), plainLine(file.Detail))
	}
	if len(lacking) != 0 {
		out.WriteString("Files the layout lacks\n")
		for _, file := range lacking {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", file.Action, plainPath(file.Path), plainLine(file.Detail))
		}
	}
	for _, path := range plan.Takes {
		fmt.Fprintf(&out, "take\t%s\n", plainPath(path))
	}
	for _, collision := range plan.Collisions {
		fmt.Fprintf(&out, "collision\t%s\t%s", plainPath(collision.Path), plainLine(collision.Kept))
		if collision.DesignPath != "" {
			fmt.Fprintf(&out, "\tthe design repository spells it %s", plainPath(collision.DesignPath))
		}
		out.WriteString("\n")
	}
	for _, tree := range plan.CodeTrees {
		fmt.Fprintf(&out, "code tree\t%s\t%s", plainPath(tree.Path), plainPath(tree.BuildFile))
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
			fmt.Fprintf(&out, "left out\t%s\t%s\t%s\n", plainLine(left.Name), plainPath(left.Path), plainLine(left.Reason))
		}
	}
	for _, move := range plan.Moves {
		fmt.Fprintf(&out, "move\t%s\t%s\t%s\n", plainLine(move.Name), plainPath(move.From), plainPath(move.To))
		for _, command := range move.Commands {
			fmt.Fprintf(&out, "run\t%s\n", commandText(command))
		}
	}
	if len(plan.Steps) != 0 {
		out.WriteString("Steps for you\n")
		for _, step := range plan.Steps {
			fmt.Fprintf(&out, "step\t%s\t%s\n", plainLine(step.Name), plainPath(step.Path))
			for _, command := range step.Commands {
				fmt.Fprintf(&out, "run\t%s\n", commandText(command))
			}
		}
	}
	for _, note := range plan.Notes {
		fmt.Fprintf(&out, "note\t%s\n", plainLine(note))
	}
	fmt.Fprintf(&out, "Digest\t%s\n", plan.Digest)
	if plan.Offers() {
		fmt.Fprintf(&out, "Run '%s adopt --restructure' to restructure as listed, or '%s adopt' to adopt without restructuring. A dry run records nothing.\n", program, program)
	} else {
		fmt.Fprintf(&out, "A dry run records nothing.\n")
	}
	return out.String()
}

// plainLine is text a tool or the API sent, made safe to print as one field of one line: every run of white space,
// which holds each kind of line break, is one space, and plainText drops the control characters.
func plainLine(text string) string { return ledger.PlainText(oneLine(text)) }

// plainPath is a path made safe to print as one field of one line: a line break is a space, a control or format
// character is dropped, and every other character, a space among them, is kept, so a path prints as it is.
func plainPath(text string) string {
	text = lineBreaks.Replace(text)
	return strings.ReplaceAll(ledger.PlainText(text), "\t", "")
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\u2028", " ", "\u2029", " ")

// commandText is a command for a person to copy and run, exactly as it is when every character of it can be shown.
// A command with a control or format character, a tab or a line break among them, would show as another command, so a
// line says it cannot be shown and where it is exact: the plan's JSON.
func commandText(command string) string {
	if strings.IndexFunc(command, ledger.HiddenText) < 0 {
		return command
	}
	return fmt.Sprintf("(this command holds characters that cannot be shown; run '%s adopt --dry-run --json' with the same arguments to read it exactly)", program)
}

// plannedPractices is the block under a repository that the dry run read the practices of: `ropeshift practices`'s
// text for the result, with no acknowledgement, or the note that says why the check could not read the clone. A
// repository the dry run did not ask about has none.
func plannedPractices(repository planning.PlannedRepository) string {
	switch {
	case repository.Practices != nil:
		view := api.PracticesView{Repository: plainLine(repository.ID), Name: cmp.Or(plainLine(repository.Name), plainPath(path.Base(repository.Path)))}
		result := api.PracticesResultViewOf("", *repository.Practices)
		view.Result = &result
		var out strings.Builder
		out.WriteString("Practices\n")
		for _, line := range strings.SplitAfter(practicesText(view, "", time.Time{}), "\n") {
			if line != "" {
				out.WriteString("  " + line)
			}
		}
		return out.String()
	case repository.PracticesNote != "":
		return "Practices\t" + plainLine(repository.PracticesNote) + "\n"
	}
	return ""
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
