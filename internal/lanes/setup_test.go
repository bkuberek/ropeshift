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

// An instruction file a session loaded is a leak only from outside the lane's worktree, by any name the worktree is
// known by, and never the organization's, which no setting leaves out.
func TestAnInstructionFileLeaksOnlyFromOutsideTheWorktree(t *testing.T) {
	worktrees := []string{"/var/lanes/l1", "/private/var/lanes/l1"}
	for path, want := range map[string]bool{
		"/var/lanes/l1/CLAUDE.md":                   false,
		"/private/var/lanes/l1/docs/CLAUDE.md":      false,
		"/var/lanes/CLAUDE.md":                      true,
		"/var/lanes/l10/CLAUDE.md":                  true,
		"/home/member/.claude/CLAUDE.md":            true,
		"/home/member/development/AGENTS.md":        true,
		"/private/var/lanes/l1/../outside/CLAUDE.md": true,
	} {
		item, leaks := InstructionLeak(worktrees, path, "Project")
		if leaks != want {
			t.Errorf("%s leaks %t, want %t", path, leaks, want)
		}
		if leaks && (item.Kind != PieceInstruction || item.Name != path || item.From != "Project") {
			t.Errorf("%s leaks as %+v", path, item)
		}
	}
	if _, leaks := InstructionLeak(worktrees, "/etc/claude-code/CLAUDE.md", "Managed"); leaks {
		t.Error("the organization's instruction file leaks")
	}
	if choice, _ := SetupAdvice(PersonalItem{Kind: PieceInstruction, Name: "/var/lanes/CLAUDE.md"}); choice != SetupAccept {
		t.Errorf("an instruction file is recommended %q", choice)
	}
}
