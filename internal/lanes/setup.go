package lanes

// Setup is what a lane's session loaded at its start, as its agent CLI reported it, and the pieces of the member's own
// CLI setup among them. A lane runs clean of that setup, so Personal names what reached it despite the flags that keep
// it out: the CLI's own record of a session is what tells a leak from a clean start.
type Setup struct {
	// Generation is the generation whose session reported this, and Version the agent CLI version it ran.
	Generation int64          `json:"generation"`
	Version    string         `json:"version,omitempty"`
	Model      string         `json:"model,omitempty"`
	Tools      []string       `json:"tools,omitempty"`
	MCPServers []SetupServer  `json:"mcp_servers,omitempty"`
	Agents     []string       `json:"agents,omitempty"`
	Skills     []string       `json:"skills,omitempty"`
	Plugins    []string       `json:"plugins,omitempty"`
	Personal   []PersonalItem `json:"personal,omitempty"`
}

// SetupServer is one MCP server a session loaded, and where it came from.
type SetupServer struct {
	Name string `json:"name"`
	From string `json:"from"`
}

// Where a loaded MCP server came from: ropeshift's own server, a server the lane's worktree names in its `.mcp.json`,
// or, for any other, the scope the CLI reported for it.
const (
	FromRopeshift  = "ropeshift"
	FromRepository = "repository"
)

// PersonalItem is one piece of the member's own CLI setup that a session loaded. From says where the piece is set up,
// such as a file under the member's configuration directory or the scope of an MCP server.
type PersonalItem struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	From string `json:"from,omitempty"`
}

// The kinds of a personal piece.
const (
	PieceMCPServer = "mcp_server"
	PiecePlugin    = "plugin"
	PieceAgent     = "agent"
	PieceSkill     = "skill"
	PieceCommand   = "command"
)

// What a member can do about a piece of their own setup that leaked into a machine's lanes (ADR-0040). Lanes keep
// running whichever they choose.
const (
	// SetupAccept accepts the piece for the machine's lanes on that version, so it is not raised again there.
	SetupAccept = "accept"
	// SetupSeparateDirectory runs the machine's lanes in a configuration directory ropeshift owns.
	SetupSeparateDirectory = "separate_directory"
	// SetupRemove is the member removing the piece from their own setup, which ropeshift never edits.
	SetupRemove = "remove"
)

// SetupAdvice is the choice a member is recommended for one piece of their own setup that leaked, and why. An agent, a
// skill, or a command adds context, and the lane's generated rules still bound its tool calls, so the lane keeps moving
// with it accepted. An MCP server or a plugin, which can carry hooks and servers, runs commands or reaches data outside
// the project with no person watching, so the recommendation is the separate configuration directory, the one choice
// that keeps it out of unattended lanes without changing the member's own sessions.
func SetupAdvice(item PersonalItem) (choice, rationale string) {
	if item.Kind == PieceMCPServer || item.Kind == PiecePlugin {
		return SetupSeparateDirectory, "it can run commands or reach data outside the project with no person watching, and a separate configuration directory keeps it out of unattended lanes without changing your own sessions"
	}
	return SetupAccept, "it adds context, and the lane's generated rules still bound every tool call it leads to, so lanes keep moving with it named on each"
}
