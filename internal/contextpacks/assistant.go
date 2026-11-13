package contextpacks

import (
	"fmt"
	"slices"
	"strings"
)

// AssistantRole is a member's assistant's role (ADR-0024). It names only the member and the project, so it
// is the same bytes for every generation of a conversation, and each reads it from cache.
func AssistantRole(member, project string) string {
	return fmt.Sprintf(`You are the assistant of %[1]s in the ropeshift project %[2]s.

You act for %[1]s in %[2]s, and only by running `+"`ropeshift`"+` commands in a shell, which act with your own
credential as their delegate. You read and search files, and the repositories of the project are checked out
for you to read; you change no file and run no other command.

A command that needs %[1]s's consent runs nothing: it exits 10 and prepares a card that %[1]s confirms.
Pass `+"`--why`"+` with the reason for every such command, and tell %[1]s that a card waits and what it does.
Never say a card was confirmed, or that its operation ran, unless its state says so: read it with
`+"`ropeshift approval show <id>`"+`.

Cite every record you rely on as [[<kind>:<id>]], such as [[work_order:WO-12]] or [[decision:D-4]], and every
line of code as [[code:<repository>:<path>#L<line>@<commit>]]. Say plainly when you cannot ground an answer in a
record or in the code.

You have no `+"`ask`"+` tool: when you need %[1]s to choose, say so in your reply. Read what the ledger holds with
`+"`ropeshift`"+` and the context tools, which hold more than this prompt.
`, member, project)
}

// AssistantRepository is one repository an assistant's generation reads: its base branch, and the read-only
// tree it reads it in, or why it has none.
type AssistantRepository struct {
	Name   string
	Base   string
	Path   string
	Detail string
}

// AssistantHistory is one entry of the conversation a fresh generation's first prompt states.
type AssistantHistory struct {
	Kind string
	Body string
}

// AssistantFacts are what an assistant's first prompt states of its project.
type AssistantFacts struct {
	ProjectName  string
	Repositories []AssistantRepository
	Plans        []string
	Work         map[string]int64
	// History is the conversation's newest entries, oldest first, for a session that starts with no
	// context of its own.
	History []AssistantHistory
}

// AssistantBrief is an assistant generation's first prompt: the project's name, its repositories with their
// read-only trees and base branches, its open plans, and how much work stands in each state, then the
// conversation so far when the session has none of its own. Detail comes from the context tools and
// `ropeshift` reads.
func AssistantBrief(facts AssistantFacts) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Project: %s\n\nRepositories, each read-only at its base branch:\n", facts.ProjectName)
	if len(facts.Repositories) == 0 {
		out.WriteString("- none\n")
	}
	for _, repository := range facts.Repositories {
		where := repository.Path
		if where == "" {
			where = "not checked out: " + repository.Detail
		}
		fmt.Fprintf(&out, "- %s (base %s): %s\n", repository.Name, repository.Base, where)
	}
	out.WriteString("\nOpen plans:\n")
	if len(facts.Plans) == 0 {
		out.WriteString("- none\n")
	}
	for _, plan := range facts.Plans {
		fmt.Fprintf(&out, "- %s\n", plan)
	}
	out.WriteString("\nWork by state:")
	states := make([]string, 0, len(facts.Work))
	for state := range facts.Work {
		states = append(states, state)
	}
	slices.Sort(states)
	if len(states) == 0 {
		out.WriteString(" none")
	}
	for _, state := range states {
		fmt.Fprintf(&out, " %s %d;", state, facts.Work[state])
	}
	out.WriteString("\n")
	if len(facts.History) != 0 {
		out.WriteString("\nThe earlier conversation, oldest first, which this session starts without:\n")
		for _, entry := range facts.History {
			fmt.Fprintf(&out, "[%s] %s\n", entry.Kind, entry.Body)
		}
	}
	out.WriteString("\nWait for the member's next message.\n")
	return out.String()
}
