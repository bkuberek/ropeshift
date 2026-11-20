package claudecode

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
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
//     enabled plugins, and their synced skills ([skills]). The `local` source is the person's too
//     (laneSources), so a lane names `project` alone.
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

// laneSources are the setting sources a session reads: the repository's shared settings alone. The `user` source is
// the person's own, and so is `local`: a lane is a git worktree of the person's clone, and in a worktree the CLI reads
// `.claude/settings.local.json` at the main checkout's root ([settings]), the person's own settings for the project
// with the rules they saved with "don't ask again", and `CLAUDE.local.md` beside it. Ropeshift's own settings arrive
// through `--settings`.
//
// [settings]: https://code.claude.com/docs/en/settings#where-claude-code-keeps-the-local-file-in-a-git-repository
var laneSources = []string{sourceProject}

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

// repositoryServers are the MCP servers the repository's own `.mcp.json` names, which is project setup a lane keeps.
// They are read as the repository holds them at the commit the lane began from, so an edit a lane makes to the file
// reaches no lane until it is merged, and a file that does not parse names none. A server named after ropeshift's own
// is left out, so it never replaces it.
func repositoryServers(spec ports.SessionSpec) map[string]json.RawMessage {
	raw := repositoryFile(spec, ".mcp.json")
	if raw == nil {
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

// The bounds of a read of a repository's file: how long git may take, and the most the file may hold.
const (
	repositoryReadWait = 10 * time.Second
	maxRepositoryFile  = 1 << 20
	symlinkMode        = "120000"
)

// repositoryFile is a file the repository holds at the commit the lane's worktree began from, read from git's objects
// and never from the worktree, which the lane can edit (FR-PROF-5). A base that is no full commit hash, a file absent
// at it, a path that is no regular file there, and a file over the limit read as none.
func repositoryFile(spec ports.SessionSpec, name string) []byte {
	if spec.Dir == "" || !fullHash(spec.Base) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	git := gitcli.Git{Dir: spec.Dir, Env: spec.Env}
	listing, err := git.Text(ctx, "ls-tree", "--long", spec.Base, "--", name)
	if err != nil || listing == "" {
		return nil
	}
	entry, _, _ := strings.Cut(listing, "\t")
	fields := strings.Fields(entry)
	if len(fields) != 4 || fields[1] != "blob" || fields[0] == symlinkMode {
		return nil
	}
	if size, err := strconv.ParseInt(fields[3], 10, 64); err != nil || size > maxRepositoryFile {
		return nil
	}
	raw, err := git.Output(ctx, "cat-file", "blob", fields[2])
	if err != nil {
		return nil
	}
	return raw
}

// fullHash says text is the lowercase hexadecimal hash git gives a commit, of SHA-1 or SHA-256 length, so no base
// reads as an option or a name.
func fullHash(text string) bool {
	if len(text) != 40 && len(text) != 64 {
		return false
	}
	return strings.Trim(text, "0123456789abcdef") == ""
}

// laneMCP is a generation's `--mcp-config`: ropeshift's own server, and the servers of the repository's `.mcp.json`
// at the lane's base, as the repository wrote them. `--strict-mcp-config` loads this file alone, so the member's servers, a plugin's, and
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
