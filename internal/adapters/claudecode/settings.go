package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// ServerName is the MCP server's name, which is part of every tool's name.
const ServerName = "ropeshift"

// hookTimeoutSeconds bounds a hook the CLI runs. The hook holds itself to far less.
const hookTimeoutSeconds = 5

// hookEvents are the events a lane listens to. PermissionRequest joins them with
// approvals.
var hookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification",
	"PreCompact", "PostCompact", "Stop", "StopFailure", "SessionEnd",
}

type hookCommand struct {
	Type      string `json:"type"`
	Command   string `json:"command"`
	Timeout   int    `json:"timeout"`
	OnFailure string `json:"onFailure,omitempty"`
}

type hookGroup struct {
	Hooks []hookCommand `json:"hooks"`
}

type permissions struct {
	Allow []string `json:"allow"`
}

// settingsFile is what a generation's `--settings` holds. It holds no secret, because
// a hook failure returns the command line to the model.
type settingsFile struct {
	Hooks        map[string][]hookGroup `json:"hooks,omitempty"`
	Permissions  permissions            `json:"permissions"`
	APIKeyHelper string                 `json:"apiKeyHelper,omitempty"`
}

// toolRule is the permission rule of one ropeshift MCP tool.
func toolRule(tool string) string { return "mcp__" + ServerName + "__" + tool }

// generateSettings builds a generation's settings. The hook commands use the stable
// path (D18), quoted, because a state directory can hold a space and the CLI runs a
// hook through a shell. PreToolUse fails closed: a hook that times out must block the
// call, or a denial could be skipped by a slow machine.
func generateSettings(spec ports.SessionSpec) settingsFile {
	out := settingsFile{Permissions: permissions{Allow: []string{}}, APIKeyHelper: spec.Credential.APIKeyHelper}
	for _, tool := range spec.Tools {
		out.Permissions.Allow = append(out.Permissions.Allow, toolRule(tool))
	}
	if spec.Binary == "" {
		return out
	}
	out.Hooks = map[string][]hookGroup{}
	for _, event := range hookEvents {
		command := hookCommand{
			Type: "command", Command: lanes.ShellQuote(spec.Binary) + " hook " + event, Timeout: hookTimeoutSeconds,
		}
		if event == "PreToolUse" {
			command.OnFailure = "block"
		}
		out.Hooks[event] = []hookGroup{{Hooks: []hookCommand{command}}}
	}
	return out
}

type mcpServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type mcpFile struct {
	Servers map[string]mcpServer `json:"mcpServers"`
}

// generateMCP builds a generation's `--mcp-config`. The server reads the lane token
// from the environment it inherits, so this file holds none.
func generateMCP(spec ports.SessionSpec) mcpFile {
	out := mcpFile{Servers: map[string]mcpServer{}}
	if spec.Binary != "" {
		out.Servers[ServerName] = mcpServer{Type: "stdio", Command: spec.Binary, Args: []string{"mcp"}}
	}
	return out
}

// generatedPaths names a generation's files.
func generatedPaths(spec ports.SessionSpec) (settings, mcp string) {
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("settings-%d.json", spec.Generation)),
		filepath.Join(spec.ConfigDir, fmt.Sprintf("mcp-%d.json", spec.Generation))
}

// writeGenerated writes a generation's files where only the owner can read them.
func writeGenerated(spec ports.SessionSpec) error {
	if err := os.MkdirAll(spec.ConfigDir, 0o700); err != nil {
		return err
	}
	settingsPath, mcpPath := generatedPaths(spec)
	for path, body := range map[string]any{settingsPath: generateSettings(spec), mcpPath: generateMCP(spec)} {
		raw, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}
