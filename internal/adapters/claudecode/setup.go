package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// initReport is what the driver reads of a session's `system/init`: what the session loaded ([TypeScript SDK]).
//
// [TypeScript SDK]: https://code.claude.com/docs/en/agent-sdk/typescript
type initReport struct {
	Version    string   `json:"claude_code_version"`
	Model      string   `json:"model"`
	Tools      []string `json:"tools"`
	MCPServers []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"mcp_servers"`
	Agents        []string `json:"agents"`
	Skills        []string `json:"skills"`
	SlashCommands []string `json:"slash_commands"`
	Plugins       []struct {
		Name string `json:"name"`
	} `json:"plugins"`
}

// The MCP server sources that are not the member's own: the repository's `.mcp.json`, and the organization's
// ([TypeScript SDK]).
//
// [TypeScript SDK]: https://code.claude.com/docs/en/agent-sdk/typescript#mcpserverprovenance
var notPersonalSources = []string{"project", "managed", "enterprise"}

// setupContext is what tells the member's own pieces from the rest, read once when a session starts: what ropeshift
// and the repository supply, and what the member's configuration directory holds. A piece is the member's only when
// their directory names it and neither ropeshift nor the repository supplies one of that name, so a built-in agent or
// skill is never theirs.
type setupContext struct {
	home       string
	repository map[string]bool
	roster     map[string]bool
	repo       pieces
	member     pieces
}

// pieces are the agents, skills, commands, and enabled plugins one place holds, each with the file that names it.
type pieces struct {
	agents, skills, commands, plugins map[string]string
}

func setupContextOf(spec ports.SessionSpec) setupContext {
	c := setupContext{
		home: envValue(spec.Env, "HOME"), repository: map[string]bool{}, roster: map[string]bool{}, repo: repositoryPieces(spec),
	}
	for name := range repositoryServers(spec) {
		c.repository[name] = true
	}
	for _, agent := range spec.Agents {
		c.roster[agent.Name] = true
	}
	if config := memberConfig(spec); config != "" {
		c.member = piecesIn(config, "settings.json")
	}
	return c
}

func newPieces() pieces {
	return pieces{agents: map[string]string{}, skills: map[string]string{}, commands: map[string]string{}, plugins: map[string]string{}}
}

// piecesIn reads one `.claude` directory: its agents by the name their frontmatter gives, its skills, synced ones
// included, its commands, and the plugins its settings files enable.
func piecesIn(dir string, settings ...string) pieces {
	out := newPieces()
	walkMarkdown(filepath.Join(dir, "agents"), func(path string) { out.agents[frontmatterName(path)] = path })
	walkMarkdown(filepath.Join(dir, "commands"), func(path string) {
		out.commands[strings.TrimSuffix(filepath.Base(path), ".md")] = path
	})
	for _, root := range []string{filepath.Join(dir, "skills"), filepath.Join(dir, "skills", "synced")} {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "synced" || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			skill := filepath.Join(root, entry.Name())
			name := frontmatterName(filepath.Join(skill, "SKILL.md"))
			if name == "SKILL" {
				name = entry.Name()
			}
			out.skills[name] = skill
		}
	}
	for _, name := range settings {
		path := filepath.Join(dir, name)
		if raw, err := os.ReadFile(path); err == nil {
			out.enablePlugins(raw, path)
		}
	}
	return out
}

// enablePlugins adds the plugins a settings file enables, named without their marketplace.
func (p pieces) enablePlugins(settings []byte, path string) {
	var file struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	if json.Unmarshal(settings, &file) != nil {
		return
	}
	for key, on := range file.Enabled {
		plugin, _, _ := strings.Cut(key, "@")
		if on {
			p.plugins[plugin] = path
		}
	}
}

// The bounds of the read of the repository's `.claude` directory: the files it lists, and the bytes it reads of each.
const (
	maxRepositoryPieceFiles = 4096
	maxPieceFile            = 64 << 10
)

// repositoryPieces are the agents, skills, commands, and enabled plugins the repository's `.claude` directory holds at
// the lane's base (atSettingsBase), read as repositoryFile reads a file, so a piece a lane adds to its worktree or
// commits to a branch it continues is never the repository's until it is merged, and never hides one of the member's
// own: what the repository supplies decides which pieces are the member's, and so what the leak report says. A
// directory that cannot be read holds none, so each piece the member's directory names reads as theirs.
func repositoryPieces(spec ports.SessionSpec) pieces {
	spec = atSettingsBase(spec)
	out := newPieces()
	if spec.Repository == "" || spec.Base == "" {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	git := repositoryGit(spec)
	files, err := git.FilesUnder(ctx, spec.Base, ".claude", maxRepositoryPieceFiles)
	if err != nil {
		return out
	}
	read := func(file gitcli.TreeFile) []byte {
		raw, _ := git.BlobOf(ctx, file.Blob, maxPieceFile)
		return raw
	}
	skills := map[string]string{}
	for _, file := range files {
		at := ".claude/" + file.Path
		parts := strings.Split(file.Path, "/")
		name := path.Base(file.Path)
		switch {
		case parts[0] == "agents" && strings.HasSuffix(name, ".md"):
			out.agents[frontmatterNameOf(read(file), strings.TrimSuffix(name, ".md"))] = at
		case parts[0] == "commands" && strings.HasSuffix(name, ".md"):
			out.commands[strings.TrimSuffix(name, ".md")] = at
		case parts[0] == "skills":
			dir, ok := skillOf(parts)
			if _, seen := skills[path.Dir(at)]; ok && !seen {
				skills[path.Dir(at)] = dir
			}
			if ok && name == "SKILL.md" {
				skills[path.Dir(at)] = frontmatterNameOf(read(file), dir)
			}
		case file.Path == "settings.json":
			out.enablePlugins(read(file), at)
		}
	}
	for dir, name := range skills {
		out.skills[name] = dir
	}
	return out
}

// skillOf reads the path of a file under `.claude/skills/`, split at its slashes, as piecesIn reads that directory, and
// gives the skill directory the file is directly in: a directory of skills/ or of skills/synced/ whose name starts
// with no dot.
func skillOf(parts []string) (string, bool) {
	rest := parts[1:]
	if len(rest) > 2 && rest[0] == "synced" {
		rest = rest[1:]
	}
	if len(rest) != 2 || rest[0] == "synced" || strings.HasPrefix(rest[0], ".") {
		return "", false
	}
	return rest[0], true
}

func walkMarkdown(root string, take func(path string)) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".md") {
			take(path)
		}
		return nil
	})
}

// frontmatterName is the `name` a file's frontmatter gives, or the file's own name without its extension.
func frontmatterName(file string) string {
	raw, _ := os.ReadFile(file)
	return frontmatterNameOf(raw, strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)))
}

// frontmatterNameOf is the `name` the frontmatter of a file's content gives, or fallback.
func frontmatterNameOf(raw []byte, fallback string) string {
	if !bytes.HasPrefix(raw, []byte("---")) {
		return fallback
	}
	lines := bufio.NewScanner(bytes.NewReader(raw))
	lines.Scan()
	for lines.Scan() {
		line := lines.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if value, ok := strings.CutPrefix(line, "name:"); ok {
			if name := strings.Trim(strings.TrimSpace(value), `"'`); name != "" {
				return name
			}
		}
	}
	return fallback
}

