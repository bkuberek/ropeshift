package hook

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
)

// reloadInput is what the agent CLI gives a `ConfigChange` hook when a lane's worktree changes its project settings.
const reloadInput = `{"session_id": "s", "hook_event_name": "ConfigChange", "source": "project_settings", "file_path": "/w/.claude/settings.json"}`

// reloadDecision runs a `ConfigChange` hook against a socket and says what it told the agent CLI: "block" for a JSON
// block, and "" for no answer, which lets the change apply.
func reloadDecision(t *testing.T, socket string) string {
	t.Helper()
	env := map[string]string{api.EnvWorkerSocket: socket, api.EnvLaneToken: "a-token", api.EnvLaneID: "lane-1", api.EnvGeneration: "1"}
	var out bytes.Buffer
	if code := Main([]string{lanes.EventConfigChange}, strings.NewReader(reloadInput), &out, func(name string) string { return env[name] }); code != 0 {
		t.Fatalf("the hook exited %d", code)
	}
	if out.Len() == 0 {
		return ""
	}
	var answer struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
		t.Fatalf("the hook wrote %q: %v", out.String(), err)
	}
	if answer.Reason == "" {
		t.Errorf("a block gives no reason: %s", out.String())
	}
	return answer.Decision
}

// A `ConfigChange` hook lets a change apply only when the worker says it may, and blocks it on every other answer: a
// change the worker blocks, a refusal, a worker that cannot be reached, and a reply that says nothing of the reload.
// Blocking is the top-level decision the CLI reads for this event ([hooks]), and the hook asks the worker once, by its
// token, with nothing of the change in the line.
//
// [hooks]: https://code.claude.com/docs/en/hooks#configchange-decision-control
func TestAReloadAppliesOnlyWhenTheWorkerAllowsIt(t *testing.T) {
	allowed, worker := aScriptedWorker(t, lanes.Reply{OK: true, Reload: lanes.ReloadAllow})
	if got := reloadDecision(t, allowed); got != "" {
		t.Errorf("an allowed reload was answered %q", got)
	}
	lines := worker.taken()
	if len(lines) != 1 || !lines[0].Reload || lines[0].Token != "a-token" || lines[0].Hook != nil {
		t.Errorf("the worker took %+v", lines)
	}
	for name, reply := range map[string]lanes.Reply{
		"a block":     {OK: true, Reload: lanes.ReloadBlock},
		"a refusal":   {Code: lanes.CodeInvalid, Message: "this line asks for nothing"},
		"no decision": {OK: true},
	} {
		socket, _ := aScriptedWorker(t, reply)
		if got := reloadDecision(t, socket); got != "block" {
			t.Errorf("%s was answered %q", name, got)
		}
	}
	if got := reloadDecision(t, filepath.Join(t.TempDir(), "gone.sock")); got != "block" {
		t.Errorf("a reload with no worker was answered %q", got)
	}
}
