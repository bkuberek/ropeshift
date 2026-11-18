package cli

import (
	"cmp"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
)

// writeAdoption prints an adopted plan as its approval groups it (ADR-0023): every claim of what the code
// already does with its evidence, each in-flight branch the plan continues, what it adds, what it leaves out
// with each reason, and every file the adoption branch changed. Approving is the member's word that the claims
// hold, so each one is a line of its own. A group with nothing in it says so.
func writeAdoption(out *strings.Builder, adoption planning.Adoption, indent string) {
	group := func(title string, lines []string) {
		fmt.Fprintf(out, "%s%s:\n", indent, title)
		if len(lines) == 0 {
			lines = []string{"nothing"}
		}
		for _, line := range lines {
			fmt.Fprintf(out, "%s  %s\n", indent, line)
		}
	}
	var lines []string
	for _, record := range adoption.Exists {
		line := record.Ref + "\t" + record.Title
		if len(record.Evidence) != 0 {
			line += "\tevidence " + strings.Join(record.Evidence, ", ")
		}
		if len(record.Cites) != 0 {
			line += "\trests on " + strings.Join(record.Cites, ", ")
		}
		lines = append(lines, line)
	}
	group("what exists, by the plan's claim", lines)
	lines = nil
	for _, work := range adoption.Continues {
		line := fmt.Sprintf("%s\t%s\t%s\tcontinues %s", work.Contract, work.WorkOrder, work.Title, work.Branch)
		if work.Repo != "" {
			line += " in " + work.Repo
		}
		lines = append(lines, line)
	}
	group("what continues", lines)
	lines = nil
	for _, record := range adoption.New {
		line := record.Ref + "\t" + record.Title
		if len(record.WorkOrders) != 0 {
			line += "\twork orders " + strings.Join(record.WorkOrders, ", ")
		}
		lines = append(lines, line)
	}
	group("what is new", lines)
	lines = nil
	for _, left := range adoption.LeftOut {
		lines = append(lines, left.Ref+"\t"+left.Reason)
	}
	group("what is left out", lines)
	if adoption.ChangesUnread != "" {
		fmt.Fprintf(out, "%sthe adoption branch's changes could not be read: %s\n", indent, adoption.ChangesUnread)
	} else {
		lines = nil
		for _, change := range adoption.Changes {
			path := change.Path
			if change.From != "" {
				path = change.From + " -> " + change.Path
			}
			lines = append(lines, change.Status+"\t"+path)
		}
		group("the adoption branch changes "+api.Count(int64(len(adoption.Changes)), "file", "files"), lines)
	}
	if adoption.Consolidation != nil {
		writeConsolidation(out, *adoption.Consolidation, indent, group)
	}
}

// keptWords says which version of a path both repositories held a consolidation kept.
var keptWords = map[string]string{planning.KeptWorkspace: "the workspace's version", planning.KeptUnion: "the lines of both"}

// writeConsolidation prints what approving a consolidating adoption does beyond the plan (FR-ADOPT-13): the merge
// that imported the design repository's history, every path both repositories held with the version kept, the
// trees left out, the old design repository approving removes, and at person_merges that only a merge commit
// keeps the history.
func writeConsolidation(out *strings.Builder, c planning.Consolidation, indent string, group func(string, []string)) {
	if c.Unread != "" {
		fmt.Fprintf(out, "%sthe adoption imports the history of %s, and its merge could not be read: %s\n", indent, c.Design, c.Unread)
	} else {
		fmt.Fprintf(out, "%sthe adoption imports the history of %s in merge commit %s, whose second parent %s is the design repository's tip\n", indent, c.Design, c.Merge, c.DesignTip)
		var lines []string
		for _, collision := range c.Collisions {
			lines = append(lines, collision.Path+"\t"+keptWords[collision.Kept])
		}
		if c.CollisionsTruncated {
			lines = append(lines, "and more, which this list leaves out")
		}
		group("the paths both repositories held, with the version kept", lines)
	}
	group("the design repository's code trees left out", c.LeftOut)
	if c.Removes != nil {
		fmt.Fprintf(out, "%sapproving removes repository %s from the project, and keeps its record as history\n", indent, cmp.Or(c.Removes.Name, c.Removes.ID))
	}
	if c.RemovalUnread != "" {
		fmt.Fprintf(out, "%sapproving removes no repository: %s\n", indent, c.RemovalUnread)
	}
	if c.PersonMerges {
		fmt.Fprintf(out, "%sat person_merges you merge this branch yourself: merge it with a merge commit. %s\n", indent, planning.MergeCommitKeepsHistory)
	}
}