// shown says where a file is, under `~` when it is in the member's home.
func (c setupContext) shown(path string) string {
	if c.home != "" {
		if rest, ok := strings.CutPrefix(path, c.home+string(filepath.Separator)); ok {
			return "~/" + filepath.ToSlash(rest)
		}
	}
	return path
}

// of turns a session's init into what it loaded, with where each MCP server came from and the member's own pieces.
func (c setupContext) of(init initReport) *lanes.Setup {
	out := &lanes.Setup{Version: init.Version, Model: init.Model, Tools: init.Tools, Agents: init.Agents, Skills: init.Skills}
	for _, server := range init.MCPServers {
		from := server.Source
		switch {
		case server.Name == ServerName:
			from = lanes.FromRopeshift
		case c.repository[server.Name]:
			from = lanes.FromRepository
		}
		out.MCPServers = append(out.MCPServers, lanes.SetupServer{Name: server.Name, From: from})
		if from != lanes.FromRopeshift && from != lanes.FromRepository && !slices.Contains(notPersonalSources, from) {
			out.Personal = append(out.Personal, lanes.PersonalItem{Kind: lanes.PieceMCPServer, Name: server.Name, From: from})
		}
	}
	for _, plugin := range init.Plugins {
		out.Plugins = append(out.Plugins, plugin.Name)
		if path, ok := c.member.plugins[plugin.Name]; ok && c.repo.plugins[plugin.Name] == "" {
			out.Personal = append(out.Personal, lanes.PersonalItem{Kind: lanes.PiecePlugin, Name: plugin.Name, From: c.shown(path)})
		}
	}
	for _, agent := range init.Agents {
		if path, ok := c.member.agents[agent]; ok && c.repo.agents[agent] == "" && !c.roster[agent] {
			out.Personal = append(out.Personal, lanes.PersonalItem{Kind: lanes.PieceAgent, Name: agent, From: c.shown(path)})
		}
	}
	seen := map[string]bool{}
	for _, name := range slices.Concat(init.Skills, init.SlashCommands) {
		if seen[name] {
			continue
		}
		seen[name] = true
		if item, ok := c.memberCommand(name); ok {
			out.Personal = append(out.Personal, item)
		}
	}
	return out
}

// memberCommand says a skill or command a session loaded is the member's own, as a skill when their skills hold it.
func (c setupContext) memberCommand(name string) (lanes.PersonalItem, bool) {
	if path, ok := c.member.skills[name]; ok && c.repo.skills[name] == "" && c.repo.commands[name] == "" {
		return lanes.PersonalItem{Kind: lanes.PieceSkill, Name: name, From: c.shown(path)}, true
	}
	if path, ok := c.member.commands[name]; ok && c.repo.commands[name] == "" && c.repo.skills[name] == "" {
		return lanes.PersonalItem{Kind: lanes.PieceCommand, Name: name, From: c.shown(path)}, true
	}
	return lanes.PersonalItem{}, false
}
