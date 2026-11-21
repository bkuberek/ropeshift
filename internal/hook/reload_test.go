package hook

import (
	"bytes"
	"encoding/json"
	"io"
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
	return reloadDecisionOf(t, out.String())
}

// reloadDecisionOf reads what a `ConfigChange` hook wrote: "block" for a JSON block, and "" for nothing.
func reloadDecisionOf(t *testing.T, out string) string {
	t.Helper()
	if out == "" {
		return ""
	}
	var answer struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatalf("the hook wrote %q: %v", out, err)
	}
	if answer.Reason == "" {
		t.Errorf("a block gives no reason: %s", out)
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

// panicsReading is a hook's input that panics when the hook reads it.
type panicsReading struct{}

func (panicsReading) Read([]byte) (int, error) { panic("the input cannot be read") }

// panicsOnce is a hook's output whose first write panics, as a hook that fails while it gives its answer does.
type panicsOnce struct {
	bytes.Buffer
	panicked bool
}

func (w *panicsOnce) Write(p []byte) (int, error) {
	if !w.panicked {
		w.panicked = true
		panic("the answer cannot be written")
	}
	return w.Buffer.Write(p)
}

// A `ConfigChange` hook that panics blocks the reload: whatever fails, the hook still gives the block it owes, because
// nothing on its output lets the change apply (ADR-0040).
func TestAReloadHookThatPanicsBlocksTheReload(t *testing.T) {
	blocked, _ := aScriptedWorker(t, lanes.Reply{OK: true, Reload: lanes.ReloadBlock})
	env := map[string]string{api.EnvWorkerSocket: blocked, api.EnvLaneToken: "a-token", api.EnvLaneID: "lane-1", api.EnvGeneration: "1"}
	for name, tt := range map[string]struct {
		input io.Reader
		out   *panicsOnce
	}{
		"while it reads its input":  {input: panicsReading{}, out: &panicsOnce{panicked: true}},
		"while it gives its answer": {input: strings.NewReader(reloadInput), out: new(panicsOnce)},
	} {
		t.Run(name, func(t *testing.T) {
			if code := Main([]string{lanes.EventConfigChange}, tt.input, tt.out, func(name string) string { return env[name] }); code != 0 {
				t.Fatalf("the hook exited %d", code)
			}
			if got := reloadDecisionOf(t, tt.out.String()); got != "block" {
				t.Errorf("a hook that panicked %s answered %q (%q)", name, got, tt.out.String())
			}
		})
	}
}

// A `ConfigChange` hook that reaches its deadline before the worker answers blocks the reload, and a worker's allow that
// comes after it adds no answer, so the agent CLI reads one.
func TestAReloadHookThatTimesOutBlocksTheReload(t *testing.T) {
	allowed, _ := aScriptedWorker(t, lanes.Reply{OK: true, Reload: lanes.ReloadAllow})
	cfg := config{event: eventConfigChange, socket: allowed, token: "a-token", out: new(outbox)}
	var out lockedBuffer
	event, _ := take(cfg, strings.NewReader(reloadInput))
	untilDecided(cfg, event, &out)
	cfg.out.settle(cfg)
	if got := reloadDecisionOf(t, out.String()); got != "block" {
		t.Fatalf("a reload still waiting on the worker at the deadline was answered %q", got)
	}
	before := out.String()
	answerReload(cfg, &out)
	if out.String() != before {
		t.Errorf("an answer after the deadline wrote %q", strings.TrimPrefix(out.String(), before))
	}
}
