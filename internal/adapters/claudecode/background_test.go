package claudecode

import (
	"encoding/json"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// TestEverySessionRunsItsHelpersInTheForeground holds the guard behind the worker's rest on background work: the
// session's environment turns the CLI's background tasks off, so a subagent or a command a session starts runs in
// its turn and the turn's end is the session's answer (https://code.claude.com/docs/en/env-vars,
// https://code.claude.com/docs/en/sub-agents). A command that reaches its timeout then stops instead of moving to the
// background (https://code.claude.com/docs/en/tools-reference#foreground-commands-that-move-to-the-background), so the
// ceiling a session may ask for is raised past a long build's run (`BASH_MAX_TIMEOUT_MS`,
// https://code.claude.com/docs/en/tools-reference#timeout-and-output-limits). Both go in the environment the launcher
// sets, as the roster's variables do, and the settings file sets neither.
func TestEverySessionRunsItsHelpersInTheForeground(t *testing.T) {
	want := []string{"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1", "BASH_MAX_TIMEOUT_MS=1800000"}
	spec := policySpec(t)
	for name, env := range map[string][]string{
		"a pane":             paneEnv(spec, Use{}),
		"a headless session": sessionEnv(spec, Use{}),
		"a session with no roster": func() []string {
			bare := spec
			bare.Agents = nil
			return paneEnv(bare, Use{})
		}(),
	} {
		for _, entry := range want {
			if !slices.Contains(env, entry) {
				t.Errorf("%s: the environment leaves out %s: %q", name, entry, env)
			}
		}
	}
	for _, run := range []string{ports.RunModeInteractive, ports.RunModeHeadless} {
		spec.RunMode = run
		raw, err := json.Marshal(generateSettings(spec))
		if err != nil {
			t.Fatal(err)
		}
		var file map[string]json.RawMessage
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		if env, set := file["env"]; set {
			t.Errorf("%s: the settings file sets env %s", run, env)
		}
	}
}
