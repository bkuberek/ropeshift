package claudecode

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

var native = roster.Support{Subagents: roster.SubagentsNative, SubagentTools: roster.ToolsEnforced}

// sessionAgents fits the built-in roster to a session, with the built-in class mapping.
func sessionAgents(session roster.Session) []ports.SessionAgent {
	classes := map[string]string{roster.ClassSmall: "haiku", roster.ClassStandard: "sonnet", roster.ClassLarge: "opus", roster.ClassFrontier: "opus"}
	var out []ports.SessionAgent
	for _, fitted := range roster.Resolve(roster.Layer{}, roster.Layer{}).ForSession(session, native) {
		out = append(out, ports.SessionAgent{Fitted: fitted, Model: fitted.ModelFor(DriverName, classes)})
	}
	return out
}

func renderedOf(t *testing.T, arguments []string) map[string]renderedAgent {
	t.Helper()
	at := slices.Index(arguments, "--agents")
	if at < 0 || at+1 >= len(arguments) {
		t.Fatalf("no --agents in %q", arguments)
	}
	var out map[string]renderedAgent
	if err := json.Unmarshal([]byte(arguments[at+1]), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTheRosterRendersThroughTheAgentsFlag is FR-AGENT-8 on Claude Code: every start and resume, in both
// run modes, carries the session's agents in --agents with the documented fields and no others, and the
// session's environment turns the built-in Explore and Plan agents and nested agents off.
func TestTheRosterRendersThroughTheAgentsFlag(t *testing.T) {
	spec := policySpec(t)
	spec.Tools, spec.Agents, spec.Effort = lanes.Tools, sessionAgents(roster.SessionWriting), roster.EffortHigh
	for name, arguments := range map[string][]string{
		"start": StartArgs(spec), "resume": ResumeArgs(spec, ports.ResumeRef{SessionID: "s"}), "pane": paneArgs(spec),
	} {
		rendered := renderedOf(t, arguments)
		if len(rendered) != len(spec.Agents) {
			t.Fatalf("%s rendered %d agents of %d", name, len(rendered), len(spec.Agents))
		}
		if at := slices.Index(arguments, "--effort"); at < 0 || arguments[at+1] != roster.EffortHigh {
			t.Errorf("%s states no effort: %q", name, arguments)
		}
	}
	raw := StartArgs(spec)[slices.Index(StartArgs(spec), "--agents")+1]
	for _, forbidden := range []string{"permissionMode", "hooks", "mcpServers"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the rendering sets %s: %s", forbidden, raw)
		}
	}
	implementation := renderedOf(t, StartArgs(spec))["implementation"]
	if implementation.Model != "sonnet" || implementation.Effort != roster.EffortMedium || !strings.Contains(implementation.Prompt, "Report to your coordinator") ||
		!strings.Contains(implementation.Description, "ordinary change") || !slices.Contains(implementation.Tools, "Edit") ||
		!slices.Contains(implementation.Tools, "mcp__ropeshift__context_get") || !slices.Contains(implementation.DisallowedTools, "Agent") {
		t.Errorf("implementation rendered as %+v", implementation)
	}
	env := sessionEnv(spec, Use{})
	for _, want := range []string{"CLAUDE_CODE_DISABLE_EXPLORE_PLAN_AGENTS=1", "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=1"} {
		if !slices.Contains(env, want) || !slices.Contains(paneEnv(spec, Use{}), want) {
			t.Errorf("the environment holds no %s", want)
		}
	}
	none := policySpec(t)
	if slices.Contains(StartArgs(none), "--agents") || slices.Contains(sessionEnv(none, Use{}), "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=1") {
		t.Errorf("a session with no agents was given the flag: %q", StartArgs(none))
	}
}

// TestNoRenderedAgentHoldsAToolItsSessionsRulesRefuse is FR-AGENT-10 and FR-LANE-19: whatever session an
// agent is rendered into, no tool it holds is one a deny rule of that session's generated settings
// names, no read-only agent holds an edit tool or a shell in a session that writes, and none may start
// another agent.
func TestNoRenderedAgentHoldsAToolItsSessionsRulesRefuse(t *testing.T) {
	for _, tt := range []struct {
		name     string
		session  roster.Session
		readOnly bool
	}{
		{"a writing lane", roster.SessionWriting, false},
		{"a review lane", roster.SessionReview, true},
		{"the assistant", roster.SessionAssistant, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := policySpec(t)
			spec.Tools, spec.ReadOnly, spec.Agents = lanes.Tools, tt.readOnly, sessionAgents(tt.session)
			denied := generateSettings(spec).Permissions.Deny
			allowedTools := slices.Concat(vendorTools(), contextTools(spec))
			for name, agent := range renderedOf(t, StartArgs(spec)) {
				if len(agent.Tools) == 0 {
					t.Errorf("%s holds no tool, which the CLI reads as every tool", name)
				}
				for _, tool := range agent.Tools {
					if slices.Contains(denied, tool) {
						t.Errorf("%s holds %s, which the session denies", name, tool)
					}
					if !slices.Contains(allowedTools, tool) {
						t.Errorf("%s holds %s, which the session does not hold", name, tool)
					}
					if tool == "Agent" {
						t.Errorf("%s may start another agent", name)
					}
				}
				readOnlyAgent := slices.ContainsFunc(spec.Agents, func(a ports.SessionAgent) bool { return a.Name == name && a.Access == roster.AccessReadOnly })
				if readOnlyAgent && (slices.Contains(agent.Tools, "Edit") || !slices.Contains(agent.DisallowedTools, "Edit")) {
					t.Errorf("read-only %s may edit: %+v", name, agent)
				}
				if readOnlyAgent && !tt.readOnly && slices.Contains(agent.Tools, "Bash") {
					t.Errorf("read-only %s holds a shell in a session that writes", name)
				}
				if tt.session == roster.SessionAssistant && slices.Contains(agent.Tools, "Bash") {
					t.Errorf("the assistant's %s holds a shell", name)
				}
			}
			if report, _ := (&Driver{}).Preflight(t.Context(), spec); slices.ContainsFunc(report.Problems, func(p string) bool { return strings.Contains(p, "agent") }) {
				t.Errorf("preflight refused the rendering: %v", report.Problems)
			}
		})
	}
}

