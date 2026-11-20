package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// A session runs clean of the person's own CLI setup while it keeps their sign-in, which lives in their
// configuration directory, so no second `CLAUDE_CONFIG_DIR` holds it apart (ADR-0040). Each switch is a
// documented one:
//
//   - `--setting-sources` loads only the sources it names ([CLI reference]). The `user` source is the
//     person's `~/.claude/settings.json`, their `~/.claude/CLAUDE.md` and `~/.claude/rules/`, and their
//     skills, commands, and subagents in `~/.claude/skills/`, `~/.claude/commands/`, and `~/.claude/agents/`
//     ([setting sources]), so a list that leaves it out also leaves out their hooks, status line, and
//     enabled plugins, and their synced skills ([skills]).
//   - `--strict-mcp-config` skips the MCP servers in `.mcp.json`, `~/.claude.json`, and plugins, and loads
//     only those `--mcp-config` names ([CLI reference]); with it, the claude.ai connectors do not load
//     either ([setting sources]).
//   - Auto memory loads whatever the setting sources say ([setting sources]), and is the person's own notes,
//     kept per repository and shared across its worktrees ([memory]), so `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`
//     turns it off ([environment variables]).
//
// `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1` is documented too, but it drops every CLAUDE.md, the repository's own
// among them ([environment variables]), and the repository's instructions are what a lane works by, so a
// lane does not set it: leaving out the `user` source already leaves out the person's own.
//
// [CLI reference]: https://code.claude.com/docs/en/cli-reference
// [setting sources]: https://code.claude.com/docs/en/agent-sdk/claude-code-features#control-filesystem-settings-with-settingsources
// [skills]: https://code.claude.com/docs/en/skills
// [memory]: https://code.claude.com/docs/en/memory
// [environment variables]: https://code.claude.com/docs/en/env-vars

const (
	flagStrictMCP = "--strict-mcp-config"
	envNoMemory   = "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"
)

// laneSources are the setting sources a session reads, from the lowest precedence up: the repository's
// shared settings and its local ones, and none of the person's user settings.
var laneSources = []string{sourceProject, sourceLocal}

// isolationArgs hold a session to ropeshift's MCP servers and the repository's settings. A real-session
// suite that names its own setting sources narrows them, so the driver states none of its own then.
func isolationArgs(spec ports.SessionSpec) []string {
	out := []string{flagStrictMCP}
	if slices.Contains(spec.Override.Args(), flagSettingSources) {
		return out
	}
	return append(out, flagSettingSources, strings.Join(laneSources, ","))
}

// isolationEnv is what a session's environment adds to keep the person's own notes out of it.
func isolationEnv() []string { return []string{envNoMemory} }

// The member's own instructions are their `CLAUDE.md` and rules in their configuration directory. Leaving out the
// `user` setting source leaves them out too ([setting sources]), and `claudeMdExcludes`, whose patterns match absolute
// paths ([memory]), says so a second time in the lane's own settings, while the repository's CLAUDE.md still loads.
// Whether the CLI applies `claudeMdExcludes` from `--settings` is unverified: the page names the user, project, local,
// and managed layers.
//
// [setting sources]: https://code.claude.com/docs/en/agent-sdk/claude-code-features#control-filesystem-settings-with-settingsources
// [memory]: https://code.claude.com/docs/en/memory#exclude-specific-claude-md-files
func personalInstructions(spec ports.SessionSpec) []string {
	config := memberConfig(spec)
	if config == "" {
		return nil
	}
	return []string{filepath.Join(config, "CLAUDE.md"), filepath.Join(config, "rules") + "/**"}
}

// memberConfig is the member's configuration directory: the one `CLAUDE_CONFIG_DIR` names, or `~/.claude`
// ([`.claude` directory]). Empty means the session's environment names no home.
//
// [`.claude` directory]: https://code.claude.com/docs/en/claude-directory
func memberConfig(spec ports.SessionSpec) string {
	if dir := envValue(spec.Env, "CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	if home := envValue(spec.Env, "HOME"); home != "" {
		return filepath.Join(home, ".claude")
	}
	return ""
}

func envValue(env []string, name string) string {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, name+"="); ok {
			value = v
		}
	}
	return value
}

// repositoryServers are the MCP servers the lane's own worktree names in its `.mcp.json`, which is project setup a
// lane keeps. They are read from the worktree and never from the member's clone, and a file that does not parse names
// none. A server named after ropeshift's own is left out, so it never replaces it.
func repositoryServers(spec ports.SessionSpec) map[string]json.RawMessage {
	raw, err := os.ReadFile(filepath.Join(spec.Dir, ".mcp.json"))
	if err != nil {
		return nil
	}
	var file struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return nil
	}
	delete(file.Servers, ServerName)
	return file.Servers
}

// laneMCP is a generation's `--mcp-config`: ropeshift's own server, and the servers of the worktree's `.mcp.json`
// as the repository wrote them. `--strict-mcp-config` loads this file alone, so the member's servers, a plugin's, and
// the claude.ai connectors stay out and the repository's stay in ([CLI reference]).
//
// [CLI reference]: https://code.claude.com/docs/en/cli-reference
func laneMCP(spec ports.SessionSpec) any {
	servers := map[string]any{}
	for name, server := range repositoryServers(spec) {
		servers[name] = server
	}
	for name, server := range generateMCP(spec).Servers {
		servers[name] = server
	}
	return struct {
		Servers map[string]any `json:"mcpServers"`
	}{servers}
}
