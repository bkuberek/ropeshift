package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/test/apitest"
)

// A lane's rules read only an agent's `tools.deny`, so a category taken out of `tools.allow` still reaches a lane.
// `agent set` says so when it drops one, and names `tools.deny` as the way to keep a category out of lanes.
func TestAgentSetWarnsThatDroppingACategoryFromAllowKeepsItNotOutOfLanes(t *testing.T) {
	s := newSession(t)
	code, stdout, stderr := s.run("agent", "set", "analysis", "tools.allow=read,search", "--project", apitest.ProjectName)
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "roster version") {
		t.Errorf("the change was not printed: %q", stdout)
	}
	for _, want := range []string{"tools.allow", "shell, context", "tools.deny"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the warning %q does not say %q", stderr, want)
		}
	}
}

// A change that drops no category from `tools.allow`, and one to `tools.deny`, warn of nothing, and the warning stays
// out of the JSON a script reads.
func TestAgentSetWarnsOfNothingWhenNoCategoryIsDropped(t *testing.T) {
	s := newSession(t)
	for _, args := range [][]string{
		{"agent", "set", "analysis", "tools.allow=context,read,search,shell", "--project", apitest.ProjectName},
		{"agent", "set", "analysis", "tools.deny=web", "--project", apitest.ProjectName},
		{"agent", "set", "analysis", "effort=low", "--project", apitest.ProjectName},
	} {
		if code, _, stderr := s.run(args...); code != cli.ExitOK || stderr != "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
	code, stdout, stderr := s.run("agent", "set", "lookup", "tools.allow=read", "--project", apitest.ProjectName, "--json")
	if code != cli.ExitOK || !json.Valid([]byte(stdout)) {
		t.Fatalf("exit %d: %q %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "tools.deny") {
		t.Errorf("the warning is not on stderr: %q", stderr)
	}
}
