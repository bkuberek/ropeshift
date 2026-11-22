package lanes

import (
	"slices"
	"strings"
)

// The shell tools besides Bash. Monitor runs a shell command in the background ([tools]).
//
// [tools]: https://code.claude.com/docs/en/tools-reference#monitor-tool
const (
	toolMonitor    = "Monitor"
	toolPowerShell = "PowerShell"
)

// The reasons a hook asks. Each reaches the person who answers.
const (
	ReasonAskPush = "ropeshift policy: a push asks in every permission mode."
	ReasonAskGate = "ropeshift policy: this lane's profile gates this command, which asks in every permission mode."
	ReasonAskRead = "ropeshift policy: this command could be a push or a gated command in a form the policy cannot read, so it asks in every permission mode."
)

// RunsShell says a tool runs the shell command its input names.
func RunsShell(tool string) bool {
	return tool == ToolBash || tool == toolMonitor || tool == toolPowerShell
}

// Asks says a person is asked before a shell call runs, and why: for a push, for a command a gate names, and for a
// command this cannot read that could be either. It holds in both run modes and every permission mode, because a
// hook's `ask` goes to the permission prompt and not to auto mode's classifier ([hooks]), while an ask rule matches
// only the command as written ([permissions]). It asks of nothing in a session held to one program, which runs no
// other.
//
// [hooks]: https://code.claude.com/docs/en/hooks#pretooluse-ask-decisions-in-auto-mode
// [permissions]: https://code.claude.com/docs/en/permissions#bash-rule-limits
func (p DenyPolicy) Asks(call ToolCall) (string, bool) {
	if !RunsShell(call.Tool) || p.Program != "" {
		return "", false
	}
	if call.Tool == toolPowerShell {
		return ReasonAskRead, true
	}
	if call.Command == "" {
		return "", false
	}
	read := readShellGates(call.Command, p.Gates)
	switch {
	case read.pushed || Pushes(call.Command):
		return ReasonAskPush, true
	case read.gated || p.Gated(call) || p.Gated(ToolCall{Tool: ToolBash, Command: strings.TrimSpace(call.Command)}):
		return ReasonAskGate, true
	case read.unread:
		return ReasonAskRead, true
	}
	return "", false
}

// MayPush says a shell call is a push or could be one, which is what the worker reads a lane's commits for before it
// lets the call ask anyone (FR-LANE-20).
func MayPush(tool, command string) bool {
	switch {
	case !RunsShell(tool):
		return false
	case tool == toolPowerShell:
		return true
	case Pushes(command):
		return true
	}
	read := readShellGates(command, nil)
	return read.pushed || read.unread
}

// Defers says a hook may end the turn on a gated call. The agent CLI honors a deferral only in a `-p` run, and only
// for the one call of a turn, and otherwise goes on as if the hook had said nothing ([hooks]). So a shell command
// defers only as written the way its gate's ask rule matches, which still asks then, and any other form asks.
//
// [hooks]: https://code.claude.com/docs/en/hooks#defer-a-tool-call-for-later
func (p DenyPolicy) Defers(call ToolCall) bool {
	if p.GatesAskOnly || !p.Gated(call) {
		return false
	}
	if !RunsShell(call.Tool) {
		return true
	}
	return slices.ContainsFunc(p.Gates, func(gate GateRule) bool {
		switch {
		case call.Tool != ToolBash || gate.Tool != ToolBash:
			return false
		case gate.CommandPrefix == "", call.Command == gate.CommandPrefix:
			return true
		}
		return !gate.Exact && strings.HasPrefix(call.Command, gate.CommandPrefix+" ")
	})
}

// decidePushes refuses a push the lane may never make, in every form one is read in, and says whether one runs by
// itself, which shares no call with a stage or a commit.
func (p DenyPolicy) decidePushes(command string) (reason string, runs, denied bool) {
	if p.Program != "" || command == "" {
		return "", false, false
	}
	for _, push := range readShellGates(command, nil).pushes {
		for _, rule := range p.Deny {
			if rule.Tool == DenyToolGit && rule.CommandPrefix == "push" {
				return rule.Reason, runs, true
			}
		}
		if reason, denied := p.decidePush(push.args); denied {
			return reason, runs, true
		}
		runs = runs || push.runs
	}
	return "", runs, false
}
