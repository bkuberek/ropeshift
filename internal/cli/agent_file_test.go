package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/roster"
)

// agentCase is one text of an agent's definition and what `agent add --file` makes of it: the agent it reads, or a
// refusal. web/testdata/agent-toml.json holds the cases, and web/src/admin/logic.test.ts holds the web UI's reading
// of the same text to the same result, so pasting a definition in the browser and naming it in a file agree.
type agentCase struct {
	Name    string        `json:"name"`
	TOML    string        `json:"toml"`
	Agent   *roster.Agent `json:"agent"`
	Refused bool          `json:"refused"`
}

// plain is an agent with no nil list or map, so two agents that say the same thing compare equal.
func plain(agent roster.Agent) roster.Agent {
	if agent.Tools.Allow == nil {
		agent.Tools.Allow = []string{}
	}
	if agent.Tools.Deny == nil {
		agent.Tools.Deny = []string{}
	}
	if len(agent.Models) == 0 {
		agent.Models = nil
	}
	return agent
}

func TestAnAgentFileReadsTheWayTheWebUIReadsPastedText(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "testdata", "agent-toml.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []agentCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("%d cases, so the file read too few", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.toml")
			if err := os.WriteFile(path, []byte(c.TOML), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := cli.ReadAgentFile(path)
			switch {
			case c.Refused && err == nil:
				t.Errorf("the file was read as %+v, and the case says it is refused", got)
			case !c.Refused && err != nil:
				t.Errorf("the file was refused: %v", err)
			case !c.Refused && !reflect.DeepEqual(plain(got), plain(*c.Agent)):
				t.Errorf("the file reads as\n%+v\nand the case says\n%+v", plain(got), plain(*c.Agent))
			}
		})
	}
}
