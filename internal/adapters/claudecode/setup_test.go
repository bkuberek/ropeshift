package claudecode

import (
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

// What a session's init reports is what it loaded: the driver names where each MCP server came from, and the pieces
// of the member's own setup among them. A piece the repository or ropeshift also supplies, and a built-in one, is not
// the member's.
func TestTheInitNamesWhatTheSessionLoadedAndWhatOfItIsTheMembers(t *testing.T) {
	home := t.TempDir()
	spec := laneSpec(t)
	spec.Env = []string{"HOME=" + home}
	spec.Agents = sessionAgents(roster.SessionWriting)
	config := filepath.Join(home, ".claude")
	writeFile(t, filepath.Join(config, "agents", "review", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews\n---\nReview.\n")
	writeFile(t, filepath.Join(config, "agents", "heavy-implementation.md"), "---\nname: heavy-implementation\n---\nMine.\n")
	writeFile(t, filepath.Join(config, "agents", "shared.md"), "---\nname: shared\n---\nMine too.\n")
	writeFile(t, filepath.Join(config, "skills", "pdf", "SKILL.md"), "---\nname: pdf\n---\nPDFs.\n")
	writeFile(t, filepath.Join(config, "commands", "deploy.md"), "Deploy.\n")
	writeFile(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"formatter@market":true,"team-tools@market":true}}`)
	writeFile(t, filepath.Join(spec.Dir, ".claude", "agents", "shared.md"), "---\nname: shared\n---\nThe repository's.\n")
	writeFile(t, filepath.Join(spec.Dir, ".claude", "settings.json"), `{"enabledPlugins":{"team-tools@market":true}}`)
	spec.Repository = spec.Dir
	spec.Base = committed(t, spec.Dir, map[string]string{".mcp.json": `{"mcpServers":{"docs":{"type":"http","url":"https://docs.example.test/mcp"}}}`})
	s := &session{events: make(chan ports.DriverEvent, 4), setup: setupContextOf(spec)}
	s.take([]byte(`{"type":"system","subtype":"init","session_id":"s-1","model":"claude-opus-5-5","claude_code_version":"2.1.330",
		"tools":["Read","Bash","mcp__github__issues"],
		"mcp_servers":[{"name":"ropeshift","status":"connected","source":"dynamic"},{"name":"docs","status":"connected","source":"dynamic"},
			{"name":"github","status":"connected","source":"user"},{"name":"claude.ai Gmail","status":"connected","source":"claudeai"}],
		"agents":["general-purpose","heavy-implementation","reviewer","shared"],
		"skills":["pdf","simplify"],"slash_commands":["deploy","review"],
		"plugins":[{"name":"formatter","path":"/x/formatter"},{"name":"team-tools","path":"/x/team-tools"}]}`), 1)
	event := <-s.events
	if event.Kind != ports.EventSessionBound || event.Setup == nil {
		t.Fatalf("the init gave %+v", event)
	}
	got := *event.Setup
	wantServers := []lanes.SetupServer{
		{Name: "ropeshift", From: lanes.FromRopeshift}, {Name: "docs", From: lanes.FromRepository},
		{Name: "github", From: "user"}, {Name: "claude.ai Gmail", From: "claudeai"},
	}
	if got.Model != "claude-opus-5-5" || got.Version != "2.1.330" || !slices.Equal(got.MCPServers, wantServers) || !slices.Equal(got.Plugins, []string{"formatter", "team-tools"}) ||
		!slices.Equal(got.Tools, []string{"Read", "Bash", "mcp__github__issues"}) || len(got.Agents) != 4 || len(got.Skills) != 2 {
		t.Errorf("the session loaded %+v", got)
	}
	wantPersonal := []lanes.PersonalItem{
		{Kind: lanes.PieceMCPServer, Name: "github", From: "user"},
		{Kind: lanes.PieceMCPServer, Name: "claude.ai Gmail", From: "claudeai"},
		{Kind: lanes.PiecePlugin, Name: "formatter", From: "~/.claude/settings.json"},
		{Kind: lanes.PieceAgent, Name: "reviewer", From: "~/.claude/agents/review/reviewer.md"},
		{Kind: lanes.PieceSkill, Name: "pdf", From: "~/.claude/skills/pdf"},
		{Kind: lanes.PieceCommand, Name: "deploy", From: "~/.claude/commands/deploy.md"},
	}
	if !slices.Equal(got.Personal, wantPersonal) {
		t.Errorf("the member's own pieces are\n %+v\nwant\n %+v", got.Personal, wantPersonal)
	}
}

// A session whose init names nothing of the member's own is clean, and says so with no personal piece.
func TestACleanInitNamesNoPersonalPiece(t *testing.T) {
	spec := laneSpec(t)
	spec.Env = []string{"HOME=" + t.TempDir()}
	s := &session{events: make(chan ports.DriverEvent, 4), setup: setupContextOf(spec)}
	s.take([]byte(`{"type":"system","subtype":"init","session_id":"s-1","model":"m","tools":["Read"],
		"mcp_servers":[{"name":"ropeshift","status":"connected","source":"dynamic"}],"agents":["general-purpose"],"skills":[],"plugins":[]}`), 1)
	event := <-s.events
	if event.Setup == nil || len(event.Setup.Personal) != 0 {
		t.Errorf("a clean init gave %+v", event.Setup)
	}
}

// A piece a lane adds to its worktree is not the repository's until it is merged, so it never hides one of the member's
// own: the repository's pieces are those its `.claude` directory holds at the lane's base, read from the clone.
func TestAPieceALaneAddsHidesNoneOfTheMembers(t *testing.T) {
	home := t.TempDir()
	spec := laneSpec(t)
	spec.Env = []string{"HOME=" + home}
	config := filepath.Join(home, ".claude")
	writeFile(t, filepath.Join(config, "agents", "reviewer.md"), "---\nname: reviewer\n---\nMine.\n")
	writeFile(t, filepath.Join(config, "skills", "pdf", "SKILL.md"), "---\nname: pdf\n---\nMine.\n")
	writeFile(t, filepath.Join(config, "skills", "tables", "SKILL.md"), "---\nname: tables\n---\nMine.\n")
	writeFile(t, filepath.Join(config, "commands", "deploy.md"), "Mine.\n")
	writeFile(t, filepath.Join(config, "commands", "ship.md"), "Mine.\n")
	writeFile(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"formatter@market":true,"team-tools@market":true}}`)
	spec.Repository = spec.Dir
	spec.Base = committed(t, spec.Dir, map[string]string{
		".claude/skills/synced/tables/SKILL.md": "---\nname: tables\n---\nThe repository's.\n",
		".claude/commands/release/ship.md":      "The repository's.\n",
		".claude/settings.json":                 `{"enabledPlugins":{"team-tools@market":true}}`,
	})
	writeFile(t, filepath.Join(spec.Dir, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\n---\nThe lane's.\n")
	writeFile(t, filepath.Join(spec.Dir, ".claude", "skills", "pdf", "SKILL.md"), "---\nname: pdf\n---\nThe lane's.\n")
	writeFile(t, filepath.Join(spec.Dir, ".claude", "commands", "deploy.md"), "The lane's.\n")
	writeFile(t, filepath.Join(spec.Dir, ".claude", "settings.json"), `{"enabledPlugins":{"formatter@market":true,"team-tools@market":true}}`)
	s := &session{events: make(chan ports.DriverEvent, 4), setup: setupContextOf(spec)}
	s.take([]byte(`{"type":"system","subtype":"init","session_id":"s-1","model":"m","claude_code_version":"2.1.330",
		"mcp_servers":[{"name":"ropeshift","status":"connected","source":"dynamic"}],
		"agents":["reviewer"],"skills":["pdf","tables"],"slash_commands":["deploy","ship"],
		"plugins":[{"name":"formatter","path":"/x/formatter"},{"name":"team-tools","path":"/x/team-tools"}]}`), 1)
	event := <-s.events
	if event.Setup == nil {
		t.Fatalf("the init gave %+v", event)
	}
	want := []lanes.PersonalItem{
		{Kind: lanes.PiecePlugin, Name: "formatter", From: "~/.claude/settings.json"},
		{Kind: lanes.PieceAgent, Name: "reviewer", From: "~/.claude/agents/reviewer.md"},
		{Kind: lanes.PieceSkill, Name: "pdf", From: "~/.claude/skills/pdf"},
		{Kind: lanes.PieceCommand, Name: "deploy", From: "~/.claude/commands/deploy.md"},
	}
	if !slices.Equal(event.Setup.Personal, want) {
		t.Errorf("the member's own pieces are\n %+v\nwant\n %+v", event.Setup.Personal, want)
	}
}
