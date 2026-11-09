package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

const compactedPayload = `{"session_id":"s1","hook_event_name":"PostCompact","trigger":"auto","compact_summary":"the conversation in full"}`

func groundingOf(t *testing.T, stdout string) string {
	t.Helper()
	var output struct {
		Specific struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("%q: %v", stdout, err)
	}
	return output.Specific.Context
}

func withGround(t *testing.T, env hookEnv, ground lanes.Grounding) hookEnv {
	t.Helper()
	raw, err := json.Marshal(lanes.DenyPolicy{Deny: []lanes.DenyRule{}, Ground: ground})
	if err != nil {
		t.Fatal(err)
	}
	env.policy = filepath.Join(filepath.Dir(env.socket), "policy.json")
	if err := os.WriteFile(env.policy, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return env
}

// A session that lost its place reads who it is from a file, so this needs no worker.
func TestReGroundingNamesTheContractAndThePack(t *testing.T) {
	env := withGround(t, newHookEnv(t), lanes.Grounding{Contract: "checkout", ContractVersion: 4, Pack: "/work/pack.md"})
	context := groundingOf(t, runHook(t, env, "SessionStart", `{"session_id":"s1","source":"compact"}`).stdout)
	for _, part := range []string{"lane-1", "generation 3", "wo", "checkout", "version 4", "/work/pack.md"} {
		if !strings.Contains(context, part) {
			t.Errorf("%q in %q", part, context)
		}
	}
}

// A process's tags cannot change, so a cleared session hears its generation from the worker.
func TestReGroundingAfterAClearNamesTheCurrentGeneration(t *testing.T) {
	env := withGround(t, newHookEnv(t), lanes.Grounding{})
	w := newLaneWorker(t, env)
	stored, err := w.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	stored.Generation = 7
	if err := w.journal.SaveLane(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	context := groundingOf(t, runHook(t, env, "SessionStart", `{"session_id":"s2","source":"clear"}`).stdout)
	if !strings.Contains(context, "generation 7") {
		t.Errorf("context %q", context)
	}
}

// With no worker a clear's generation cannot be known, and a wrong number is worse than none.
func TestReGroundingAfterAClearWithNoWorkerNamesNoGeneration(t *testing.T) {
	context := groundingOf(t, runHook(t, newHookEnv(t), "SessionStart", `{"session_id":"s2","source":"clear"}`).stdout)
	if strings.Contains(context, "generation") || !strings.Contains(context, "lane-1") {
		t.Errorf("context %q", context)
	}
}

// A summary holds the conversation, so it reaches neither the worker nor the spool.
func TestACompactionSummaryNeverLeavesTheHook(t *testing.T) {
	down := newHookEnv(t)
	if run := runHook(t, down, "PostCompact", compactedPayload); run.stdout != "" {
		t.Errorf("stdout %q", run.stdout)
	}
	raw, err := os.ReadFile(down.spool)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "the conversation") || !strings.Contains(string(raw), "PostCompact") {
		t.Errorf("spool %s", raw)
	}
	up := newHookEnv(t)
	w := newLaneWorker(t, up)
	runHook(t, up, "PostCompact", compactedPayload)
	records := w.hooks(t)
	if len(records) != 1 || records[0].Detail != "auto" {
		t.Fatalf("records %+v", records)
	}
	stored, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "the conversation") {
		t.Errorf("the worker holds %s", stored)
	}
}
