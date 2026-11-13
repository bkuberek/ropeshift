package contextpacks_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/contextpacks"
)

// The role is the same bytes for every generation of a conversation, so each reads it from cache, and it
// states what the assistant may do and how it says it.
func TestTheAssistantsRoleStatesHowItActs(t *testing.T) {
	role := contextpacks.AssistantRole("Sam Rivera", "harbor", "/opt/ropeshift/bin/ropeshift")
	if role != contextpacks.AssistantRole("Sam Rivera", "harbor", "/opt/ropeshift/bin/ropeshift") {
		t.Fatal("the role is not the same bytes twice")
	}
	for _, want := range []string{
		"You act for Sam Rivera in harbor", "running `/opt/ropeshift/bin/ropeshift`", "`/opt/ropeshift/bin/ropeshift approval show <id>`", "by that path",
		"exits 10", "--why", "a card waits", "unless its state says so",
		"[[<kind>:<id>]]", "[[code:<repository>:<path>#L<line>@<commit>]]", "cannot ground", "no `ask`",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("the role lacks %q", want)
		}
	}
}

func TestTheAssistantsBriefStatesTheProjectAndTheConversationSoFar(t *testing.T) {
	brief := contextpacks.AssistantBrief(contextpacks.AssistantFacts{
		ProjectName:  "harbor",
		Repositories: []contextpacks.AssistantRepository{{Name: "harbor-api", Base: "main", Path: "/run/x/repositories/harbor-api"}, {Name: "harbor-web", Base: "trunk", Detail: "no checkout"}},
		Plans:        []string{"WO-1 Plan the harbor"},
		Work:         map[string]int64{"running": 2, "ready": 1},
		History:      []contextpacks.AssistantHistory{{Kind: "member", Body: "Which merges wait?"}, {Kind: "reply", Body: "Two."}},
	})
	for _, want := range []string{
		"harbor", "harbor-api", "main", "/run/x/repositories/harbor-api", "harbor-web", "no checkout", "WO-1 Plan the harbor",
		"ready 1", "running 2", "earlier conversation", "Which merges wait?", "Two.",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	if strings.Contains(contextpacks.AssistantBrief(contextpacks.AssistantFacts{ProjectName: "harbor"}), "earlier conversation") {
		t.Error("a brief with no history names an earlier conversation")
	}
}
