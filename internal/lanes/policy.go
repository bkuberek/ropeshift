package lanes

import (
	"path"
	"slices"
	"strings"
)

// The tools a policy reads.
const (
	ToolBash         = "Bash"
	toolWrite        = "Write"
	toolEdit         = "Edit"
	toolMultiEdit    = "MultiEdit"
	toolNotebookEdit = "NotebookEdit"
)

// The reasons a compiled policy gives. Each reaches the model, so each says what to do.
const (
	ReasonForcePush     = "ropeshift policy: a force push is denied on this lane. Push without force."
	ReasonDefaultBranch = "ropeshift policy: a push to the default branch is denied on this lane. Push the lane's own branch."
	ReasonOutside       = "ropeshift policy: a write outside this lane's worktree is denied. Write inside the worktree, with a path the policy can read."
)

// PolicySpec is what a generation's policy compiles from.
type PolicySpec struct {
	Worktree string
	// WorktreeReal is the worktree with its links resolved, because the CLI reports
	// real paths, and `/tmp` is a link on macOS. Empty means the same path.
	WorktreeReal string
	Home         string
	// DefaultBranches are the branches no push may reach.
	DefaultBranches []string
	// Writable are other directories a lane may write, such as its private temp.
	Writable []string
}

// CompilePolicy builds the file a generation's hook reads.
func CompilePolicy(spec PolicySpec) DenyPolicy {
	roots := append([]string{}, spec.Writable...)
	if spec.Worktree != "" {
		roots = append(roots, spec.Worktree)
	}
	if spec.WorktreeReal != "" && spec.WorktreeReal != spec.Worktree {
		roots = append(roots, spec.WorktreeReal)
	}
	return DenyPolicy{Roots: roots, Home: spec.Home, DefaultBranches: spec.DefaultBranches, Deny: []DenyRule{}}
}

// ToolCall is the part of a tool call a policy reads.
type ToolCall struct {
	Tool    string
	Command string
	Path    string
	// Cwd is where the session stands, which a relative path is read against.
	Cwd string
}

// Decide returns the reason of the first thing that denies a call. It reads what a call
// says and touches nothing, because a hook does no work: a link inside a worktree is the
// generated `Edit` rules' to catch, and a hook is a guardrail, not a sandbox.
func (p DenyPolicy) Decide(call ToolCall) (string, bool) {
	for _, rule := range p.Deny {
		if rule.Tool == call.Tool && strings.HasPrefix(call.Command, rule.CommandPrefix) {
			return rule.Reason, true
		}
	}
	switch call.Tool {
	case ToolBash:
		return p.decideShell(call.Command, call.Cwd, 0)
	case toolWrite, toolEdit, toolMultiEdit, toolNotebookEdit:
		if len(p.Roots) != 0 && !p.inside(shellWord{text: call.Path}, call.Cwd) {
			return ReasonOutside, true
		}
	}
	return "", false
}

// commandWrappers run the command that follows them.
var commandWrappers = []string{"command", "builtin", "exec", "env", "nice", "nohup", "time", "sudo", "doas", "xargs", "timeout"}

// shellKeywords lead a command inside a compound one.
var shellKeywords = []string{"if", "then", "elif", "else", "do", "while", "until", "!", "{", "}", "fi", "done"}

// shells take a command as an argument.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh"}

func (p DenyPolicy) decideShell(text, cwd string, depth int) (string, bool) {
	if depth > maxShellDepth {
		return ReasonOutside, true
	}
	commands, nested := parseShell(text)
	for _, inner := range nested {
		if reason, denied := p.decideShell(inner, cwd, depth+1); denied {
			return reason, true
		}
	}
	at := cwd
	for _, command := range commands {
		if len(p.Roots) != 0 {
			for _, target := range command.writes {
				if !p.inside(target, at) {
					return ReasonOutside, true
				}
			}
		}
		words := stripLeaders(command.words)
		if len(words) == 0 {
			continue
		}
		name := path.Base(words[0].text)
		switch {
		case name == "cd":
			at = p.moved(words[1:], at)
		case name == "git":
			if reason, denied := p.decideGit(words[1:]); denied {
				return reason, true
			}
		case name == "eval":
			if reason, denied := p.decideShell(joinWords(words[1:]), at, depth+1); denied {
				return reason, true
			}
		case slices.Contains(shells, name):
			if inner, ok := shellArgument(words[1:]); ok {
				if reason, denied := p.decideShell(inner, at, depth+1); denied {
					return reason, true
				}
			}
		default:
			if len(p.Roots) != 0 {
				for _, target := range writeTargets(name, words[1:]) {
					if !p.inside(target, at) {
						return ReasonOutside, true
					}
				}
			}
		}
	}
	return "", false
}

