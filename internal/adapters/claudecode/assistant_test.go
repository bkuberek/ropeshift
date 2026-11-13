package claudecode

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
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

// An assistant reads and searches only its own directory and the read-only trees, and runs `ropeshift`
// and nothing else in a shell: no rule lets it read anywhere else, reach the web, edit, write, stage,
// commit, or push, and the hook's policy holds reads to those roots whatever a rule says.
func TestAnAssistantReadsOnlyItsTreesAndRunsOnlyRopeshift(t *testing.T) {
	spec := assistantSpec(t)
	tree := t.TempDir()
	spec.ReadOnlyDirs = []string{tree}
	settings := generateSettings(spec)
	allow := settings.Permissions.Allow
	for _, want := range []string{"Read(/" + spec.Dir + "/**)", "Read(/" + tree + "/**)", "Bash(ropeshift *)", toolRule(lanes.ToolContextGet), toolRule(lanes.ToolQuestionSearch)} {
		if !slices.Contains(allow, want) {
			t.Errorf("allow lacks %s: %v", want, allow)
		}
	}
	for _, rule := range allow {
		if slices.Contains([]string{"Read", "Grep", "Glob", "WebFetch", "WebSearch"}, rule) || strings.HasPrefix(rule, "Web") {
			t.Errorf("an assistant's settings allow %s everywhere", rule)
		}
		if strings.HasPrefix(rule, "Edit(") || strings.HasPrefix(rule, "Write") || (strings.HasPrefix(rule, "Bash(") && rule != "Bash(ropeshift *)") {
			t.Errorf("an assistant's settings allow %s", rule)
		}
		if strings.HasPrefix(rule, toolRule("")) && !slices.Contains([]string{
			toolRule(lanes.ToolContextGet), toolRule(lanes.ToolDecisionSearch), toolRule(lanes.ToolRequirementGet), toolRule(lanes.ToolQuestionSearch),
		}, rule) {
			t.Errorf("an assistant's settings allow its tool %s", rule)
		}
	}
	for _, tool := range append(lanes.ReadOnlyTools(), "WebFetch", "WebSearch") {
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
	policy := compilePolicy(spec)
	for _, call := range []lanes.ToolCall{
		{Tool: "Edit", Path: spec.Dir + "/x", Cwd: spec.Dir},
		{Tool: "Read", Path: "/proc/1/environ", Cwd: spec.Dir},
		{Tool: "Grep", Path: filepath.Dir(spec.ConfigDir), Cwd: spec.Dir},
		{Tool: "WebFetch", Cwd: spec.Dir},
		{Tool: "WebSearch", Cwd: spec.Dir},
		{Tool: "Bash", Command: "cat /proc/1/environ", Cwd: spec.Dir},
		{Tool: "Bash", Command: "ropeshift lane list && ls ..", Cwd: spec.Dir},
	} {
		if _, denied := policy.Decide(call); !denied {
			t.Errorf("the hook's policy lets an assistant %+v", call)
		}
	}
	for _, call := range []lanes.ToolCall{
		{Tool: "Read", Path: tree + "/main.go", Cwd: spec.Dir}, {Tool: "Grep", Cwd: spec.Dir}, {Tool: "Bash", Command: "ropeshift lane list", Cwd: spec.Dir},
	} {
		if reason, denied := policy.Decide(call); denied {
			t.Errorf("the hook's policy denies %+v: %s", call, reason)
		}
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

// An assistant's rules deny every ropeshift command that acts on the machine rather than through the API,
// though `ropeshift *` is allowed, because a deny rule beats an allow rule.
func TestAnAssistantIsDeniedMachineLocalCommands(t *testing.T) {
	deny := generateSettings(assistantSpec(t)).Permissions.Deny
	for _, command := range authority.MachineLocalCommands {
		if want := "Bash(ropeshift " + command + "*)"; !slices.Contains(deny, want) {
			t.Errorf("deny lacks %s", want)
		}
	}
	if len(authority.MachineLocalCommands) < 6 {
		t.Fatalf("machine-local commands %v", authority.MachineLocalCommands)
	}
}

// An assistant's shell runs only the ropeshift commands its spec names: a route the worker or an agent CLI
// starts, such as `lane-shim`, which would run any program, is denied by a rule of its own and by the
// hook, and a command the spec names still runs.
func TestAnAssistantRunsOnlyRopeshiftCommandsAndNoRoute(t *testing.T) {
	spec := assistantSpec(t)
	spec.Commands, spec.Routes = []string{"lane list", "chat show"}, []string{"hook", "mcp", "statusline", "lane-pane", "lane-shim", "help", "version"}
	deny := generateSettings(spec).Permissions.Deny
	for _, route := range spec.Routes {
		if want := "Bash(ropeshift " + route + "*)"; !slices.Contains(deny, want) {
			t.Errorf("deny lacks %s", want)
		}
	}
	policy := compilePolicy(spec)
	for _, command := range []string{"ropeshift lane-shim --dir d --socket s -- /bin/sh -c id", "ropeshift hook PreToolUse", "ropeshift backup /tmp/x"} {
		if _, denied := policy.Decide(lanes.ToolCall{Tool: "Bash", Command: command, Cwd: spec.Dir}); !denied {
			t.Errorf("the hook's policy lets an assistant run %q", command)
		}
	}
	if reason, denied := policy.Decide(lanes.ToolCall{Tool: "Bash", Command: "ropeshift chat show c1", Cwd: spec.Dir}); denied {
		t.Errorf("a command the spec names was denied: %s", reason)
	}
}
