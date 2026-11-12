package main

import (
	"encoding/json"
	"slices"
	"strings"
)

// agentDefinition is one value of `--agents`, with the fields the CLI documents for an agent defined on
// its command line (https://code.claude.com/docs/en/sub-agents). The double reads the ones ropeshift
// renders, and refuses what the CLI refuses at startup.
type agentDefinition struct {
	Description     string   `json:"description"`
	Prompt          string   `json:"prompt"`
	Tools           []string `json:"tools"`
	DisallowedTools []string `json:"disallowedTools"`
	Model           string   `json:"model"`
	Effort          string   `json:"effort"`
}

// cliAgentFields are the fields the CLI takes in a definition. It ignores `color` and `experimental`
// rather than refusing them.
var cliAgentFields = []string{
	"description", "prompt", "tools", "disallowedTools", "model", "permissionMode", "mcpServers", "hooks", "maxTurns", "autoCompactWindow",
	"skills", "initialPrompt", "memory", "effort", "background", "omitClaudeMd", "isolation", "color", "experimental",
}

// readAgents reads `--agents` as the CLI validates it, and ends the process on a value it would refuse:
// an object whose keys are names that do not start with `-` and hold no `:`, and whose values are
// definitions with known fields.
func readAgents(value string) map[string]agentDefinition {
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		fail("Invalid --agents configuration: %v", err)
	}
	out := map[string]agentDefinition{}
	for name, fields := range raw {
		if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, ":") || len(name) > 256 {
			fail("Invalid --agents configuration: agent name %q", name)
		}
		for field := range fields {
			if !slices.Contains(cliAgentFields, field) {
				fail("Invalid --agents configuration: agent %s has unknown field %s", name, field)
			}
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			fail("%v", err)
		}
		var definition agentDefinition
		if err := json.Unmarshal(encoded, &definition); err != nil {
			fail("Invalid --agents configuration: agent %s: %v", name, err)
		}
		if strings.TrimSpace(definition.Description) == "" {
			fail("Invalid --agents configuration: agent %s has no description", name)
		}
		out[name] = definition
	}
	return out
}

// holds says the agent may use a tool: one its tools list, when it lists any, and its disallowed tools do
// not name. An MCP tool goes by its full name.
func (d agentDefinition) holds(tool string) bool {
	if slices.Contains(d.DisallowedTools, tool) {
		return false
	}
	return len(d.Tools) == 0 || slices.Contains(d.Tools, tool)
}
