package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// ServerName is the MCP server's name, which is part of every tool's name.
const ServerName = "ropeshift"

const inboundAccept = "accept"

// hookTimeoutSeconds bounds a hook the CLI runs. The hook holds itself to far less.
const hookTimeoutSeconds = 5

// eventPermissionRequest is the one hook that waits, up to a profile limit. The CLI's bound sits past
// the hook's own, because a hook the CLI ends says nothing, and a pane would stay on its prompt.
const (
	eventPermissionRequest = "PermissionRequest"
	approvalTimeoutMargin  = 30
)

// hookEvents are the events a lane listens to.
// approvals.
var hookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PermissionDenied", "PostToolUse",
	"PostToolUseFailure", "Notification", "PreCompact", "PostCompact", "Stop", "StopFailure", "SessionEnd",
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
	Deny  []string `json:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	// AdditionalDirectories are directories outside the worktree the session may read with no prompt: the
	// worktrees of other repositories it reads, which its deny rules keep it from writing.
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

// settingsFile is what a generation's `--settings` holds. It holds no secret, because
// a hook failure returns the command line to the model.
type settingsFile struct {
	Hooks        map[string][]hookGroup `json:"hooks,omitempty"`
	Permissions  permissions            `json:"permissions"`
	APIKeyHelper string                 `json:"apiKeyHelper,omitempty"`
	// AutoContinueAtUsageLimit is false, because the worker owns the wait: a session that sat out a
	// limit in its own process would hold it, and continue with nobody deciding.
	AutoContinueAtUsageLimit bool `json:"autoContinueAtUsageLimit"`
	// CrossSessionInbound is `accept`, because a worker's input to a pane and every
	// message to a lane arrive on the session's socket, and nobody is there to answer a
	// hold.
	CrossSessionInbound string      `json:"crossSessionInbound"`
	StatusLine          *statusLine `json:"statusLine,omitempty"`
}

// toolRule is the permission rule of one ropeshift MCP tool.
func toolRule(tool string) string { return "mcp__" + ServerName + "__" + tool }

// commitRules let a lane stage and commit with no prompt, because nobody may be there to answer one,
// and a lane would wait out `approval_wait` on its own work. Each names one subcommand, so the CLI
// holds a compound command to every part of it, and a push, a move to another branch, and a `git -C`
// are none of these. A session's hook holds what is left to the worktree (lanes.DenyPolicy).
var commitRules = []string{"Bash(git add *)", "Bash(git commit *)"}

// allowRules are what a session runs with no prompt: its own tools, and a stage and a commit for a
// session that has a worktree and writes in it.
func allowRules(spec ports.SessionSpec) []string {
	rules := make([]string, 0, len(spec.Tools)+len(commitRules))
	for _, tool := range spec.Tools {
		rules = append(rules, toolRule(tool))
	}
	if !spec.OnlyTools && !spec.ReadOnly {
		rules = append(rules, commitRules...)
	}
	return rules
}

// generateSettings builds a generation's settings. The hook commands use the stable
// path (D18), quoted, because a state directory can hold a space and the CLI runs a
// hook through a shell. PreToolUse fails closed: a hook that times out must block the
// call, or a denial could be skipped by a slow machine.
func generateSettings(spec ports.SessionSpec) settingsFile {
	out := settingsFile{
		Permissions: permissions{
			Allow: allowRules(spec), Deny: denyRules(spec), Ask: askRules(spec), AdditionalDirectories: slices.Clone(spec.ReadOnlyDirs),
		},
		APIKeyHelper: spec.Credential.APIKeyHelper, CrossSessionInbound: spec.Override.Inbound(inboundAccept),
	}
	if spec.Binary == "" {
		return out
	}
	out.Hooks = map[string][]hookGroup{}
	for _, event := range hookEvents {
		command := hookCommand{
			Type: "command", Command: lanes.ShellQuote(spec.Binary) + " hook " + event, Timeout: hookTimeoutSeconds,
		}
		if event == eventPermissionRequest {
			command.Timeout = int(spec.ApprovalWait.Seconds()) + approvalTimeoutMargin
		}
		if event == "PreToolUse" {
			command.OnFailure = "block"
		}
		out.Hooks[event] = []hookGroup{{Hooks: []hookCommand{command}}}
	}
	if spec.RunMode == ports.RunModeInteractive {
		out.StatusLine = composedStatusLine(spec)
	}
	return out
}

type mcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type mcpFile struct {
	Servers map[string]mcpServer `json:"mcpServers"`
}

// serverVariables are what `ropeshift mcp` reads. The CLI can start a stdio server with
// a baseline environment plus the server's own `env`, so these are named here and not
// left to inheritance.
var serverVariables = []string{lanes.EnvWorkerSocket, lanes.EnvLaneToken}

// generateMCP builds a generation's `--mcp-config`. Each variable is a reference the CLI
// expands from the session's environment, so this file holds a name and never the lane
// token.
func generateMCP(spec ports.SessionSpec) mcpFile {
	out := mcpFile{Servers: map[string]mcpServer{}}
	if spec.Binary == "" {
		return out
	}
	env := make(map[string]string, len(serverVariables))
	for _, name := range serverVariables {
		env[name] = "${" + name + "}"
	}
	out.Servers[ServerName] = mcpServer{Type: "stdio", Command: spec.Binary, Args: []string{"mcp"}, Env: env}
	return out
}

// policyPath names the file a generation's hook reads.
func policyPath(spec ports.SessionSpec) string {
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("policy-%d.json", spec.Generation))
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
	files := map[string]any{settingsPath: generateSettings(spec), mcpPath: generateMCP(spec), policyPath(spec): compilePolicy(spec)}
	for path, body := range files {
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
