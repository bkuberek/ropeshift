package lanes

import "testing"

// Only an agent, a skill, or a command, which adds context, is recommended for accepting. Every other kind, an MCP
// server, a plugin, and a kind this build does not know, is recommended for the separate configuration directory, so a
// piece nobody judged never leads with the choice that lets it keep running in unattended lanes.
func TestSetupAdviceAcceptsOnlyTheKindsThatAddContext(t *testing.T) {
	for kind, want := range map[string]string{
		PieceAgent: SetupAccept, PieceSkill: SetupAccept, PieceCommand: SetupAccept,
		PieceMCPServer: SetupSeparateDirectory, PiecePlugin: SetupSeparateDirectory,
		"hook": SetupSeparateDirectory, "": SetupSeparateDirectory,
	} {
		if choice, why := SetupAdvice(PersonalItem{Kind: kind, Name: "piece"}); choice != want || why == "" {
			t.Errorf("a %q piece is recommended %q (%q), want %q", kind, choice, why, want)
		}
	}
}
