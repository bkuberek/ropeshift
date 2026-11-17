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
	return out.String()
}
