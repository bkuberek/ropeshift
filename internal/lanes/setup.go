package lanes

import (
	"path"
	"strings"
)

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

// The bounds of what a lane records of what its session loaded: the entries each list keeps, and the bytes each name,
// version, or source keeps.
const (
	MaxSetupEntries = 512
	MaxSetupText    = 512
)

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
	// PieceInstruction is an instruction file a session loaded from outside its worktree: a CLAUDE.md of a directory
	// above the clone, or the member's own, which a lane's settings leave out.
	PieceInstruction = "instruction"
)

// EventInstructionsLoaded is the hook that names each instruction file a session loads, with the scope the agent CLI
// gives it ([hooks]).
//
// [hooks]: https://code.claude.com/docs/en/hooks#instructionsloaded
const EventInstructionsLoaded = "InstructionsLoaded"

// EventConfigChange is the hook the agent CLI runs when a settings file or a skill changes during a session, before it
// applies the change, and whose block keeps the change from the running session ([hooks]).
//
// [hooks]: https://code.claude.com/docs/en/hooks#configchange
const EventConfigChange = "ConfigChange"

// scopeManaged is the scope of an organization's instruction file, which no member's setting can leave out.
const scopeManaged = "Managed"

// InstructionLeak says an instruction file a session loaded is none of the repository's: a file outside every directory
// the lane's worktree is known by, unless it is the organization's. A lane loads only the repository's own
// instructions, so any other is a leak (ADR-0040).
func InstructionLeak(worktrees []string, file, scope string) (PersonalItem, bool) {
	if file == "" || scope == scopeManaged {
		return PersonalItem{}, false
	}
	clean := path.Clean(file)
	for _, dir := range worktrees {
		if dir != "" && strings.HasPrefix(clean, strings.TrimSuffix(path.Clean(dir), "/")+"/") {
			return PersonalItem{}, false
		}
	}
	return PersonalItem{Kind: PieceInstruction, Name: file, From: scope}, true
}

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
// skill, a command, or an instruction file adds context, and the lane's generated rules still bound its tool calls, so
// the lane keeps moving with it accepted. Any other piece, an MCP server or a plugin, which can carry hooks and servers, and a kind this build
// does not know, can run commands or reach data outside the project with no person watching, so the recommendation is
// the separate configuration directory, the one choice that keeps it out of unattended lanes without changing the
// member's own sessions.
func SetupAdvice(item PersonalItem) (choice, rationale string) {
	switch item.Kind {
	case PieceAgent, PieceSkill, PieceCommand, PieceInstruction:
		return SetupAccept, "it adds context, and the lane's generated rules still bound every tool call it leads to, so lanes keep moving with it named on each"
	}
	return SetupSeparateDirectory, "it can run commands or reach data outside the project with no person watching, and a separate configuration directory keeps it out of unattended lanes without changing your own sessions"
}