func joinWords(words []shellWord) string {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		parts = append(parts, word.text)
	}
	return strings.Join(parts, " ")
}

// stripLeaders takes away what stands before the command itself: keywords, assignments,
// and programs that run the rest of the line.
func stripLeaders(words []shellWord) []shellWord {
	for len(words) != 0 {
		first := words[0]
		switch {
		case slices.Contains(shellKeywords, first.text), isAssignment(first.text):
			words = words[1:]
		case slices.Contains(commandWrappers, path.Base(first.text)):
			words = words[1:]
			for len(words) != 0 && (strings.HasPrefix(words[0].text, "-") || isAssignment(words[0].text)) {
				words = words[1:]
			}
		default:
			return words
		}
	}
	return nil
}

func isAssignment(text string) bool {
	name, _, found := strings.Cut(text, "=")
	if !found || name == "" {
		return false
	}
	for _, c := range []byte(name) {
		if !isNameCharacter(c) {
			return false
		}
	}
	return name[0] < '0' || name[0] > '9'
}

// shellArgument finds the text of `sh -c`, in any bundle of flags that holds `c`.
func shellArgument(words []shellWord) (string, bool) {
	for i, word := range words {
		if !strings.HasPrefix(word.text, "-") || strings.HasPrefix(word.text, "--") {
			return "", false
		}
		if strings.Contains(word.text, "c") && i+1 < len(words) {
			return words[i+1].text, true
		}
	}
	return "", false
}

// moved follows a `cd`. A directory the policy cannot read leaves it not knowing where it
// stands, and a relative path from there reads as outside.
func (p DenyPolicy) moved(args []shellWord, at string) string {
	if len(args) == 0 {
		return p.Home
	}
	target := args[len(args)-1]
	resolved, ok := p.resolve(target, at)
	if !ok {
		return ""
	}
	return resolved
}

// gitOptionsWithValue are the long options before a subcommand that take the next word.
var gitOptionsWithValue = []string{"--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env"}

// takesGitValue reads an option before a subcommand, and `-C` and `-c` take one too.
func takesGitValue(text string) bool {
	return strings.EqualFold(text, "-c") || slices.Contains(gitOptionsWithValue, text)
}

func (p DenyPolicy) decideGit(args []shellWord) (string, bool) {
	for len(args) != 0 && strings.HasPrefix(args[0].text, "-") {
		if takesGitValue(args[0].text) {
			args = args[1:]
		}
		if len(args) != 0 {
			args = args[1:]
		}
	}
	if len(args) == 0 || args[0].text != "push" {
		return "", false
	}
	return p.decidePush(args[1:])
}

// pushOptionsWithValue are the options of `git push` that take the next word.
var pushOptionsWithValue = []string{"-o", "--push-option", "--repo", "--receive-pack", "--exec", "--signed"}

func isForceOption(text string) bool {
	if text == "--force" || text == "--force-if-includes" || strings.HasPrefix(text, "--force-with-lease") {
		return true
	}
	return strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && strings.Contains(text, "f")
}

// decidePush reads one push. A push with no refspec, or of HEAD, is the current branch,
// which a hook cannot read and a lane's worktree does not hold on the default branch.
func (p DenyPolicy) decidePush(args []shellWord) (string, bool) {
	var named []string
	everything := false
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case isForceOption(text):
			return ReasonForcePush, true
		case text == "--all" || text == "--mirror" || text == "--branches":
			everything = true
		case slices.Contains(pushOptionsWithValue, text):
			i++
		case strings.HasPrefix(text, "-"):
		default:
			named = append(named, text)
		}
	}
	if everything {
		return ReasonDefaultBranch, true
	}
	if len(named) < 2 {
		return "", false
	}
	for _, refspec := range named[1:] {
		if strings.HasPrefix(refspec, "+") {
			return ReasonForcePush, true
		}
		destination := refspec
		if _, after, found := strings.Cut(refspec, ":"); found {
			destination = after
		}
		destination = strings.TrimPrefix(destination, "refs/heads/")
		if slices.Contains(p.DefaultBranches, destination) {
			return ReasonDefaultBranch, true
		}
	}
	return "", false
}

