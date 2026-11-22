package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
//     turns it off ([environment variables]). It turns a subagent's `memory` off too, whose `user` scope is a
//     directory in the member's `~/.claude/agent-memory/`: "Subagent memory is part of auto memory: if you turn auto
//     memory off, with the `autoMemoryEnabled` setting or `CLAUDE_CODE_DISABLE_AUTO_MEMORY`, the `memory` field has no
//     effect" ([subagent memory]), so a subagent a lane writes with `memory: user` reads nothing of the member's and
//     holds nothing.
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
// [subagent memory]: https://code.claude.com/docs/en/sub-agents#enable-persistent-memory

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
// parse names none. A server named after ropeshift's own is left out, so it never replaces it, and the preflight says
// so (serverClash).
func repositoryServers(spec ports.SessionSpec) map[string]json.RawMessage {
	servers, _ := repositoryServersOf(spec)
	return servers
}

// repositoryServersOf is repositoryServers, and whether the repository named a server after ropeshift's own, which it
// left out.
func repositoryServersOf(spec ports.SessionSpec) (map[string]json.RawMessage, bool) {
	raw := repositoryFile(spec, ".mcp.json")
	if raw == nil {
		return nil, false
	}
	var file struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return nil, false
	}
	_, clash := file.Servers[ServerName]
	delete(file.Servers, ServerName)
	return file.Servers, clash
}

// serverClash is what a preflight says of a repository whose `.mcp.json` names a server after ropeshift's own, which
// the lane runs in its place.
func serverClash(spec ports.SessionSpec) []string {
	if _, clash := repositoryServersOf(spec); !clash {
		return nil
	}
	return []string{fmt.Sprintf("the repository's .mcp.json names an MCP server %q, the name of ropeshift's own server, so it is left out and the lane runs ropeshift's", ServerName)}
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

// A linked git worktree with no `.claude/skills` of its own loads the main checkout's: "when the worktree checkout has
// no `.claude/skills` directory at its root, for example because your `.claude/skills` is gitignored, Claude Code loads
// the main checkout's project skills in the worktree session. In a worktree with its own `.claude/skills` directory,
// only that copy loads", and "The same read-through covers `.claude/agents` and `.claude/commands`" ([worktrees]). A
// lane is a linked worktree of the member's clone, whose untracked skills, subagents, and commands are the member's own
// and no part of the repository, so each generation's start gives the worktree its own of each, empty where the
// repository holds none (ownPieceDirs).
//
// [worktrees]: https://code.claude.com/docs/en/worktrees#what-worktrees-share-with-the-main-checkout
var readThroughDirs = []string{".claude/agents", ".claude/commands", ".claude/skills"}

// errNoOwnPieceDir says a worktree's skills, subagents, or commands directory is no directory of its own, so the main
// checkout's could load in its place.
var errNoOwnPieceDir = errors.New("is no directory of the worktree's own, so the main checkout's would load in its place")

// ownPieceDirs makes each directory a linked worktree must hold so that no read-through reaches the main checkout
// (readThroughDirs). One that is there, through a link of the lane's own included, is left as it is; a missing one is
// made only through directories of the worktree's own, and a link or a file on the way, or one that is no directory,
// fails the start. A worktree that is no linked worktree, whose `.git` is no file, reads through to nothing.
func ownPieceDirs(spec ports.SessionSpec) error {
	if spec.Dir == "" {
		return nil
	}
	if info, err := os.Lstat(filepath.Join(spec.Dir, ".git")); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	for _, dir := range readThroughDirs {
		info, err := os.Stat(filepath.Join(spec.Dir, filepath.FromSlash(dir)))
		switch {
		case err == nil && info.IsDir():
			continue
		case err == nil, !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("the worktree's %s %w", dir, errNoOwnPieceDir)
		}
		if _, err := ownParents(spec.Dir, dir+"/.", true); err != nil {
			return fmt.Errorf("the worktree's %s %w: %v", dir, errNoOwnPieceDir, err)
		}
	}
	return nil
}

// A lane is a linked worktree of the member's clone, and "plugins installed at project scope from the main checkout
// also load in worktrees of the same repository" ([worktrees]). The `--settings` value does not replace what the other
// files enable: their `enabledPlugins` "merge key by key. For each plugin id, the value that applies is the one from the
// highest-precedence source that mentions the id. A source that doesn't mention the id leaves the value from the
// lower-precedence source in effect", and the `flag` source, `--settings`, outranks the project and local files there
// ([plugin loading]). No documented switch gives a worktree a plugin scope of its own. So a lane's generated settings
// name each plugin the main checkout's working tree enables, in its shared or its local settings, that neither the
// repository's settings at the lane's base nor the worktree's own enable, and turn it off, which holds whether the CLI
// reaches the main checkout through its project scope or its install records. A plugin the base enables is left as the
// repository's settings set it, and not stated as on, which would also fetch one with an external source ([plugin
// loading]). Managed settings, which "no other source overrides", are the organization's.
//
// [worktrees]: https://code.claude.com/docs/en/worktrees#what-worktrees-share-with-the-main-checkout
// [plugin loading]: https://code.claude.com/docs/en/plugins/loading#find-where-a-plugin-is-enabled
func lanePlugins(spec ports.SessionSpec) map[string]bool {
	if spec.Repository == "" || spec.Dir == "" {
		return nil
	}
	allowed := enabledPlugins(repositoryFile(spec, projectSettingsFile))
	for _, file := range settingsFiles(spec) {
		for id := range enabledPlugins(worktreeFile(spec, file)) {
			allowed[id] = true
		}
	}
	var out map[string]bool
	for _, file := range []string{projectSettingsFile, localSettingsFile} {
		raw, whole := boundedFile(filepath.Join(spec.Repository, filepath.FromSlash(file)))
		if !whole {
			continue
		}
		for id := range enabledPlugins(raw) {
			if !allowed[id] {
				if out == nil {
					out = map[string]bool{}
				}
				out[id] = false
			}
		}
	}
	return out
}

// enabledPlugins are the plugins a settings file's `enabledPlugins` turns on. A file that is absent or does not parse
// turns none on.
func enabledPlugins(raw []byte) map[string]bool {
	var file struct {
		Plugins map[string]bool `json:"enabledPlugins"`
	}
	out := map[string]bool{}
	if len(raw) == 0 || json.Unmarshal(raw, &file) != nil {
		return out
	}
	for id, on := range file.Plugins {
		if on {
			out[id] = true
		}
	}
	return out
}
