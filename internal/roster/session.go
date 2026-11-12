package roster

import (
	"fmt"
	"slices"
	"strings"
)

// Session is the kind of session that receives agents.
type Session string

// The sessions of ADR-0025's table.
const (
	// SessionWriting is a change, rework, planning, or adopt lane.
	SessionWriting Session = "writing"
	// SessionReview is a review lane, which has no edit or commit rule.
	SessionReview Session = "review"
	// SessionAssistant is the assistant, which acts only by running the `ropeshift` CLI itself.
	SessionAssistant Session = "assistant"
	// SessionOrchestrator is the orchestrator, which starts with no vendor tools and receives no agents.
	SessionOrchestrator Session = "orchestrator"
)

// What a driver says of subagents (FR-AGENT-8).
const (
	SubagentsNative       = "native"
	SubagentsNone         = "none"
	ToolsEnforced         = "enforced"
	ToolsInstructionsOnly = "instructions_only"
)

// Support is what a driver declares of subagents: whether its CLI has them, and whether it enforces
// their tools.
type Support struct {
	Subagents     string
	SubagentTools string
}

// Fitted is one agent as a session receives it: the categories its session's rules let it hold, and
// the categories it is denied, which together cover every category.
type Fitted struct {
	Agent
	Tools  []string `json:"tools"`
	Denied []string `json:"denied"`
}

// Permitted lists the categories a session's generated rules let an agent hold. No session lets an
// agent start another, so the coordinating session is the only one that routes.
func Permitted(s Session) []string {
	switch s {
	case SessionWriting:
		return []string{ToolRead, ToolSearch, ToolEdit, ToolShell, ToolWeb, ToolContext}
	case SessionReview:
		return []string{ToolRead, ToolSearch, ToolShell, ToolWeb, ToolContext}
	case SessionAssistant:
		return []string{ToolRead, ToolSearch, ToolWeb, ToolContext}
	}
	return nil
}

// ForSession picks the agents a session receives and fits each inside its rules (FR-AGENT-9,
// FR-AGENT-10). An agent's tools are its allowed categories, less its denied ones, intersected with
// what the session permits. A read-only agent never holds edit, and in a writing session it holds no
// shell, because a driver cannot hold its commands to the reads a writing lane's rules allow. An agent
// left with no tool is left out, because a driver may read an empty list as every tool.
func (r Resolved) ForSession(s Session, support Support) []Fitted {
	if support.Subagents != SubagentsNative {
		return nil
	}
	permitted := Permitted(s)
	var out []Fitted
	for _, agent := range r.Agents {
		readOnly := agent.Access == AccessReadOnly
		switch {
		case !agent.Enabled, len(permitted) == 0:
			continue
		case (s == SessionReview || s == SessionAssistant) && !readOnly:
			continue
		case s == SessionWriting && readOnly && support.SubagentTools != ToolsEnforced:
			continue
		}
		var tools []string
		for _, tool := range ToolCategories {
			switch {
			case !slices.Contains(agent.Tools.Allow, tool), slices.Contains(agent.Tools.Deny, tool), !slices.Contains(permitted, tool):
			case readOnly && tool == ToolEdit:
			case readOnly && s == SessionWriting && tool == ToolShell:
			default:
				tools = append(tools, tool)
			}
		}
		if len(tools) == 0 {
			continue
		}
		denied := slices.DeleteFunc(slices.Clone(ToolCategories), func(tool string) bool { return slices.Contains(tools, tool) })
		out = append(out, Fitted{Agent: clone(agent.Agent), Tools: tools, Denied: denied})
	}
	return out
}

// Text is the roster's item of a session's context pack: the routing guide and the catalog of agents it
// may start, and, for a lane that runs as an agent, that agent's instructions and report format. A
// session whose CLI has no subagents is told to do the work itself.
func Text(guide string, agents []Fitted, delegates bool, lane *Agent) string {
	var b strings.Builder
	if lane != nil {
		fmt.Fprintf(&b, "## Your agent\n\nYou run as the roster agent %s.\n\n%s\n\nReport to your coordinator in this format: %s\n\n", lane.Name, strings.TrimSpace(lane.Instructions), strings.TrimSpace(lane.Report))
	}
	if len(agents) == 0 {
		return strings.TrimRight(b.String(), "\n")
	}
	b.WriteString("## Routing guide\n\n")
	b.WriteString(strings.TrimSpace(guide))
	b.WriteString("\n\n## Agents you may start\n\n")
	if !delegates {
		b.WriteString("Your agent CLI cannot start these agents. Use the guide to do the work yourself, as the agent it names would.\n\n")
	}
	for _, agent := range agents {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", agent.Name, strings.TrimSpace(agent.Purpose))
		fmt.Fprintf(&b, "Class %s, effort %s, tools %s.", agent.Class, agent.Effort, strings.Join(agent.Tools, ", "))
		if agent.Approval == ApprovalPerUse {
			b.WriteString(" It needs a member's approval for each start.")
		}
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
