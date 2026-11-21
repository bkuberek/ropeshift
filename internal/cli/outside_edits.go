package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
)

// repositoryEdits prints each clone's paths that intake and lanes cannot read, with the step each needs and the exact
// commands of each step (FR-UI-14).
func (c *invocation) repositoryEdits(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	list, err := client.OutsideEdits(c.ctx)
	return c.finish(err, list, func() string { return outsideEditsText(list) })
}

// outsideEditsText is one block for each clone: a line that names it, a line for each path, and each step with its
// commands, one command line to a line.
func outsideEditsText(list api.OutsideEdits) string {
	var out strings.Builder
	for _, clone := range list.Clones {
		name := plainLine(clone.Name)
		if clone.Problem != "" {
			fmt.Fprintf(&out, "%s\t-\t%s\n", name, plainLine(clone.Problem))
			continue
		}
		fmt.Fprintf(&out, "%s\t%s\tbranch %s\tremote %s\n", name, plainPath(clone.Path), dashIfEmpty(plainLine(clone.Branch)), dashIfEmpty(plainLine(clone.Remote)))
		if len(clone.Edits) == 0 {
			out.WriteString("\tnothing that intake and lanes cannot read\n")
		}
		for _, edit := range clone.Edits {
			steps := strings.Join(edit.Steps, ", ")
			if edit.Hidden {
				steps = "no command names it, because it holds a character that is not seen or a backslash: commit it from a terminal in the clone"
			}
			fmt.Fprintf(&out, "\t%s\t%s\t%s\n", edit.State, plainPath(edit.Path), steps)
		}
		if clone.Cut {
			fmt.Fprintf(&out, "\tthese are the first %d paths git listed, and it listed more; the steps take only these\n", list.Limit)
		}
		for _, step := range clone.Steps {
			fmt.Fprintf(&out, "step\t%s\t%s\n\t%s\n", step.Kind, plainLine(step.Title), plainLine(step.Detail))
			for _, command := range step.Commands {
				fmt.Fprintf(&out, "\t%s\n", command)
			}
		}
	}
	return out.String()
}
