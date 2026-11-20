package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// initReport is what the driver reads of a session's `system/init`: what the session loaded ([TypeScript SDK]).
//
// [TypeScript SDK]: https://code.claude.com/docs/en/agent-sdk/typescript
type initReport struct {
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
		home: envValue(spec.Env, "HOME"), repository: map[string]bool{}, roster: map[string]bool{},
		repo: piecesIn(filepath.Join(spec.Dir, ".claude"), "settings.json"),
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

// piecesIn reads one `.claude` directory: its agents by the name their frontmatter gives, its skills, synced ones
// included, its commands, and the plugins its settings files enable.
func piecesIn(dir string, settings ...string) pieces {
	out := pieces{agents: map[string]string{}, skills: map[string]string{}, commands: map[string]string{}, plugins: map[string]string{}}
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
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var file struct {
			Enabled map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(raw, &file) != nil {
			continue
		}
		for key, on := range file.Enabled {
			plugin, _, _ := strings.Cut(key, "@")
			if on {
				out.plugins[plugin] = path
			}
		}
	}
	return out
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
func frontmatterName(path string) string {
	fallback := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(raw, []byte("---")) {
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
	out := &lanes.Setup{Model: init.Model, Tools: init.Tools, Agents: init.Agents, Skills: init.Skills}
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
