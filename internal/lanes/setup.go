package lanes

import "strings"

// Setup is what a lane's session loaded at its start, as its agent CLI reported it, and the pieces of the member's own
// CLI setup among them. A lane runs clean of that setup, so Personal names what reached it despite the flags that keep
// it out: the CLI's own record of a session is what tells a leak from a clean start.
type Setup struct {
	// Generation is the generation whose session reported this.
	Generation int64          `json:"generation"`
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

// SetupIgnore is the choice a member is recommended for a lane that loaded their own setup: leave the lane running this
// time, and open the setting so the next lanes do not load it. The one way to stop a running lane now is to cancel its
// work, which a leak does not warrant.
const SetupIgnore = "ignore"

// SetupAdvice is the choice a member is recommended for a lane that loaded their own setup, and why. The lane's own
// rules and hooks hold every call it makes, whatever loaded, so it runs on; an MCP server or a plugin of the member's
// can still reach outside the lane with their own access, which the rationale says.
func SetupAdvice(personal []PersonalItem) (choice, rationale string) {
	for _, item := range personal {
		if item.Kind == PieceMCPServer || item.Kind == PiecePlugin {
			return SetupIgnore, "the lane's rules and hooks still hold every call it makes, but an MCP server or a plugin of yours can reach outside the lane with your own access, so keep it out of the next lanes"
		}
	}
	return SetupIgnore, "agents, skills, and commands only add instructions, and the lane's own rules and hooks still hold every call it makes"
}

// pieceWords names each kind of piece as a sentence says it.
var pieceWords = map[string]string{
	PieceMCPServer: "MCP server",
	PiecePlugin:    "plugin",
	PieceAgent:     "agent",
	PieceSkill:     "skill",
	PieceCommand:   "command",
}

// PersonalWords says what of the member's setup a lane loaded, such as "MCP server github (user) and agent reviewer".
func PersonalWords(personal []PersonalItem) string {
	said := make([]string, 0, len(personal))
	for _, item := range personal {
		words := pieceWords[item.Kind]
		if words == "" {
			words = item.Kind
		}
		words += " " + item.Name
		if item.From != "" {
			words += " (" + item.From + ")"
		}
		said = append(said, words)
	}
	switch len(said) {
	case 0:
		return ""
	case 1:
		return said[0]
	}
	return strings.Join(said[:len(said)-1], ", ") + " and " + said[len(said)-1]
}