// TestAPerUseAgentAsksOnEveryStart is FR-AGENT-11: each per-use agent gets an ask rule in the form whose
// deny rule the CLI documents, in every permission mode, and an agent with no approval gets none.
func TestAPerUseAgentAsksOnEveryStart(t *testing.T) {
	spec := policySpec(t)
	spec.Tools, spec.Agents = lanes.Tools, sessionAgents(roster.SessionWriting)
	ask := generateSettings(spec).Permissions.Ask
	for _, want := range []string{"Agent(reasoning)", "Agent(deep-implementation)"} {
		if !slices.Contains(ask, want) {
			t.Errorf("no ask rule %s in %v", want, ask)
		}
	}
	if slices.Contains(ask, "Agent(implementation)") {
		t.Errorf("an agent with no approval asks: %v", ask)
	}
}

// TestALanesAgentNarrowsItsRules: the categories the lane's own agent denies become deny rules of the
// lane, and delegate stays, because the lane's session is the one that routes.
func TestALanesAgentNarrowsItsRules(t *testing.T) {
	spec := policySpec(t)
	spec.Tools, spec.Deny = lanes.Tools, []string{roster.ToolWeb, roster.ToolDelegate}
	denied := generateSettings(spec).Permissions.Deny
	for _, want := range []string{"WebFetch", "WebSearch"} {
		if !slices.Contains(denied, want) {
			t.Errorf("no deny rule %s in %v", want, denied)
		}
	}
	if slices.Contains(denied, "Agent") {
		t.Errorf("the lane may not route: %v", denied)
	}
}

// TestPreflightRefusesARenderingTheCLIWouldRefuse: an invalid --agents value ends a session at startup,
// so the preflight checks it first.
func TestPreflightRefusesARenderingTheCLIWouldRefuse(t *testing.T) {
	spec := policySpec(t)
	spec.Tools = lanes.Tools
	agent := sessionAgents(roster.SessionWriting)[0]
	agent.Name = "-bad:name"
	spec.Agents = []ports.SessionAgent{agent}
	report, err := (&Driver{}).Preflight(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(report.Problems, func(p string) bool { return strings.Contains(p, "agent") }) {
		t.Errorf("preflight passed %q: %v", agent.Name, report.Problems)
	}
}

// TestSubagentUsageNamesTheRosterAgent is FR-AGENT-12: an Agent call names the agent it starts, and the
// usage of a message whose parent is that call counts toward that agent. A message whose parent no call
// in this stream named stays the session's own.
func TestSubagentUsageNamesTheRosterAgent(t *testing.T) {
	s := &session{events: make(chan ports.DriverEvent, 8)}
	for _, line := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"x"},{"type":"tool_use","id":"toolu_1","name":"Agent","input":{"subagent_type":"explore","prompt":"p"}}],"usage":{"input_tokens":9}}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"content":[],"usage":{"input_tokens":120,"output_tokens":30,"cache_read_input_tokens":7}}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_gone","message":{"content":[],"usage":{"input_tokens":5}}}`,
	} {
		s.take([]byte(line), 1)
	}
	close(s.events)
	var agents []ports.DriverEvent
	for event := range s.events {
		if event.Kind == ports.EventAgentUsage {
			agents = append(agents, event)
		}
	}
	if len(agents) != 1 || agents[0].Agent != "explore" || agents[0].Tokens == nil || agents[0].Tokens.InputTokens != 120 ||
		agents[0].Tokens.OutputTokens != 30 || agents[0].Tokens.CacheReadTokens != 7 {
		t.Fatalf("agent usage %+v", agents)
	}
}
