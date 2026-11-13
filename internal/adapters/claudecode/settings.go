package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	// DisableBypassPermissionsMode is `disable`, so the CLI refuses the bypass mode in every session
	// ropeshift starts, from a flag or a key a person presses in a pane (NFR-SEC-3).
	DisableBypassPermissionsMode string `json:"disableBypassPermissionsMode"`
}

// bypassOff is the value that turns the bypass mode off.
const bypassOff = "disable"

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

// commitRules let a lane stage, stage a deletion or a rename, and commit with no prompt, because nobody
// may be there to answer one, and a lane would wait out `approval_wait` on its own work (FR-LANE-19).
// Each names one subcommand, so the CLI holds a compound command to every part of it, and a push, a
// move to another branch, and a `git -C` are none of these. A session's hook holds what is left to the
// worktree (lanes.DenyPolicy).
var commitRules = []string{"Bash(git add *)", "Bash(git commit *)", "Bash(git rm *)", "Bash(git mv *)"}

// assistantRules are what an assistant's session runs with no prompt besides its own tools and reading its
// read roots: a shell for `ropeshift` commands, which act with its credential. Any other command has no
// rule, so it would ask, and the worker's hook denies it with a reason; every edit, every write, and the
// web are denied outright (ADR-0024).
var assistantRules = []string{"Bash(" + assistantProgram + " *)"}

// assistantProgram is the one program an assistant's session runs in a shell, which its hook holds it to
// whatever its rules or its mode let through.
const assistantProgram = "ropeshift"

// assistantDenied are the tools an assistant's session never runs: the web, through which anything it read
// would leave.
var assistantDenied = []string{"WebFetch", "WebSearch"}

// assistantReadRoots are the only directories an assistant's session reads: its own directory and the
// read-only trees of the project's repositories, each by its name and the name its links resolve to. A name
// a rule would read as a pattern gets no rule, and the hook still reads it.
func assistantReadRoots(spec ports.SessionSpec) []string {
	var roots []string
	for _, root := range readRootsOf(spec) {
		if !strings.ContainsAny(root, patternCharacters) {
			roots = append(roots, root)
		}
	}
	return roots
}

// readRootsOf are an assistant's own directory and its read-only trees, each by its name and the name its
// links resolve to.
func readRootsOf(spec ports.SessionSpec) []string {
	var roots []string
	for _, dir := range append([]string{spec.Dir}, spec.ReadOnlyDirs...) {
		if dir == "" {
			continue
		}
		names := []string{filepath.Clean(dir)}
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			names = append(names, real)
		}
		for _, name := range names {
			if !slices.Contains(roots, name) {
				roots = append(roots, name)
			}
		}
	}
	return roots
}

// pushRules ask before every push on a lane that writes, in every mode, because an ask rule beats an allow
// rule and holds where nothing else would ask (FR-LANE-19). A push the rule does not match, such as
// `git -C . push`, is allowed by no rule either, so it asks in any mode that asks.
var pushRules = []string{"Bash(git push *)"}

// patternCharacters make a path in a rule a pattern, which could reach past the path it names.
const patternCharacters = "()[]{}*?!\\"

// allowRules are what a session runs with no prompt: its own tools, and, for a session that writes in
// its worktree, every edit there, the programs that make, move, and delete files there, and a stage
// and a commit (FR-LANE-19).
func allowRules(spec ports.SessionSpec) []string {
	rules := make([]string, 0, len(spec.Tools)+len(commitRules)+4)
	for _, tool := range spec.Tools {
		rules = append(rules, toolRule(tool))
	}
	if spec.Assistant {
		for _, root := range assistantReadRoots(spec) {
			rules = append(rules, "Read(/"+root+"/**)")
		}
		return append(rules, assistantRules...)
	}
	if spec.OnlyTools || spec.ReadOnly {
		return rules
	}
	rules = append(rules, commitRules...)
	if spec.Dir == "" {
		return rules
	}
	rules = append(rules, worktreeRules(spec.Dir)...)
	if spec.Binary != "" {
		rules = append(rules, fileRules()...)
	}
	return rules
}

// worktreeRules let every tool that edits a file write in the worktree with no prompt. The CLI allows a
// path through a link only when the path asked for and the file it resolves to both match, so a link
// that leads out of the worktree still asks. The worktree goes by each of its names, because `/tmp` is
// a link on macOS. A name a rule would read as a pattern gets none, and an edit there asks.
func worktreeRules(worktree string) []string {
	names := []string{filepath.Clean(worktree)}
	if real, err := filepath.EvalSymlinks(worktree); err == nil && real != names[0] {
		names = append(names, real)
	}
	var rules []string
	for _, name := range names {
		if strings.ContainsAny(name, patternCharacters) {
			return nil
		}
		rules = append(rules, "Edit(/"+name+"/**)")
	}
	return rules
}

// fileRules let the programs that make, move, and delete files run with no prompt. A rule names a
// program and not a path, so each needs the hook, which holds every operand to the worktree and reads
// the disk for a link on the way (lanes.FilePrograms).
func fileRules() []string {
	programs := lanes.FilePrograms()
	rules := make([]string, 0, len(programs))
	for _, program := range programs {
		rules = append(rules, lanes.ToolBash+"("+program+" *)")
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
			DisableBypassPermissionsMode: bypassOff,
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
