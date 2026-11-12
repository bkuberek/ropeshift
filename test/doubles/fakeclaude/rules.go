package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ruling is what a call's permission rules make of it, read as the CLI documents them
// (https://code.claude.com/docs/en/permissions): a deny rule refuses it, an ask rule prompts, an
// allow rule that covers it runs it with no prompt, and anything else prompts, as the default mode
// does. This reads the rule shapes ropeshift generates, and no more.
type ruling int

const (
	rulePrompts ruling = iota
	ruleAllows
	ruleDenies
)

// fileTools are the tools `Edit` rules cover.
var fileTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}

// rule decides a built-in call from the session's rules.
func (s settings) rule(st step) ruling {
	if slices.Contains(fileTools, st.Tool) {
		return s.ruleFile(st.Tool, st.File)
	}
	if st.Tool == agentTool {
		return s.ruleAgent(st.Agent)
	}
	return s.ruleCommand(st.Command)
}

// agentTool starts a subagent. A rule names one agent as `Agent(<name>)`, and `Task` is the tool's old
// name, which rules still take (https://code.claude.com/docs/en/sub-agents).
const agentTool = "Agent"

// ruleAgent reads a subagent's start: a deny rule refuses it, an ask rule prompts, and with neither it
// starts, as the CLI asks no permission to start one.
func (s settings) ruleAgent(name string) ruling {
	names := func(rules []string) bool {
		return slices.Contains(rules, agentTool+"("+name+")") || slices.Contains(rules, "Task("+name+")") || slices.Contains(rules, agentTool)
	}
	switch {
	case names(s.Permissions.Deny):
		return ruleDenies
	case names(s.Permissions.Ask):
		return rulePrompts
	}
	return ruleAllows
}

// ruleCommand reads a shell command part by part, where the CLI splits one: every part must be
// allowed for the command to run with no prompt, and one denied or asked part decides it all.
func (s settings) ruleCommand(command string) ruling {
	parts := commandParts(command)
	anyPart := func(rules []string) bool {
		return slices.ContainsFunc(parts, func(part string) bool {
			return slices.ContainsFunc(rules, func(rule string) bool { return bashMatches(rule, part) })
		})
	}
	switch {
	case anyPart(s.Permissions.Deny):
		return ruleDenies
	case anyPart(s.Permissions.Ask):
		return rulePrompts
	}
	for _, part := range parts {
		if !slices.ContainsFunc(s.Permissions.Allow, func(rule string) bool { return bashMatches(rule, part) }) {
			return rulePrompts
		}
	}
	return ruleAllows
}

// commandParts splits a command at `&&`, `||`, `;`, `|`, and a newline.
func commandParts(command string) []string {
	split := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n").Replace(command)
	var parts []string
	for part := range strings.SplitSeq(split, "\n") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// bashMatches reads one rule against one simple command: `Bash` takes every command, a trailing ` *`
// takes the prefix alone or followed by a space, a trailing `*` takes any text after the prefix, and
// any other rule takes the command exactly.
func bashMatches(rule, part string) bool {
	if rule == "Bash" {
		return true
	}
	body, ok := strings.CutPrefix(rule, "Bash(")
	if !ok {
		return false
	}
	body = strings.TrimSuffix(body, ")")
	if prefix, found := strings.CutSuffix(body, " *"); found {
		return part == prefix || strings.HasPrefix(part, prefix+" ")
	}
	if prefix, found := strings.CutSuffix(body, "*"); found {
		return strings.HasPrefix(part, prefix)
	}
	return part == body
}

// ruleFile reads a file tool's call. A deny or an ask rule takes the path asked for or the file it
// resolves to, and an allow rule must take both, so a link that leads out of an allowed directory is
// not allowed. A path whose links cannot be followed is refused, as the CLI refuses one.
func (s settings) ruleFile(tool, file string) ruling {
	asked := file
	if !filepath.IsAbs(asked) {
		asked = filepath.Join(mustGetwd(), asked)
	}
	asked = filepath.Clean(asked)
	resolved, ok := resolvePath(asked)
	if !ok {
		return ruleDenies
	}
	takes := func(rule, path string) bool { return rule == tool || rule == "Edit" || editMatches(rule, path) }
	either := func(rules []string) bool {
		return slices.ContainsFunc(rules, func(rule string) bool { return takes(rule, asked) || takes(rule, resolved) })
	}
	switch {
	case either(s.Permissions.Deny):
		return ruleDenies
	case either(s.Permissions.Ask):
		return rulePrompts
	}
	for _, path := range []string{asked, resolved} {
		if !slices.ContainsFunc(s.Permissions.Allow, func(rule string) bool { return takes(rule, path) }) {
			return rulePrompts
		}
	}
	return ruleAllows
}

// editMatches reads an `Edit` rule on an absolute path: `//` starts the path, and `/**` takes
// everything under it.
func editMatches(rule, path string) bool {
	pattern, ok := strings.CutPrefix(rule, "Edit(//")
	if !ok {
		return false
	}
	pattern = "/" + strings.TrimSuffix(pattern, ")")
	if dir, all := strings.CutSuffix(pattern, "/**"); all {
		return strings.HasPrefix(path, dir+"/")
	}
	return path == pattern
}

// resolvePath follows a path's links as far as the disk holds it, and keeps the parts past that as
// they read. A part that exists and does not resolve, such as a link to nothing, cannot be followed.
func resolvePath(path string) (string, bool) {
	dir, rest := path, ""
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(real, rest), true
		}
		if _, err := os.Lstat(dir); err == nil {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path, true
		}
		dir, rest = parent, filepath.Join(filepath.Base(dir), rest)
	}
}
