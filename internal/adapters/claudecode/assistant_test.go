package claudecode

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

func assistantSpec(t *testing.T) ports.SessionSpec {
	t.Helper()
	dir := t.TempDir()
	return ports.SessionSpec{
		Lane: "01JRUN", Generation: 1, Dir: dir, ConfigDir: t.TempDir(), Binary: "/opt/ropeshift/bin/ropeshift", Assistant: true, ReadOnly: true,
		Tools: []string{lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch}, RunMode: ports.RunModeHeadless,
	}
}

// An assistant reads, searches, uses the web tools and its context tools, and runs `ropeshift` and nothing
// else in a shell; no rule lets it edit, write, stage, commit, or push.
func TestAnAssistantRunsOnlyRopeshiftInAShellAndWritesNothing(t *testing.T) {
	settings := generateSettings(assistantSpec(t))
	allow := settings.Permissions.Allow
	for _, want := range []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch", "Bash(ropeshift *)", toolRule(lanes.ToolContextGet), toolRule(lanes.ToolQuestionSearch)} {
		if !slices.Contains(allow, want) {
			t.Errorf("allow lacks %s: %v", want, allow)
		}
	}
	for _, rule := range allow {
		if strings.HasPrefix(rule, "Edit(") || strings.HasPrefix(rule, "Write") || (strings.HasPrefix(rule, "Bash(") && rule != "Bash(ropeshift *)") {
			t.Errorf("an assistant's settings allow %s", rule)
		}
		if strings.HasPrefix(rule, toolRule("")) && !slices.Contains([]string{
			toolRule(lanes.ToolContextGet), toolRule(lanes.ToolDecisionSearch), toolRule(lanes.ToolRequirementGet), toolRule(lanes.ToolQuestionSearch),
		}, rule) {
			t.Errorf("an assistant's settings allow its tool %s", rule)
		}
	}
	for _, tool := range lanes.ReadOnlyTools() {
		if !slices.Contains(settings.Permissions.Deny, tool) {
			t.Errorf("deny lacks %s: %v", tool, settings.Permissions.Deny)
		}
	}
	if !slices.Contains(settings.Permissions.Deny, "Bash(git commit*)") {
		t.Errorf("deny lacks a commit: %v", settings.Permissions.Deny)
	}
	if len(settings.Permissions.Ask) != 0 {
		t.Errorf("an assistant asks %v", settings.Permissions.Ask)
	}
	if denied := compilePolicy(assistantSpec(t)).Deny; !slices.ContainsFunc(denied, func(rule lanes.DenyRule) bool { return strings.Contains(rule.Tool, "Edit") }) {
		t.Error("the hook's policy lets an assistant edit")
	}
	if settings.Hooks["PreToolUse"] == nil || settings.Hooks["PermissionRequest"] == nil {
		t.Error("an assistant's session has no hook")
	}
}

// A reply is the text an assistant's message holds, under the vendor's ID; a message of tool calls or
// thinking alone is none, and no other session's text is read.
func TestAnAssistantsTextIsReadAsReplies(t *testing.T) {
	text := []byte(`{"type":"assistant","uuid":"u-1","message":{"id":"msg_01","content":[{"type":"text","text":"Two merges wait."},{"type":"tool_use","id":"t1","name":"Bash","input":{}},{"type":"text","text":"Both are green."}]}}`)
	p, err := parseLineOf(text, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.useful || p.event.Kind != ports.EventReply || p.event.MessageID != "msg_01" || p.event.Text != "Two merges wait.\n\nBoth are green." {
		t.Fatalf("parsed %+v", p.event)
	}
	for _, line := range []string{
		`{"type":"assistant","message":{"id":"msg_02","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}`,
		`{"type":"assistant","message":{"id":"msg_03","content":[{"type":"thinking","thinking":"hmm"}]}}`,
		`{"type":"assistant","parent_tool_use_id":"t9","message":{"id":"msg_04","content":[{"type":"text","text":"a subagent"}]}}`,
	} {
		p, err := parseLineOf([]byte(line), false, true)
		if err != nil {
			t.Fatal(err)
		}
		if p.event.Kind == ports.EventReply {
			t.Errorf("%s read as a reply", line)
		}
	}
	p, err = parseLine(text, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.event.Kind == ports.EventReply || p.event.Text != "" {
		t.Errorf("a lane's text was read: %+v", p.event)
	}
}
