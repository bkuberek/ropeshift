package claudecode

import (
	"encoding/json"
	"testing"
)

// TestEverySessionRunsItsHelpersInTheForeground holds the guard behind the worker's wait on background work:
// the generated settings turn the CLI's background tasks off, so a subagent or a command a session starts runs
// in its turn and the turn's end is the session's answer
// (https://code.claude.com/docs/en/env-vars, https://code.claude.com/docs/en/sub-agents). `--settings` sits above
// a repository's settings files, and its `env` overwrites the variable a shell exported
// (https://code.claude.com/docs/en/settings#settings-precedence, https://code.claude.com/docs/en/settings-reference#env).
func TestEverySessionRunsItsHelpersInTheForeground(t *testing.T) {
	for name, spec := range map[string]func() settingsFile{
		"a lane":           func() settingsFile { return generateSettings(policySpec(t)) },
		"one with no hook": func() settingsFile { spec := policySpec(t); spec.Binary = ""; return generateSettings(spec) },
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(spec())
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Env map[string]string `json:"env"`
			}
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatal(err)
			}
			if got := file.Env["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]; got != "1" {
				t.Errorf("CLAUDE_CODE_DISABLE_BACKGROUND_TASKS is %q in %s, want 1", got, raw)
			}
		})
	}
}
