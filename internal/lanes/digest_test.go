package lanes_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

const worktree = "/work/lane"

func digestJSON(t *testing.T, digest lanes.Digest) string {
	t.Helper()
	raw, err := json.MarshalIndent(digest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

// hookLine is one recorded hook: its name, when it fired, and the input the CLI gave it.
type hookLine struct {
	Event string          `json:"event"`
	AtMs  int64           `json:"at_ms"`
	Input json.RawMessage `json:"input"`
}

func recordedHooks(t *testing.T, name string) []lanes.HookEvent {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []lanes.HookEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line hookLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		event := lanes.NewHookEvent(line.Event, line.Input)
		event.AtMs = line.AtMs
		out = append(out, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAnInteractiveGenerationsHooksFoldIntoItsDigest builds a digest from a recorded hook sequence: two turns,
// every tool by name, and the paths the file-writing tools were called with, relative to the worktree.
func TestAnInteractiveGenerationsHooksFoldIntoItsDigest(t *testing.T) {
	builder := lanes.NewDigest(lanes.DigestHooks, worktree, 900)
	for _, event := range recordedHooks(t, "digest-hooks.jsonl") {
		builder.Hook(event)
	}
	golden(t, "digest-hooks.golden", digestJSON(t, builder.Finish(4000)))
}

// TestAHookDigestHoldsNoToolInputButTheWrittenPaths is FR-SLOG-1's boundary: a secret in a shell command and
// one in an edit's body reach neither the event a hook forwards nor the digest built from it.
func TestAHookDigestHoldsNoToolInputButTheWrittenPaths(t *testing.T) {
	const secret = "sk-live-0123456789abcdef"
	builder := lanes.NewDigest(lanes.DigestHooks, worktree, 900)
	for _, event := range recordedHooks(t, "digest-hooks.jsonl") {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "Authorization") {
			t.Fatalf("a hook forwarded tool input: %s", raw)
		}
		builder.Hook(event)
	}
	digest, err := json.Marshal(builder.Finish(4000))
	if err != nil {
		t.Fatal(err)
	}
	state, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range [][]byte{digest, state} {
		if bytes.Contains(kept, []byte(secret)) || bytes.Contains(kept, []byte("Authorization")) || bytes.Contains(kept, []byte("curl")) {
			t.Errorf("the digest holds tool input: %s", kept)
		}
	}
}

func TestOnlyAFileWritingToolNamesAPath(t *testing.T) {
	for tool, want := range map[string]string{"Write": "a.go", "Edit": "a.go", "MultiEdit": "a.go", "NotebookEdit": "n.ipynb", "Read": "", "Bash": ""} {
		if got := lanes.WrittenPath(tool, "a.go", "n.ipynb"); got != want {
			t.Errorf("%s: %q, want %q", tool, got, want)
		}
	}
	input, err := json.Marshal(map[string]any{
		"tool_name": "Read", "tool_input": map[string]string{"file_path": "/work/lane/a.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event := lanes.NewHookEvent("PreToolUse", input); event.Path != "" {
		t.Errorf("a read names the path %q", event.Path)
	}
	if event := lanes.NewHookEvent("PostToolUse", mustJSON(t, map[string]any{
		"tool_name": "Write", "tool_input": map[string]string{"file_path": "/work/lane/a.go"},
	})); event.Path != "" {
		t.Errorf("a later hook names the path %q", event.Path)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestAFieldTheSourceDoesNotGiveIsAbsent holds FR-SLOG-1: an interactive digest has no API time, a headless one
// with no durations has no turn time, and none has a result text before the rule set that scans it exists.
func TestAFieldTheSourceDoesNotGiveIsAbsent(t *testing.T) {
	hooks := lanes.NewDigest(lanes.DigestHooks, worktree, 1)
	hooks.Hook(lanes.HookEvent{Event: "UserPromptSubmit", AtMs: 10})
	hooks.Hook(lanes.HookEvent{Event: "Stop", AtMs: 20})
	stream := lanes.NewDigest(lanes.DigestStream, worktree, 1)
	stream.Turn(5, lanes.TurnFacts{})
	for name, builder := range map[string]*lanes.DigestBuilder{"hooks": hooks, "stream": stream} {
		raw, err := json.Marshal(builder.Finish(30))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, absent := range []string{"api_ms", "result_text"} {
			if _, found := fields[absent]; found {
				t.Errorf("%s: %s is %s, and the source gives none", name, absent, fields[absent])
			}
		}
		if _, found := fields["turn_ms"]; found != (name == "hooks") {
			t.Errorf("%s: turn_ms present is %v", name, found)
		}
	}
}

// TestAStreamsTimesAreTheTurnsOwn sums the turns' durations, and takes the API time as the differences of the
// total the stream keeps, which starts over when a session does.
func TestAStreamsTimesAreTheTurnsOwn(t *testing.T) {
	ms := func(n int64) *int64 { return &n }
	builder := lanes.NewDigest(lanes.DigestStream, worktree, 1)
	builder.Turn(10, lanes.TurnFacts{DurationMs: ms(700), APIMs: ms(500)})
	builder.Turn(20, lanes.TurnFacts{DurationMs: ms(900), APIMs: ms(1200)})
	builder.Turn(30, lanes.TurnFacts{DurationMs: ms(100), APIMs: ms(80)})
	digest := builder.Finish(2)
	if digest.Turns != 3 || digest.TurnMs == nil || *digest.TurnMs != 1700 || digest.APIMs == nil || *digest.APIMs != 500+700+80 {
		t.Errorf("digest %+v", digest)
	}
}

// TestAStreamPositionFoldsOnce is the replay a kill between reading and saving causes: a position at or before
// the last one folded is read again and changes nothing.
func TestAStreamPositionFoldsOnce(t *testing.T) {
	builder := lanes.NewDigest(lanes.DigestStream, worktree, 1)
	call := []lanes.ToolUse{{Name: "Edit", Path: "/work/lane/a.go"}, {Name: "Bash"}}
	builder.Calls(7, call)
	builder.Turn(8, lanes.TurnFacts{})
	builder.Calls(7, call)
	builder.Turn(8, lanes.TurnFacts{})
	builder.Calls(9, call[1:])
	digest := builder.Finish(2)
	if digest.Turns != 1 || digest.ToolCalls["Edit"] != 1 || digest.ToolCalls["Bash"] != 2 || !slices.Equal(digest.FilesWritten, []string{"a.go"}) {
		t.Errorf("digest %+v", digest)
	}
}

func TestADigestKeepsTheFirstTwoHundredPathsAndCountsTheRest(t *testing.T) {
	builder := lanes.NewDigest(lanes.DigestStream, worktree, 1)
	for i := range lanes.MaxDigestFiles + 25 {
		builder.Calls(int64(i+1), []lanes.ToolUse{{Name: "Write", Path: fmt.Sprintf("%s/f%03d.txt", worktree, i)}})
	}
	// A path already kept is no new path, and a path past the cap is counted each time it is written.
	builder.Calls(1000, []lanes.ToolUse{{Name: "Write", Path: worktree + "/f000.txt"}, {Name: "Write", Path: worktree + "/f220.txt"}})
	digest := builder.Finish(2)
	if len(digest.FilesWritten) != lanes.MaxDigestFiles || digest.FilesWritten[0] != "f000.txt" || digest.FilesWrittenMore != 26 {
		t.Errorf("%d paths and %d more", len(digest.FilesWritten), digest.FilesWrittenMore)
	}
	if digest.ToolCalls["Write"] != lanes.MaxDigestFiles+27 {
		t.Errorf("calls %v", digest.ToolCalls)
	}
}

// TestAKnownMissCostsCompleteness holds `complete`: a line the worker could not read, a hook line that
// stopped a turn nobody saw start, and a digest begun after events went by are each one the worker knows it
// missed.
func TestAKnownMissCostsCompleteness(t *testing.T) {
	stream := lanes.NewDigest(lanes.DigestStream, worktree, 1)
	if !stream.Finish(2).Complete {
		t.Error("a digest of nothing missed is incomplete")
	}
	stream.Missed()
	if stream.Finish(2).Complete {
		t.Error("a missed line left the digest complete")
	}
	hooks := lanes.NewDigest(lanes.DigestHooks, worktree, 1)
	hooks.Hook(lanes.HookEvent{Event: "Stop", AtMs: 5})
	if digest := hooks.Finish(6); digest.Complete || digest.Turns != 1 || digest.TurnMs != nil {
		t.Errorf("a stop with no prompt: %+v", digest)
	}
	malformed := lanes.NewDigest(lanes.DigestHooks, worktree, 1)
	malformed.Hook(lanes.HookEvent{Event: "PreToolUse", Malformed: true})
	if malformed.Finish(6).Complete {
		t.Error("a hook line that did not parse left the digest complete")
	}
}

func TestADigestStateSurvivesARestart(t *testing.T) {
	builder := lanes.NewDigest(lanes.DigestStream, worktree, 1)
	builder.Calls(3, []lanes.ToolUse{{Name: "Edit", Path: worktree + "/a.go"}})
	builder.Turn(4, lanes.TurnFacts{})
	raw, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := lanes.RestoreDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	restored.Calls(3, []lanes.ToolUse{{Name: "Edit", Path: worktree + "/a.go"}})
	restored.Calls(5, []lanes.ToolUse{{Name: "Bash"}})
	got, want := restored.Finish(9), builder
	want.Calls(5, []lanes.ToolUse{{Name: "Bash"}})
	if digestJSON(t, got) != digestJSON(t, want.Finish(9)) {
		t.Errorf("restored %s, want %s", digestJSON(t, got), digestJSON(t, want.Finish(9)))
	}
	if _, err := lanes.RestoreDigest([]byte("not json")); err == nil {
		t.Error("a state that is not one restored")
	}
}
