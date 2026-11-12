package claudecode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

// renderedAgent is one value of the `--agents` JSON object, whose key is the agent's name. The flag
// "Define[s] custom subagents dynamically via JSON", takes "the fields listed for CLI-defined subagents",
// and Claude Code "validates the value at startup and exits on an invalid one"
// (https://code.claude.com/docs/en/cli-reference). A definition takes `prompt`, the agent's system prompt,
// and the frontmatter fields, among them `description`, `tools`, `disallowedTools`, `model`, and `effort`,
// and such agents "exist only for that session and aren't saved to disk"
// (https://code.claude.com/docs/en/sub-agents). The rendering never sets `permissionMode`, `hooks`, or
// `mcpServers`: an unset permission mode inherits the main conversation's, and the lane's settings,
// whose deny rules and hooks apply inside subagents, stay the only source of hooks (ADR-0025).
type renderedAgent struct {
	Description     string   `json:"description"`
	Prompt          string   `json:"prompt"`
	Tools           []string `json:"tools"`
	DisallowedTools []string `json:"disallowedTools"`
	Model           string   `json:"model,omitempty"`
	Effort          string   `json:"effort,omitempty"`
}

// The tool that starts a subagent. Version 2.1.63 renamed it from Task, which stays an alias, and a
// permission rule names one agent as `Agent(<name>)` (https://code.claude.com/docs/en/sub-agents).
const agentTool = "Agent"

// The environment that makes the roster the session's set of agents: the built-in Explore and Plan agents
// give way to it, and no subagent starts another (https://code.claude.com/docs/en/sub-agents).
const (
	envNoExplorePlan = "CLAUDE_CODE_DISABLE_EXPLORE_PLAN_AGENTS=1"
	envSpawnDepth    = "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=1"
)

// contextGroup are ropeshift's context tools, which the `context` category names.
var contextGroup = []string{lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch}

// categoryTools are the CLI's own tools of each category but context, which a session's ropeshift tools
// give.
var categoryTools = map[string][]string{
	roster.ToolRead:     {"Read"},
	roster.ToolSearch:   {"Grep", "Glob"},
	roster.ToolEdit:     lanes.ReadOnlyTools(),
	roster.ToolShell:    {lanes.ToolBash},
	roster.ToolWeb:      {"WebFetch", "WebSearch"},
	roster.ToolDelegate: {agentTool},
}

// vendorTools are every CLI tool a category names.
func vendorTools() []string {
	var out []string
	for _, category := range roster.ToolCategories {
		out = append(out, categoryTools[category]...)
	}
	return out
}

// contextTools are the context tools this session holds, as rules name them.
func contextTools(spec ports.SessionSpec) []string {
	var out []string
	for _, tool := range contextGroup {
		if slices.Contains(spec.Tools, tool) {
			out = append(out, toolRule(tool))
		}
	}
	return out
}

// toolsOf are the CLI's tools of some categories, in this session.
func toolsOf(spec ports.SessionSpec, categories []string) []string {
	var out []string
	for _, category := range categories {
		if category == roster.ToolContext {
			out = append(out, contextTools(spec)...)
			continue
		}
		out = append(out, categoryTools[category]...)
	}
	return out
}

// renderAgents is the session's agents as the flag's object. An agent whose categories leave it no tool
// in this session is left out, because an empty list would read as every tool. Starting another agent is
// always disallowed, whatever an agent's categories say (FR-AGENT-10).
func renderAgents(spec ports.SessionSpec) map[string]renderedAgent {
	if spec.OnlyTools || len(spec.Agents) == 0 {
		return nil
	}
	out := map[string]renderedAgent{}
	laneDenied := laneDenyRules(spec)
	for _, agent := range spec.Agents {
		tools := slices.DeleteFunc(toolsOf(spec, agent.Tools), func(tool string) bool { return tool == agentTool || slices.Contains(laneDenied, tool) })
		if len(tools) == 0 {
			continue
		}
		disallowed := toolsOf(spec, agent.Denied)
		for _, tool := range append(laneDenied, agentTool) {
			if !slices.Contains(disallowed, tool) {
				disallowed = append(disallowed, tool)
			}
		}
		out[agent.Name] = renderedAgent{
			Description: strings.TrimSpace(agent.Purpose), Prompt: agentPrompt(agent.Agent),
			Tools: tools, DisallowedTools: disallowed, Model: agent.Model, Effort: agent.Effort,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// agentPrompt is an agent's instructions and the format it reports in.
func agentPrompt(agent roster.Agent) string {
	prompt := strings.TrimSpace(agent.Instructions)
	if report := strings.TrimSpace(agent.Report); report != "" {
		prompt += "\n\nReport to your coordinator in this format: " + report
	}
	return prompt
}

// agentArgs are the flags that carry the session's agents and its effort, which every start and every
// resume states, in both run modes, as every start flag is (ADR-0025).
func agentArgs(spec ports.SessionSpec) []string {
	var out []string
	if rendered := renderAgents(spec); rendered != nil {
		raw, err := json.Marshal(rendered)
		if err == nil {
			out = append(out, "--agents", string(raw))
		}
	}
	if spec.Effort != "" {
		out = append(out, "--effort", spec.Effort)
	}
	return out
}

// agentEnv turns the CLI's own Explore and Plan agents and nested agents off for a session that receives
// the roster.
func agentEnv(spec ports.SessionSpec) []string {
	if renderAgents(spec) == nil {
		return nil
	}
	return []string{envNoExplorePlan, envSpawnDepth}
}

// perUseRules ask before each start of an agent whose approval is per use, in every mode, through the
// PermissionRequest hook every other request takes (FR-AGENT-11). Whether an ask rule in this form raises
// a request is a qualified surface (ADR-0025).
func perUseRules(spec ports.SessionSpec) []string {
	var out []string
	for name := range renderAgents(spec) {
		if slices.ContainsFunc(spec.Agents, func(a ports.SessionAgent) bool { return a.Name == name && a.Approval == roster.ApprovalPerUse }) {
			out = append(out, agentTool+"("+name+")")
		}
	}
	slices.Sort(out)
	return out
}

// laneDenyRules are what the lane's own agent denies, as deny rules of the lane. Delegate stays, because
// the lane's session is the one that routes, and the deny is about the agent as a subagent of another.
func laneDenyRules(spec ports.SessionSpec) []string {
	categories := slices.DeleteFunc(slices.Clone(spec.Deny), func(category string) bool { return category == roster.ToolDelegate })
	return toolsOf(spec, categories)
}

// maxAgentName bounds a name as the CLI does.
const maxAgentName = 256

// agentProblems are what the CLI would refuse of the rendering, or what would let an agent reach past its
// session's rules, so the preflight stops the session before it starts.
func agentProblems(spec ports.SessionSpec) []string {
	var out []string
	denied := denyRules(spec)
	held := slices.Concat(vendorTools(), contextTools(spec))
	for name, agent := range renderAgents(spec) {
		if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, ":") || len(name) > maxAgentName {
			out = append(out, fmt.Sprintf("agent name %q is not one the CLI takes", name))
		}
		if strings.TrimSpace(agent.Description) == "" {
			out = append(out, fmt.Sprintf("agent %s has no description", name))
		}
		for _, tool := range agent.Tools {
			if slices.Contains(denied, tool) || !slices.Contains(held, tool) {
				out = append(out, fmt.Sprintf("agent %s holds %s, which its session's rules do not allow", name, tool))
			}
		}
	}
	return out
}
