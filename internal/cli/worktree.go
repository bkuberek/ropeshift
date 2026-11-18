package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
)

// worktreeText states the doctor's worktree section the way a person reads it: the root in force with the layer
// that set it, then a line for each checkout whose root lies inside a clone, with how to fix it.
func worktreeText(report lanes.WorktreeReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Worktree root: %s (%s)\n", report.Root, report.Source)
	for _, inside := range report.Inside {
		fmt.Fprintf(&out, "  %s %s: the root resolves to %s, inside the clone %s. A lane there fails with worktree_inside_clone: %s\n",
			inside.Project, inside.Checkout, inside.Root, inside.Clone, lanes.RootFix(report.Source))
	}
	for _, workspace := range report.Workspaces {
		out.WriteString(workspaceText(workspace))
	}
	return out.String()
}

// workspaceText states one project's workspace: where it is and how it was found, whether its folder for lane
// worktrees is ignored, with the line to add when it is not, and the root each checkout gets.
func workspaceText(workspace lanes.WorkspaceReport) string {
	var out strings.Builder
	if workspace.Error != "" {
		return fmt.Sprintf("Workspace %s: could not be checked: %s. Its lanes on the built-in root run outside every clone until it can be\n", workspace.Project, workspace.Error)
	}
	fmt.Fprintf(&out, "Workspace %s: %s (%s)", workspace.Project, workspace.Path, workspace.Found)
	switch {
	case !workspace.Clone:
		out.WriteString(", a plain folder that needs no ignore line")
	case workspace.Ignored:
		out.WriteString(", which ignores .ropeshift/worktrees/")
	default:
		fmt.Fprintf(&out, ", which does not ignore .ropeshift/worktrees/: add %s to its .gitignore so lanes of its clones run inside it", workspace.Line)
	}
	out.WriteString("\n")
	for _, root := range workspace.Roots {
		fmt.Fprintf(&out, "  %s: lane worktrees go in %s\n", root.Checkout, root.Root)
		// An unignored folder is already said for the whole workspace, with its line to add.
		if root.Fallback != "" && root.Fallback != lanes.FallbackUnignored {
			fmt.Fprintf(&out, "    %s\n", lanes.RootFallback{Workspace: workspace.Path, Reason: root.Fallback}.Notice())
		}
	}
	return out.String()
}
