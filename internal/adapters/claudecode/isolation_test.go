package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

func laneSpec(t *testing.T) ports.SessionSpec {
	t.Helper()
	return ports.SessionSpec{
		Lane: "01JLANE", Generation: 1, Dir: t.TempDir(), ConfigDir: t.TempDir(), Binary: "/opt/ropeshift/bin/ropeshift",
		PermissionMode: "default", Model: "opus", RunMode: ports.RunModeHeadless,
	}
}

// flagValue is the value that follows a flag in a command, and whether the flag is there at all.
func flagValue(args []string, flag string) (string, bool) {
	at := slices.Index(args, flag)
	if at < 0 {
		return "", false
	}
	if at+1 < len(args) && !strings.HasPrefix(args[at+1], "--") {
		return args[at+1], true
	}
	return "", true
}

// A lane starts, resumes, and opens its pane clean of the person's own setup: no user settings, so none
// of their hooks, status line, plugins, skills, agents, or CLAUDE.md, and no MCP server but ropeshift's own.
func TestALaneStartsCleanOfThePersonsOwnSetup(t *testing.T) {
	spec := laneSpec(t)
	commands := map[string][]string{
		"start":  StartArgs(spec),
		"resume": ResumeArgs(spec, ports.ResumeRef{SessionID: "s"}),
		"pane":   paneArgs(spec),
	}
	_, mcp := generatedPaths(spec)
	for name, args := range commands {
		if sources, ok := flagValue(args, "--setting-sources"); !ok || sources != "project,local" {
			t.Errorf("a lane's %s reads the setting sources %q: %v", name, sources, args)
		}
		if !slices.Contains(args, "--strict-mcp-config") {
			t.Errorf("a lane's %s loads MCP servers ropeshift did not give it: %v", name, args)
		}
		if config, _ := flagValue(args, "--mcp-config"); config != mcp {
			t.Errorf("a lane's %s loads the MCP config %q, not ropeshift's own %q", name, config, mcp)
		}
		for _, flag := range []string{"--setting-sources", "--strict-mcp-config"} {
			if count := countOf(args, flag); count != 1 {
				t.Errorf("a lane's %s states %s %d times: %v", name, flag, count, args)
			}
		}
	}
}

// An assistant's session keeps its own narrower tools and states each isolation flag once.
func TestAnAssistantStatesEachIsolationFlagOnce(t *testing.T) {
	spec := assistantSpec(t)
	spec.Commands = []string{"lane list"}
	args := StartArgs(spec)
	for _, flag := range []string{"--setting-sources", "--strict-mcp-config", "--tools"} {
		if count := countOf(args, flag); count != 1 {
			t.Errorf("an assistant states %s %d times: %v", flag, count, args)
		}
	}
}

// Auto memory is the person's own notes, kept per repository and shared across its worktrees, and the CLI
// loads it whatever the setting sources say, so a lane turns it off by its documented variable.
func TestALaneLoadsNoAutoMemory(t *testing.T) {
	spec := laneSpec(t)
	for name, env := range map[string][]string{"headless": sessionEnv(spec, Use{}), "pane": paneEnv(spec, Use{})} {
		if !slices.Contains(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
			t.Errorf("a %s lane loads the person's auto memory: %v", name, env)
		}
	}
}

// A lane's status line is ropeshift's alone when only the person's own settings name one, and it still
// composes the line the repository's settings name, because the repository's files are what a lane runs on.
func TestALanesStatusLineLeavesOutThePersonsOwn(t *testing.T) {
	home := t.TempDir()
	writeStatusLine(t, filepath.Join(home, ".claude", "settings.json"), "personal-line")
	spec := laneSpec(t)
	spec.RunMode = ports.RunModeInteractive
	spec.Env = []string{"HOME=" + home}
	if line := composedStatusLine(spec); strings.Contains(line.Command, "personal-line") {
		t.Errorf("a lane's status line runs the person's own command: %q", line.Command)
	}
	writeStatusLine(t, filepath.Join(spec.Dir, ".claude", "settings.json"), "project-line")
	if line := composedStatusLine(spec); !strings.Contains(line.Command, "project-line") {
		t.Errorf("a lane's status line leaves out the repository's command: %q", line.Command)
	}
}