// writeRule says which operands of a program it writes.
type writeRule int

const (
	writesEvery writeRule = iota
	writesLast
	writesAfterFirst
)

// writers are the programs whose operands name what they change. A program not listed
// writes where its own code says, and a hook does not guess.
var writers = map[string]writeRule{
	"tee": writesEvery, "touch": writesEvery, "mkdir": writesEvery, "rmdir": writesEvery, "rm": writesEvery,
	"truncate": writesEvery, "mv": writesEvery, "cp": writesLast, "ln": writesLast, "install": writesLast,
	"chmod": writesAfterFirst, "chown": writesAfterFirst,
}

func operands(args []shellWord) []shellWord {
	out := make([]shellWord, 0, len(args))
	for _, word := range args {
		if !strings.HasPrefix(word.text, "-") {
			out = append(out, word)
		}
	}
	return out
}

func writeTargets(name string, args []shellWord) []shellWord {
	switch name {
	case "dd":
		var out []shellWord
		for _, word := range args {
			if target, ok := strings.CutPrefix(word.text, "of="); ok {
				out = append(out, shellWord{text: target, dynamic: word.dynamic})
			}
		}
		return out
	case "sed":
		return sedTargets(args)
	}
	rule, listed := writers[name]
	if !listed {
		return nil
	}
	listedOperands := operands(args)
	switch {
	case len(listedOperands) == 0:
		return nil
	case rule == writesLast:
		return listedOperands[len(listedOperands)-1:]
	case rule == writesAfterFirst:
		return listedOperands[1:]
	}
	return listedOperands
}

// sedTargets reads `sed -i`, whose script is its first operand when no `-e` names one. A
// suffix may stand as its own empty word, as macOS takes it.
func sedTargets(args []shellWord) []shellWord {
	inPlace := false
	var rest []shellWord
	for _, word := range args {
		switch {
		case strings.HasPrefix(word.text, "-i") || word.text == "--in-place":
			inPlace = true
		case word.text != "" && !strings.HasPrefix(word.text, "-"):
			rest = append(rest, word)
		}
	}
	if !inPlace || len(rest) < 2 {
		return nil
	}
	return rest[1:]
}

// quietDevices take a write and keep nothing.
var quietDevices = []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty"}

// resolve turns a word into a path with no `..`, as far as text can. Only the variables a
// policy knows read as paths, and anything else is not known.
func (p DenyPolicy) resolve(word shellWord, at string) (string, bool) {
	text := word.text
	for _, name := range []string{"$PWD", "${PWD}"} {
		if rest, ok := strings.CutPrefix(text, name); ok && at != "" {
			return path.Clean(at + rest), !strings.ContainsAny(rest, "$")
		}
	}
	for _, name := range []string{"$HOME", "${HOME}", "~"} {
		if rest, ok := strings.CutPrefix(text, name); ok && p.Home != "" && (rest == "" || rest[0] == '/') {
			return path.Clean(p.Home + rest), !strings.ContainsAny(rest, "$")
		}
	}
	if text == "" || word.dynamic {
		return "", false
	}
	if path.IsAbs(text) {
		return path.Clean(text), true
	}
	if at == "" {
		return "", false
	}
	return path.Join(at, text), true
}

// inside reports a path under a root this generation may write. One it cannot read reads
// as outside, because this hook fails closed.
func (p DenyPolicy) inside(word shellWord, at string) bool {
	resolved, ok := p.resolve(word, at)
	if !ok {
		return false
	}
	if slices.Contains(quietDevices, resolved) {
		return true
	}
	for _, root := range p.Roots {
		if resolved == root || strings.HasPrefix(resolved, root+"/") {
			return true
		}
	}
	return false
}

// permissionRanks orders the modes by how much runs without asking. Anything else outranks
// them all, because a mode nobody here knows must not pass as a lower one.
var permissionRanks = map[string]int{"plan": 0, "default": 1, "acceptEdits": 2, "dontAsk": 3, "auto": 4, "bypassPermissions": 5}

// RaisesPermission reports a mode that runs more without asking than another.
func RaisesPermission(from, to string) bool {
	rank, known := permissionRanks[to]
	if !known {
		return true
	}
	return rank > permissionRanks[from]
}
