package claudecode

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
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
//   - `--strict-mcp-config` skips the MCP servers in `.mcp.json`, in the person's own user configuration,
//     and in plugins, and loads only those `--mcp-config` names ([CLI reference]); with it, the claude.ai
//     connectors do not load either ([setting sources]).
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
	return []string{globPath(filepath.Join(config, "CLAUDE.md")), globPath(filepath.Join(config, "rules")) + "/**"}
}

// instructionExcludes are a lane's `claudeMdExcludes`: the member's own instructions, and every instruction file above
// the lane's worktree.
func instructionExcludes(spec ports.SessionSpec) []string {
	var out []string
	for _, pattern := range slices.Concat(personalInstructions(spec), aboveTheWorktree(spec)) {
		if !slices.Contains(out, pattern) {
			out = append(out, pattern)
		}
	}
	return out
}

// aboveInstructions are the instruction files the CLI loads at launch from each directory above its working directory:
// `CLAUDE.md` and `CLAUDE.local.md` ([how CLAUDE.md files load]), `.claude/CLAUDE.md`, which counts as one, and
// `AGENTS.md` and `.claude/AGENTS.md` ([AGENTS.md]), whose loads `claudeMdExcludes` holds too. A directory's
// `.claude/rules/` is named as well, so no rule of a directory above loads either.
//
// [how CLAUDE.md files load]: https://code.claude.com/docs/en/memory#how-claude-md-files-load
// [AGENTS.md]: https://code.claude.com/docs/en/memory#when-claude-code-reads-agents-md
var aboveInstructions = []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", "AGENTS.md", ".claude/AGENTS.md", ".claude/rules/**"}

// aboveTheWorktree are the instruction files of every directory above a lane's worktree, which are neither the
// repository's nor the member's: a lane loads only the repository's own instructions, from its worktree (ADR-0040). The
// CLI loads them "from your current working directory and every directory above it" ([memory]), and `claudeMdExcludes`
// matches "absolute file paths using glob syntax" ([exclude]), so each directory is named as given and as its links
// resolve, and a character a glob reads is escaped. An instruction file of a directory above that loads anyway is
// reported as a leak, from what the session's `InstructionsLoaded` hook says it loaded.
//
// [memory]: https://code.claude.com/docs/en/memory#how-claude-md-files-load
// [exclude]: https://code.claude.com/docs/en/memory#exclude-specific-claude-md-files
func aboveTheWorktree(spec ports.SessionSpec) []string {
	if spec.Dir == "" {
		return nil
	}
	dirs := []string{filepath.Clean(spec.Dir)}
	if real, err := filepath.EvalSymlinks(spec.Dir); err == nil && real != dirs[0] {
		dirs = append(dirs, real)
	}
	var out []string
	for _, dir := range dirs {
		for parent := filepath.Dir(dir); ; parent = filepath.Dir(parent) {
			for _, name := range aboveInstructions {
				out = append(out, strings.TrimSuffix(globPath(parent), "/")+"/"+name)
			}
			if parent == filepath.Dir(parent) {
				break
			}
		}
	}
	return out
}

// globPath escapes each character of a path that a glob pattern reads, so a pattern names that path alone.
func globPath(path string) string {
	var out strings.Builder
	for _, r := range path {
		if strings.ContainsRune(patternCharacters, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
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
// They are read as the repository holds them at the lane's base (repositoryFile), so an edit a lane makes to the file,
// in its worktree or in a commit of a branch it continues, reaches no lane until it is merged, and a file that does not
// parse names none. A server named after ropeshift's own is left out, so it never replaces it.
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
)

// repositoryFile is a file the repository holds at the lane's base, the commit its claim resolved (atSettingsBase), and
// not at the tip of a branch the lane continues, which a lane wrote. It is read from git's objects and never from the
// worktree, which the lane can edit (FR-PROF-5). Git runs in the clone the worktree came from and
// not in the worktree, whose `.git` file the lane can point at a git directory of its own, and every object it reads
// must hash to its name, because the lane can write to the git directory it shares with the clone (gitcli.FileAt).
// It fetches no missing object of a partial clone. A base that is no full commit hash, a file absent at it, a path
// that is no regular file there, a file over the limit, and an object that does not hash to its name read as none.
func repositoryFile(spec ports.SessionSpec, name string) []byte {
	spec = atSettingsBase(spec)
	if spec.Repository == "" || spec.Base == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	raw, found, err := repositoryGit(spec).FileAt(ctx, spec.Base, name, maxRepositoryFile)
	if err != nil || !found {
		return nil
	}
	return raw
}

// repositoryGit runs git in the clone the lane's worktree came from, with the lane's environment, and fetches no missing
// object of a partial clone.
func repositoryGit(spec ports.SessionSpec) gitcli.Git {
	return gitcli.Git{Dir: spec.Repository, Env: spec.Env, Extra: []string{gitcli.NoLazyFetch}}
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
