package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/roster"
)

// TestAMemberEditsTheRosterOverridesItForAProjectAndReverts is FR-AGENT-4, FR-AGENT-5, and FR-AGENT-7 on
// the real daemon and command line: a member edits an agent's purpose, model, and instructions for the
// installation, overrides one field for one project, reads each scope's history with its difference, and
// reverts. A change names its scope, and a command with no member's session changes nothing.
func TestAMemberEditsTheRosterOverridesItForAProjectAndReverts(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	defer d.kill()
	in.waitReady(d)
	in.must("project", "create", "beta")
	instructions := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(instructions, []byte("Read the tests first, then the code they cover.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := in.run("agent", "set", "explore", "class=standard"); code != cli.ExitUsage || !strings.Contains(stderr, "names its scope") {
		t.Fatalf("a change with no scope: exit %d, %s", code, stderr)
	}
	if code, _, stderr := in.runWith(nil, "", nil, "agent", "set", "explore", "class=standard", "--installation"); code != cli.ExitForbidden {
		t.Fatalf("a change with no member's session: exit %d, %s", code, stderr)
	}
	in.must("agent", "set", "explore", "purpose=Search the whole tree, then report.", "models.claude-code=claude-sonnet-5-5",
		"instructions=@"+instructions, "--installation")
	in.must("agent", "set", "explore", "class=large", "--project", "beta")

	home := in.agent(ledger.DefaultProjectName, "explore")
	beta := in.agent("beta", "explore")
	if home.Purpose != "Search the whole tree, then report." || home.Models["claude-code"] != "claude-sonnet-5-5" ||
		home.Instructions != "Read the tests first, then the code they cover." || home.Class != roster.ClassSmall {
		t.Fatalf("the default project reads explore as %+v", home.Agent)
	}
	if beta.Class != roster.ClassLarge || beta.Sources["class"] != roster.SourceProject || beta.Sources["purpose"] != roster.SourceInstallation ||
		beta.Purpose != home.Purpose {
		t.Fatalf("beta reads explore as %+v", beta)
	}
	text := in.must("agent", "show", "explore", "--project", "beta")
	for _, want := range []string{"(project)", "(installation)", "runs on claude-code", "claude-sonnet-5-5"} {
		if !strings.Contains(text, want) {
			t.Errorf("show holds no %q:\n%s", want, text)
		}
	}
	if list := in.must("agent", "list", "--project", "beta"); !strings.Contains(list, "builtin+installation+project") {
		t.Errorf("the list names no source for explore:\n%s", list)
	}

	if code, _, stderr := in.run("agent", "disable", "implementation", "--project", "beta"); code != cli.ExitConflict || !strings.Contains(stderr, "default mapping") {
		t.Fatalf("disabling the default mapping's agent: exit %d, %s", code, stderr)
	}
	in.must("agent", "set", "explore", "effort=high", "--installation")
	var history api.RosterHistory
	if err := json.Unmarshal([]byte(in.must("agent", "history", "explore", "--installation", "--json")), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Versions) != 2 || history.Versions[0].Version.Version != 2 || !containsLine(history.Versions[0].Difference, "explore.effort (the layer below) -> high") {
		t.Fatalf("the installation's history %+v", history)
	}
	in.must("agent", "revert", "1", "--installation")
	if again := in.agent(ledger.DefaultProjectName, "explore"); again.Effort != roster.EffortMedium || again.Purpose != home.Purpose {
		t.Fatalf("the revert left explore as %+v", again.Agent)
	}
	if err := json.Unmarshal([]byte(in.must("agent", "history", "--project", "beta", "--json")), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Versions) != 1 || history.Versions[0].AuthoredBy == "" || history.Scope != roster.ScopeProject {
		t.Fatalf("beta's history %+v", history)
	}
	if guide := in.must("agent", "guide", "show", "--installation"); !strings.Contains(guide, "lightest agent") {
		t.Errorf("the routing guide reads %q", guide)
	}
}

// agent reads one agent of a project's resolved roster.
func (in *install) agent(project, name string) roster.ResolvedAgent {
	in.t.Helper()
	var view api.RosterView
	if err := json.Unmarshal([]byte(in.must("agent", "list", "--project", project, "--json")), &view); err != nil {
		in.t.Fatal(err)
	}
	agent, ok := view.Roster.Agent(name)
	if !ok {
		in.t.Fatalf("%s holds no agent %s: %+v", project, name, view.Roster.Names())
	}
	return agent
}

func containsLine(lines []string, part string) bool {
	for _, line := range lines {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}
