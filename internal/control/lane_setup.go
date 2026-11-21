package control

import (
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// recordedSetup is what a lane keeps of what its session reported it loaded. The agent CLI's report names what the
// repository's files and the member's own setup hold, so it is a stranger's text that reaches the ledger and every view
// of the lane: each name, version, and source is plain text on one line, with the shapes of credentials taken out, held
// to lanes.MaxSetupText bytes afterwards, because a marker can be longer than what it replaces, and each list keeps its
// first lanes.MaxSetupEntries entries.
func recordedSetup(in lanes.Setup) lanes.Setup {
	out := lanes.Setup{
		Generation: in.Generation, Version: setupText(in.Version), Model: setupText(in.Model),
		Tools: setupTexts(in.Tools), Agents: setupTexts(in.Agents), Skills: setupTexts(in.Skills), Plugins: setupTexts(in.Plugins),
	}
	for _, server := range firstEntries(in.MCPServers) {
		out.MCPServers = append(out.MCPServers, lanes.SetupServer{Name: setupText(server.Name), From: setupText(server.From)})
	}
	for _, item := range firstEntries(in.Personal) {
		out.Personal = append(out.Personal, lanes.PersonalItem{Kind: setupText(item.Kind), Name: setupText(item.Name), From: setupText(item.From)})
	}
	return out
}

func setupText(text string) string {
	text = strings.ReplaceAll(oneLine.Replace(ledger.RedactCredentials(ledger.PlainText(text))), "\t", " ")
	return clipText(strings.TrimSpace(text), lanes.MaxSetupText)
}

func setupTexts(texts []string) []string {
	if len(texts) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(texts), lanes.MaxSetupEntries))
	for _, text := range firstEntries(texts) {
		out = append(out, setupText(text))
	}
	return out
}

func firstEntries[T any](items []T) []T { return items[:min(len(items), lanes.MaxSetupEntries)] }