func writeStatusLine(t *testing.T, path, command string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"`+command+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func countOf(args []string, flag string) int {
	count := 0
	for _, arg := range args {
		if arg == flag {
			count++
		}
	}
	return count
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A repository's own `.mcp.json` is project setup, so a lane keeps its servers: the driver reads it from the lane's
// worktree and passes its servers in ropeshift's own MCP configuration, which `--strict-mcp-config` loads alone. A
// server the repository names after ropeshift's own does not replace it.
func TestALaneKeepsItsWorktreesOwnMCPServers(t *testing.T) {
	spec := laneSpec(t)
	writeFile(t, filepath.Join(spec.Dir, ".mcp.json"), `{"mcpServers":{
		"docs":{"type":"http","url":"https://docs.example.test/mcp","headers":{"Authorization":"Bearer ${DOCS_TOKEN}"}},
		"ropeshift":{"type":"stdio","command":"/elsewhere/ropeshift","args":["mcp"]}}}`)
	if err := writeGenerated(spec); err != nil {
		t.Fatal(err)
	}
	_, path := generatedPaths(spec)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if docs := file.Servers["docs"]; docs.Type != "http" || docs.URL != "https://docs.example.test/mcp" || docs.Headers["Authorization"] != "Bearer ${DOCS_TOKEN}" {
		t.Errorf("the worktree's server reached the lane as %+v", docs)
	}
	if ours := file.Servers[ServerName]; ours.Command != spec.Binary {
		t.Errorf("the worktree replaced ropeshift's own server: %+v", ours)
	}
}

// A worktree whose `.mcp.json` does not parse gives a lane ropeshift's server alone, and the lane still starts.
func TestAnUnreadableMCPFileGivesALaneOnlyRopeshiftsServer(t *testing.T) {
	spec := laneSpec(t)
	writeFile(t, filepath.Join(spec.Dir, ".mcp.json"), `{"mcpServers":`)
	if err := writeGenerated(spec); err != nil {
		t.Fatal(err)
	}
	_, path := generatedPaths(spec)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Servers) != 1 || file.Servers[ServerName] == nil {
		t.Errorf("the lane's servers are %s (%v)", raw, err)
	}
}

// A lane keeps the repository's CLAUDE.md and leaves out the member's own instructions: their `CLAUDE.md` and rules,
// as absolute paths under their configuration directory, the one `CLAUDE_CONFIG_DIR` names when it is set.
func TestALaneLeavesOutTheMembersOwnInstructions(t *testing.T) {
	spec := laneSpec(t)
	spec.Env = []string{"HOME=/home/member"}
	want := []string{"/home/member/.claude/CLAUDE.md", "/home/member/.claude/rules/**"}
	if got := generateSettings(spec).ClaudeMdExcludes; !slices.Equal(got, want) {
		t.Errorf("a lane excludes %v, want %v", got, want)
	}
	spec.Env = append(spec.Env, "CLAUDE_CONFIG_DIR=/config/claude")
	want = []string{"/config/claude/CLAUDE.md", "/config/claude/rules/**"}
	if got := generateSettings(spec).ClaudeMdExcludes; !slices.Equal(got, want) {
		t.Errorf("a lane with a configuration directory excludes %v, want %v", got, want)
	}
	for _, pattern := range generateSettings(spec).ClaudeMdExcludes {
		if matched, _ := filepath.Match(pattern, filepath.Join(spec.Dir, "CLAUDE.md")); matched {
			t.Errorf("a lane leaves out the repository's own CLAUDE.md with %s", pattern)
		}
	}
}
