package lanes

import (
	"errors"
	"fmt"
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
	ReasonPushApart     = "ropeshift policy: a push runs as a command of its own on this lane, because a commit runs with no prompt and a push does not. Commit first, then push alone."
	// ReasonReadOnlyTree is what a lane is told when it reaches for a write in a worktree of another
	// repository it reads (ADR-0022).
	ReasonReadOnlyTree = "ropeshift policy: the worktrees of the other repositories are for reading. Do not commit, stage, push, " +
		"or change branches there: read them with git log, git show, and git grep, and work only in this lane's own worktree."
	// ReasonKept is what a lane is told when it reaches for the branch it continues, which stays as its
	// author left it (ADR-0023).
	ReasonKept = "ropeshift policy: a lane never moves the branch it continues. Do not push to it, move it, switch to it, or check it out: " +
		"read it with git log, git show, and git diff, and commit on this lane's own branch."
	// ReasonReadOnly is what a lane that only reads is told when it reaches for a write. Its worktree is
	// thrown away when it ends, so a file it writes there reaches nothing, but a commit, a push, or a ref
	// would reach the work it judges.
	ReasonReadOnly = "ropeshift policy: this lane reads and does not change anything. It may not edit a file with a tool, commit, push, or " +
		"move a branch or a tag. Read the work with git log, git diff, and git show, and say what you found through the " + ToolLaneReport + " tool."
	// ReasonLink is what a lane is told when a file program that runs with no prompt names a path through a
	// link (FR-LANE-19), because a link can lead out of the worktree.
	ReasonLink = "ropeshift policy: mkdir, rm, rmdir, and mv run on this lane only on paths with no link in them, because a link can lead " +
		"out of the worktree. Name the path inside the worktree that the link leads to."
	// ReasonReadOutside is what a session whose reads are scoped is told when it reads past its read roots.
	ReasonReadOutside = "ropeshift policy: this session reads only its own directory and the read-only trees of the project's repositories. " +
		"Read there, with a path the policy can read, and read the ledger with ropeshift."
)

// readTools read files, which a policy with read roots holds to them.
var readTools = []string{"Read", "Grep", "Glob", "LS", "NotebookRead"}

// filePrograms make, move, and delete files and directories, and generated settings let a lane that writes
// run them with no prompt (FR-LANE-19). Each changes every operand it names and runs no other program, so
// a hook that reads its operands holds it to the worktree. A program that copies from anywhere, edits in
// place, makes a link, or runs another is none of these, and asks as any other command does.
var filePrograms = []string{"mkdir", "rm", "rmdir", "mv"}

// FilePrograms are the programs a lane that writes runs with no prompt, on paths in its worktree.
func FilePrograms() []string { return slices.Clone(filePrograms) }

// LinkReader says whether a path names a link on the disk a policy guards. A missing path is no link, and
// an error is read as one, because a hook fails closed.
type LinkReader func(path string) (bool, error)

// DenyToolGit, as the tool of a DenyRule, names a git subcommand and not a vendor's tool: the rule denies
// that subcommand wherever a shell command runs it, whatever stands before it and however the command is
// nested, which a prefix of the whole command could not.
const DenyToolGit = "git"

// readOnlyGit are the subcommands that write a commit, a branch, a tag, or any other ref, or that move
// where HEAD stands on a branch. A lane that only reads runs none of them. The list is a guardrail and not
// a sandbox: a worktree with no branch is what keeps a branch where it was.
var readOnlyGit = []string{
	"commit", "push", "merge", "rebase", "cherry-pick", "revert", "reset", "am", "update-ref", "symbolic-ref",
	"stash", "fetch", "pull", "worktree", "switch", "checkout", "branch", "tag",
}

// ReadOnlyTools are the vendor tools that edit a file, which a lane that only reads may not call.
func ReadOnlyTools() []string {
	return []string{toolWrite, toolEdit, toolMultiEdit, toolNotebookEdit}
}

// ReadOnlyGit are the git subcommands a lane that only reads may not run.
func ReadOnlyGit() []string { return slices.Clone(readOnlyGit) }

// readOnlyRules are the denials of a lane that only reads: every tool that edits a file, and each
// subcommand of readOnlyGit.
func readOnlyRules() []DenyRule {
	rules := make([]DenyRule, 0, 4+len(readOnlyGit))
	for _, tool := range ReadOnlyTools() {
		rules = append(rules, DenyRule{Tool: tool, Reason: ReasonReadOnly})
	}
	for _, subcommand := range readOnlyGit {
		rules = append(rules, DenyRule{Tool: DenyToolGit, CommandPrefix: subcommand, Reason: ReasonReadOnly})
	}
	return rules
}

// PolicySpec is what a generation's policy compiles from.
type PolicySpec struct {
	Worktree string
	// WorktreeReal is the worktree with its links resolved, because the CLI reports
	// real paths, and `/tmp` is a link on macOS. Empty means the same path.
	WorktreeReal string
	Home         string
	// DefaultBranches are the branches no push may reach.
	DefaultBranches []string
	// Kept are the branches a lane continues, which nothing it runs may move.
	Kept []string
	// ReadOnlyRoots are worktrees of other repositories the lane reads, where no git command may write.
	ReadOnlyRoots []string
	// Writable are other directories a lane may write, such as its private temp.
	Writable []string
	// QuestionTool says what a call of the agent's own question tool becomes.
	QuestionTool string
	// AttachedMarker is the file a worker touches while a person is attached to the lane's pane.
	AttachedMarker string
	// OwnTools is the prefix of the lane's own tools.
	OwnTools string
	// Ground is what a session that lost its place is told of its work.
	Ground Grounding
	// ApprovalWaitMs bounds a hook that waits on a decision, and Gates are what it asks for or defers.
	ApprovalWaitMs int64
	Gates          []GateRule
	// GatesAskOnly makes a gated call ask and never defer.
	GatesAskOnly bool
	// ReadOnly makes a lane that reads and reports: it edits no file with a tool, and writes no commit
	// and no ref.
	ReadOnly bool
	// ReadRoots, when set, are the only directories the session may read with a file tool.
	ReadRoots []string
	// AskReadRoots, when set, are the only directories the session reads with no prompt: a read anywhere else asks.
	AskReadRoots []string
	// Program, when set, is the one program the session may run in a shell, and Commands are the only
	// commands of it the session may run, by name.
	Program  string
	Commands []string
	// Tools, when set, are the only tools the session may call.
	Tools []string
	// RefusedFlags are flags no command of Program may take, by name.
	RefusedFlags []string
	// Waits, when set, is the wait rule set the hook reads a shell command against.
	Waits *WaitRules
}

// ReasonHandOff is what a held lane reads at its next tool call.
const ReasonHandOff = "ropeshift policy: this lane must hand off now, and no other tool runs until it does. Call " +
	ToolLaneHandoff + " with what you alone know, and then end your turn."

// ReasonSpendLimit is what a lane held at its work order's hard spend limit reads at its next tool call. It says a
// limit was reached, and it names none: a lane learns of a limit only from this denial (FR-SPEND-9).
const ReasonSpendLimit = "ropeshift policy: this work order reached its spend limit, so this lane must hand off now, and no other tool runs until it does. Call " +
	ToolLaneHandoff + " with what you alone know, and then end your turn."

// ReasonSpendUnreadable is what a lane held because its work order's spend limit could not be read reads at its next
// tool call. The worker cannot say whether the lane is under its limit, and it does not run a lane it cannot hold to one.
const ReasonSpendUnreadable = "ropeshift policy: this lane's spend limit cannot be read, so this lane must hand off now, and no other tool runs until it does. Call " +
	ToolLaneHandoff + " with what you alone know, and then end your turn."

// HoldsTool says a hold stops this tool, and why.
func (p DenyPolicy) HoldsTool(tool string) (string, bool) {
	if p.Hold == "" || (p.OwnTools != "" && strings.HasPrefix(tool, p.OwnTools)) {
		return "", false
	}
	return p.Hold, true
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
	deny := []DenyRule{}
	if spec.ReadOnly {
		deny = readOnlyRules()
	}
	var waits *WaitRules
	if spec.Waits != nil {
		clone := spec.Waits.clone()
		waits = &clone
	}
	return DenyPolicy{
		Waits:    waits,
		OwnTools: spec.OwnTools, Roots: roots, Home: spec.Home, DefaultBranches: spec.DefaultBranches, Kept: spec.Kept, ReadOnlyRoots: spec.ReadOnlyRoots, Deny: deny, Ground: spec.Ground, QuestionTool: spec.QuestionTool,
		AttachedMarker: spec.AttachedMarker,
		ApprovalWaitMs: spec.ApprovalWaitMs, Gates: spec.Gates, GatesAskOnly: spec.GatesAskOnly, ReadRoots: slices.Clone(spec.ReadRoots), AskReadRoots: slices.Clone(spec.AskReadRoots),
		Program: spec.Program, Commands: slices.Clone(spec.Commands), Tools: slices.Clone(spec.Tools),
		RefusedFlags: slices.Clone(spec.RefusedFlags),
	}
}

// ReasonUnlistedTool is what a session held to a list of tools is told of any other.
const ReasonUnlistedTool = "ropeshift policy: this session calls only the tools it was given: reading and searching its files, " +
	"its shell for ropeshift commands, and its own context tools. Use one of those."

// Unlisted says a session held to a list of tools called one it does not list, and why that is denied.
func (p DenyPolicy) Unlisted(tool string) (string, bool) {
	if len(p.Tools) == 0 || slices.Contains(p.Tools, tool) {
		return "", false
	}
	return ReasonUnlistedTool, true
}

// ToolCall is the part of a tool call a policy reads.
type ToolCall struct {
	Tool    string
	Command string
	Path    string
	// Pattern is what a Glob matches, which can name a place of its own.
	Pattern string
	// Cwd is where the session stands, which a relative path is read against.
	Cwd string
	// Transcript is the file the session's CLI keeps its transcript in, which names the folder beside it where the CLI
	// saves a large tool output for the session to read.
	Transcript string
}

// RealPathReader says where a path leads on the disk a policy guards, through every link in it.
type RealPathReader func(path string) (string, error)

// WithRealPaths gives a policy the disk it guards, which it reads to hold a read to where a path leads.
func (p DenyPolicy) WithRealPaths(real RealPathReader) DenyPolicy {
	p.real = real
	return p
}

// WithLinks gives a policy the disk it guards, which it reads only for a file program that runs with no
// prompt. A policy with none reads what a call says and nothing else.
func (p DenyPolicy) WithLinks(links LinkReader) DenyPolicy {
	p.links = links
	return p
}

// Decide returns the reason of the first thing that denies a call. It reads what a call
// says, because a hook does no work: a link inside a worktree is the generated `Edit` rules'
// to catch, and a hook is a guardrail, not a sandbox. The one exception is a file program that
// runs with no prompt, whose targets it walks for a link when it has the disk (WithLinks).
func (p DenyPolicy) Decide(call ToolCall) (string, bool) {
	if reason, unlisted := p.Unlisted(call.Tool); unlisted {
		return reason, true
	}
	for _, rule := range p.Deny {
		if rule.Tool == call.Tool && strings.HasPrefix(call.Command, rule.CommandPrefix) {
			return rule.Reason, true
		}
	}
	if call.Tool == ToolBash && p.Program != "" && !runsOnly(call.Command, p.Program, p.Commands, p.RefusedFlags) {
		return reasonProgram(p.Program), true
	}
	switch call.Tool {
	case ToolBash, toolMonitor:
		var seen gitSeen
		if reason, denied := p.decideShell(call.Command, call.Cwd, 0, &seen); denied {
			return reason, true
		}
		reason, pushes, denied := p.decidePushes(call.Command)
		if denied {
			return reason, true
		}
		if (seen.pushes || pushes) && seen.commits {
			return ReasonPushApart, true
		}
		if wait, waits := p.WaitDenial(call); waits {
			return wait.Reason, true
		}
	case toolPowerShell:
		if reason, _, denied := p.decidePushes(call.Command); denied {
			return reason, true
		}
	case toolWrite, toolEdit, toolMultiEdit, toolNotebookEdit:
		if len(p.Roots) != 0 && !p.inside(shellWord{text: call.Path}, call.Cwd) {
			return ReasonOutside, true
		}
	}
	if slices.Contains(readTools, call.Tool) && len(p.ReadRoots) != 0 && !p.readable(call) {
		return ReasonReadOutside, true
	}
	return "", false
}

// readable says a file tool reads under a read root. A search with no path reads where the session
// stands, and a path the policy cannot read reads as outside, because this hook fails closed. A Glob's
// pattern names a place too: the part of it before its first wildcard must lie under a read root, and no
// part after one may climb out with `..`.
func (p DenyPolicy) readable(call ToolCall) bool {
	return p.readableUnder(call, p.ReadRoots, false)
}

// readableUnder is readable for any roots. A lenient reading takes a path that leads nowhere as inside when its name
// lies inside, because it reads nothing, and a person is not asked about it.
func (p DenyPolicy) readableUnder(call ToolCall, roots []string, lenient bool) bool {
	target := call.Path
	if target == "" {
		target = call.Cwd
	}
	if !p.readsUnderRoots(target, call.Cwd, roots, lenient) {
		return false
	}
	if call.Tool != "Glob" || call.Pattern == "" {
		return true
	}
	prefix, ok := globPrefix(call.Pattern)
	if !ok {
		return false
	}
	switch {
	case prefix == "":
		return true
	case path.IsAbs(prefix):
		return p.readsUnderRoots(prefix, call.Cwd, roots, lenient)
	}
	if !path.IsAbs(target) {
		target = call.Cwd + "/" + target
	}
	return p.readsUnderRoots(target+"/"+prefix, call.Cwd, roots, lenient)
}

// readsUnderRoots says a path lies under a root, by its name and, with the disk, by where it leads through
// every link in it: a link in a tree leads wherever the tree's content says. A path that leads nowhere reads as
// outside, because this hook fails closed, unless the reading is lenient.
func (p DenyPolicy) readsUnderRoots(target, cwd string, roots []string, lenient bool) bool {
	// A tool may read `~` or a variable as a place the policy cannot see, so neither reads as inside.
	if strings.HasPrefix(target, "~") || strings.Contains(target, "$") {
		return false
	}
	resolved, ok := p.resolve(shellWord{text: target}, cwd)
	if !ok {
		return false
	}
	return p.leadsUnder(target, resolved, cwd, roots, lenient)
}

// leadsUnder says a path whose name lies under a root also leads there through every link in it. The name as the tool
// gave it, uncleaned, is what the disk reads, so `link/..` climbs from where the link leads, as the system reads it. A
// lenient reading follows the part of a path that exists.
func (p DenyPolicy) leadsUnder(target, resolved, cwd string, roots []string, lenient bool) bool {
	if !under(resolved, roots) {
		return false
	}
	if p.real == nil {
		return true
	}
	raw := target
	if !path.IsAbs(raw) {
		raw = cwd + "/" + raw
	}
	leads, err := p.real(raw)
	for lenient && err != nil && raw != "/" {
		// A path that leads nowhere reads nothing, so it leads where the part of it that exists does.
		raw = path.Dir(raw)
		leads, err = p.real(raw)
	}
	if err != nil {
		return false
	}
	var real []string
	for _, root := range roots {
		if resolvedRoot, err := p.real(root); err == nil {
			real = append(real, resolvedRoot)
		}
	}
	return under(leads, real)
}

// under says a path is one of roots or lies below one.
func under(target string, roots []string) bool {
	for _, root := range roots {
		if target == root || strings.HasPrefix(target, root+"/") {
			return true
		}
	}
	return false
}

// globPrefix is the directory a Glob pattern names before its first wildcard, empty when it names none. A
// pattern that reads a place the policy cannot see, or climbs with `..` past a wildcard, has none to give.
func globPrefix(pattern string) (string, bool) {
	if strings.HasPrefix(pattern, "~") || strings.Contains(pattern, "$") {
		return "", false
	}
	wild := strings.IndexAny(pattern, "*?[{")
	head := pattern
	if wild >= 0 {
		head = pattern[:wild]
		for _, part := range strings.Split(pattern[wild:], "/") {
			if part == ".." {
				return "", false
			}
		}
	}
	slash := strings.LastIndex(head, "/")
	switch {
	case slash < 0:
		return "", true
	case slash == 0:
		return "/", true
	}
	return head[:slash], true
}

// shellKeywords lead a command inside a compound one.
var shellKeywords = []string{"if", "then", "elif", "else", "do", "while", "until", "!", "{", "}", "fi", "done"}

// shells take a command as an argument.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh"}

// Pushes says a shell command runs git push where the policy's own reading of commands finds one, however deep it
// stands. MayPush also takes the forms past that reading, and what cannot be read.
func Pushes(command string) bool {
	// The worker reads this for any call. Past the bound, MayPush and Asks take the command as unread.
	if len(command) > maxGateBytes {
		return false
	}
	var seen gitSeen
	_, _ = DenyPolicy{}.decideShell(command, "", 0, &seen)
	return seen.pushes
}

// gitSeen is what one call's commands did with git, however deep they stood. A stage and a commit run
// with no prompt and a push does not, so a push that shares a call with them would lean on the CLI's
// reading of a compound command to reach a person. A hook reads that on its own.
type gitSeen struct {
	pushes  bool
	commits bool
	// reads says a command reads a file where AskReadRoots do not reach (readsOutside).
	reads bool
}

func (p DenyPolicy) decideShell(text, cwd string, depth int, seen *gitSeen) (string, bool) {
	if depth > maxShellDepth {
		return ReasonOutside, true
	}
	commands, nested := parseShell(text)
	for _, inner := range nested {
		if reason, denied := p.decideShell(inner, cwd, depth+1, seen); denied {
			return reason, true
		}
	}
	at := cwd
	vars := &loopVars{words: map[string][]string{}}
	for ci, command := range commands {
		if len(p.AskReadRoots) != 0 {
			vars.note(command)
			for _, target := range command.reads {
				seen.reads = seen.reads || p.readsOutside(target, at, vars)
			}
		}
		if len(p.Roots) != 0 {
			for _, target := range command.writes {
				if !p.inside(target, at) {
					return ReasonOutside, true
				}
			}
		}
		for _, inner := range command.processes {
			if reason, denied := p.decideShell(inner, at, depth+1, seen); denied {
				return reason, true
			}
		}
		for _, words := range leaderReadings(command.words) {
			// Only a shell knows what a command word that expands names, and an argument of a wrapper that expands may be
			// the command or an option that takes it away, so no hold can read what the words run. A lane that only reads
			// runs none of it. A lane that writes leaves it to the gate reader, which asks.
			if words[0].expands && p.readOnly() {
				return ReasonReadOnly, true
			}
			name := path.Base(words[0].text)
			switch {
			case name == "cd":
				at = p.moved(words[1:], at)
			case name == "git":
				leaders := command.words[:len(command.words)-len(words)]
				if reason, denied := p.decideGit(leaders, words[1:], at, seen); denied {
					return reason, true
				}
			case name == "eval":
				if reason, denied := p.decideShell(joinWords(words[1:]), at, depth+1, seen); denied {
					return reason, true
				}
			case slices.Contains(shells, name):
				for _, inner := range shellArguments(words[1:]) {
					if reason, denied := p.decideShell(inner, at, depth+1, seen); denied {
						return reason, true
					}
				}
			case slices.Contains(textRunners, name):
				if reason, denied := p.decideTextRunner(name, words[1:], at, depth, seen); denied {
					return reason, true
				}
			default:
				if len(p.AskReadRoots) != 0 {
					in := readContext{vars: vars}
					if ci == 1 && len(commands) == 2 && command.piped && len(nested) == 0 && len(command.reads)+len(command.writes)+len(command.processes) == 0 &&
						!command.heredoc && len(command.fed) == 0 {
						in.source = &commands[0]
					}
					leaders := command.words[:len(command.words)-len(words)]
					if slices.ContainsFunc(leaders, func(word shellWord) bool { return path.Base(word.text) == "xargs" }) {
						seen.reads = seen.reads || p.xargsReads(command, at, in)
					} else {
						seen.reads = seen.reads || p.programReadsOutside(name, words[1:], at, command, in)
					}
				}
				if len(p.Roots) != 0 {
					for _, target := range writeTargets(name, words[1:]) {
						if !p.inside(target, at) {
							return ReasonOutside, true
						}
						if slices.Contains(filePrograms, name) && p.throughLink(target, at) {
							return ReasonLink, true
						}
					}
				}
			}
		}
	}
	return "", false
}

// decideTextRunner reads what a program that hands its words to a shell runs: each argument that holds a byte
// a shell reads is a command line, as the gate reader takes it. Parallel also builds a command from the words
// that follow its options, up to the first of its inputs. A parallel with only options runs the lines it
// reads, which no hold can read, so a lane that only reads and a lane that writes are both refused it.
func (p DenyPolicy) decideTextRunner(name string, args []shellWord, at string, depth int, seen *gitSeen) (string, bool) {
	for _, word := range args {
		if !strings.ContainsAny(word.text, shellSpecial) {
			continue
		}
		if reason, denied := p.decideShell(word.text, at, depth+1, seen); denied {
			return reason, true
		}
	}
	if name != "parallel" {
		return "", false
	}
	return p.decideParallel(args, at, depth, seen)
}

// decideParallel reads the command parallel builds from the words after its options, up to its inputs. An option that
// takes a value takes the word after it, which is no command, and an option the policy does not know may take it or not,
// so its words are read from each word that is no option: the policy refuses what any of them runs.
func (p DenyPolicy) decideParallel(args []shellWord, at string, depth int, seen *gitSeen) (string, bool) {
	// ::: and :::: both begin with the first, and each opens the inputs.
	inputs := slices.IndexFunc(args, func(word shellWord) bool { return strings.HasPrefix(word.text, ":::") })
	words := args
	if inputs >= 0 {
		words = args[:inputs]
	}
	start, unsure := -1, false
	for i := 0; i < len(words) && start < 0; i++ {
		if !strings.HasPrefix(words[i].text, "-") {
			start = i
			break
		}
		takes, known := parallelOption(words[i].text)
		unsure = unsure || !known
		if takes {
			i++
		}
	}
	if start < 0 {
		// With no command, parallel runs the lines it reads, which no hold can read, or the inputs it is given, which were
		// read above.
		if inputs >= 0 {
			return "", false
		}
		if p.readOnly() {
			return ReasonReadOnly, true
		}
		return ReasonOutside, true
	}
	starts := []int{start}
	if unsure {
		starts = starts[:0]
		for i, word := range words {
			if !strings.HasPrefix(word.text, "-") {
				starts = append(starts, i)
			}
		}
	}
	for _, from := range starts {
		if reason, denied := p.decideShell(joinWords(words[from:]), at, depth+1, seen); denied {
			return reason, true
		}
	}
	return "", false
}

// parallelOption reads one option word of parallel: whether its value is the next word, and whether the policy knows
// the option at all. A short option that takes a value takes the rest of its word as that value when there is one.
func parallelOption(text string) (takes, known bool) {
	if strings.HasPrefix(text, "--") {
		flag, _, joined := strings.Cut(text, "=")
		valued := slices.Contains(parallelValued, flag)
		return valued && !joined, valued || slices.Contains(parallelFlags, flag)
	}
	for i := 1; i < len(text); i++ {
		letter := "-" + text[i:i+1]
		switch {
		case slices.Contains(parallelValued, letter):
			return i+1 == len(text), true
		case !slices.Contains(parallelFlags, letter):
			return false, false
		}
	}
	return false, true
}

// readOnly says the policy is a lane's that only reads, which denies a git subcommand with ReasonReadOnly.
func (p DenyPolicy) readOnly() bool {
	return slices.ContainsFunc(p.Deny, func(rule DenyRule) bool { return rule.Reason == ReasonReadOnly })
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
		default:
			wrapper, wraps := gateWrappers[path.Base(first.text)]
			if !wraps {
				return words
			}
			words = stripWrapper(wrapper, words[1:])
		}
	}
	return nil
}

// optionWrappers are the wrappers whose options alone stripOptions takes away.
var optionWrappers = []string{"command", "builtin", "exec", "env", "nice", "nohup", "time", "sudo", "doas", "xargs", "timeout"}

// stripOptions is stripLeaders, taking no word as a wrapper option's value or as an operand.
func stripOptions(words []shellWord) []shellWord {
	for len(words) != 0 {
		first := words[0]
		switch {
		case slices.Contains(shellKeywords, first.text), isAssignment(first.text):
			words = words[1:]
		case slices.Contains(optionWrappers, path.Base(first.text)):
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

// leaderReadings are the commands words may run. An option such as `sudo -h`, `env -P`, or `env -S` takes a value
// on one system or in one form and none in another, so a hold reads the words both ways.
func leaderReadings(words []shellWord) [][]shellWord {
	var out [][]shellWord
	valued, plain := stripLeaders(words), stripOptions(words)
	if len(valued) != 0 {
		out = append(out, valued)
	}
	if len(plain) != 0 && len(plain) != len(valued) {
		out = append(out, plain)
	}
	return out
}

// stripWrapper takes away a wrapper's own words: its options with the words they take, assignments, and the
// operands that stand before its command, such as timeout's duration.
func stripWrapper(wrapper gateWrapper, words []shellWord) []shellWord {
	operands, options := wrapper.operands, true
	for len(words) != 0 {
		text := words[0].text
		switch {
		case options && text == "--":
			options = false
		case options && strings.HasPrefix(text, "-"):
			if _, _, takes := readOption(text, wrapper.valued); takes && !words[0].expands && len(words) > 1 {
				words = words[1:]
			}
		case isAssignment(text):
		case operands > 0:
			operands--
		default:
			return words
		}
		words = words[1:]
	}
	return words
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

// shellArguments find the text of `sh -c`, in any bundle of flags that holds `c`: the word after that bundle, and
// the first word after every option and the operands of `o` and `O`, as the gate reader takes them. Shells differ on
// an option written against its letter, so a hold reads both.
func shellArguments(words []shellWord) []string {
	var texts []string
	for i, word := range words {
		if !strings.HasPrefix(word.text, "-") || strings.HasPrefix(word.text, "--") {
			break
		}
		if strings.Contains(word.text, "c") && i+1 < len(words) {
			texts = append(texts, words[i+1].text)
			break
		}
	}
	coded, at := false, 0
options:
	for at < len(words) {
		text := words[at].text
		switch {
		case text == "-" || text == "--":
			at++
			break options
		case !strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "+"):
			break options
		}
		at++
		if strings.HasPrefix(text, "--") {
			if text == "--rcfile" || text == "--init-file" {
				at++
			}
			continue
		}
		for _, letter := range text[1:] {
			switch letter {
			case 'c':
				coded = coded || text[0] == '-'
			case 'o', 'O':
				at++
			}
		}
	}
	if coded && at < len(words) && !slices.Contains(texts, words[at].text) {
		texts = append(texts, words[at].text)
	}
	return texts
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

// gitPlaceOptions say where a command works, before its subcommand.
var gitPlaceOptions = []string{"--git-dir", "--work-tree"}

// gitPlaceVariables say the same from the environment.
var gitPlaceVariables = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY"}

// assignedPlaces reads what stands before a command for where it would work.
func assignedPlaces(leaders []shellWord) []shellWord {
	var places []shellWord
	for _, word := range leaders {
		name, value, found := strings.Cut(word.text, "=")
		if found && slices.Contains(gitPlaceVariables, name) {
			places = append(places, shellWord{text: value, dynamic: word.dynamic})
		}
	}
	return places
}

func (p DenyPolicy) decideGit(leaders, args []shellWord, at string, seen *gitSeen) (string, bool) {
	places := assignedPlaces(leaders)
	for len(args) != 0 && strings.HasPrefix(args[0].text, "-") {
		option := args[0]
		name, value, joined := strings.Cut(option.text, "=")
		switch {
		case joined && slices.Contains(gitPlaceOptions, name):
			places = append(places, shellWord{text: value, dynamic: option.dynamic})
		case option.text == "-C" && len(args) > 1:
			at = p.moved(args[1:2], at)
		case slices.Contains(gitPlaceOptions, option.text) && len(args) > 1:
			places = append(places, args[1])
		}
		if takesGitValue(option.text) {
			args = args[1:]
		}
		if len(args) != 0 {
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return "", false
	}
	for _, rule := range p.Deny {
		if rule.Tool == DenyToolGit && rule.CommandPrefix == args[0].text {
			return rule.Reason, true
		}
	}
	if p.namesKept(args[0].text, args[1:]) {
		return ReasonKept, true
	}
	if p.inReadOnlyTree(args[0].text, places, at) {
		return ReasonReadOnlyTree, true
	}
	switch args[0].text {
	case "push":
		seen.pushes = true
		return p.decidePush(args[1:])
	case "add", "commit", "rm", "mv":
		seen.commits = true
		return p.decideStage(places, at)
	}
	return "", false
}

// decideStage holds a stage, a staged deletion or rename, or a commit to this lane's worktree, which is
// what lets each run with no prompt. A place it cannot read reads as outside.
func (p DenyPolicy) decideStage(places []shellWord, at string) (string, bool) {
	if len(p.Roots) == 0 {
		return "", false
	}
	if !p.inside(shellWord{text: at}, at) {
		return ReasonOutside, true
	}
	for _, place := range places {
		if !p.inside(place, at) {
			return ReasonOutside, true
		}
	}
	return "", false
}

// writesInTree are the subcommands that write in the worktree they run in: readOnlyGit's, a stage, and a
// staged deletion or rename.
var writesInTree = append(slices.Clone(readOnlyGit), "add", "rm", "mv")

// inReadOnlyTree says a git command that writes runs in a read-only worktree, where it stands or where its
// options point.
func (p DenyPolicy) inReadOnlyTree(subcommand string, places []shellWord, at string) bool {
	if len(p.ReadOnlyRoots) == 0 || !slices.Contains(writesInTree, subcommand) {
		return false
	}
	for _, place := range append([]shellWord{{text: at}}, places...) {
		resolved, ok := p.resolve(place, at)
		if !ok {
			continue
		}
		for _, root := range p.ReadOnlyRoots {
			if resolved == root || strings.HasPrefix(resolved, root+"/") {
				return true
			}
		}
	}
	return false
}

// movesBranches are the subcommands that move, make, delete, or check out the branch they name.
var movesBranches = []string{"branch", "switch", "checkout", "update-ref"}

// namesKept says a command moves or checks out a kept branch: a subcommand of movesBranches that names one,
// as a branch or as its full ref.
func (p DenyPolicy) namesKept(subcommand string, args []shellWord) bool {
	if len(p.Kept) == 0 || !slices.Contains(movesBranches, subcommand) {
		return false
	}
	for _, word := range args {
		if slices.Contains(p.Kept, strings.TrimPrefix(word.text, "refs/heads/")) {
			return true
		}
	}
	return false
}

// pushOptionsWithValue are the options of `git push` that take the next word.
var pushOptionsWithValue = []string{"-o", "--push-option", "--repo", "--receive-pack", "--exec"}

// isForceOption reads a forcing option, also cut short as git takes a long option.
func isForceOption(text string) bool {
	if strings.HasPrefix(text, "--forc") {
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
		case pushesEverything(text):
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
		destination = strings.TrimPrefix(strings.TrimPrefix(destination, "refs/"), "heads/")
		if slices.Contains(p.DefaultBranches, destination) {
			return ReasonDefaultBranch, true
		}
		if slices.Contains(p.Kept, destination) {
			return ReasonKept, true
		}
	}
	return "", false
}

// pushesEverything reads an option that pushes every branch, also cut short as far as it stays the one option.
func pushesEverything(text string) bool {
	return (len(text) >= len("--al") && strings.HasPrefix("--all", text)) ||
		(len(text) >= len("--m") && strings.HasPrefix("--mirror", text)) ||
		(len(text) >= len("--b") && strings.HasPrefix("--branches", text))
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
	// The path of a process substitution is a pipe. Written out, it is whatever the descriptor holds.
	if slices.Contains(quietDevices, resolved) || (word.process && resolved == processPath) {
		return true
	}
	for _, root := range p.Roots {
		if resolved == root || strings.HasPrefix(resolved, root+"/") {
			return true
		}
	}
	return false
}

// throughLink says a path a file program would change passes through a link below the root that holds
// it, or is one. The last part counts, because `rm -r link/` empties what the link names and `mv a link`
// moves into it. A path the policy cannot read reads as through one, and so does a part the disk cannot
// say of. Parts past what exists hold no link yet. With no disk to read, it reads none.
func (p DenyPolicy) throughLink(word shellWord, at string) bool {
	if p.links == nil {
		return false
	}
	resolved, ok := p.resolve(word, at)
	if !ok {
		return true
	}
	for _, root := range p.Roots {
		rest, below := strings.CutPrefix(resolved, root+"/")
		if !below {
			continue
		}
		walked := root
		for _, part := range strings.Split(rest, "/") {
			walked = path.Join(walked, part)
			link, err := p.links(walked)
			if err != nil || link {
				return true
			}
		}
		return false
	}
	return false
}

// BypassMode is the agent's mode that asks nothing and lets every call through. No lane runs in it
// (NFR-SEC-3): a lane in it would push and run every gated action with nobody asked (FR-LANE-19).
const BypassMode = "bypassPermissions"

// ErrBypassMode means a session was to start in BypassMode.
var ErrBypassMode = errors.New("no lane runs in a permission-bypass mode")

// CheckPermissionMode refuses a mode no lane may run in: the bypass mode, and a mode this does not know,
// because an unknown mode could be one.
func CheckPermissionMode(mode string) error {
	if mode == BypassMode {
		return fmt.Errorf("%w: %w", ErrBadProfile, ErrBypassMode)
	}
	if _, known := permissionRanks[mode]; !known {
		return fmt.Errorf("%w: permission_mode %q is none of plan, default, acceptEdits, dontAsk, and auto", ErrBadProfile, mode)
	}
	return nil
}

// permissionRanks orders the modes from most careful to least: plan, which only plans, dontAsk, which refuses what
// would ask, default, acceptEdits, auto, and the bypass mode. Anything else outranks them all, because a mode nobody
// here knows must not pass as a lower one.
var permissionRanks = map[string]int{"plan": 0, "dontAsk": 1, "default": 2, "acceptEdits": 3, "auto": 4, BypassMode: 5}

// RaisesPermission reports a mode that is less careful than another.
func RaisesPermission(from, to string) bool {
	rank, known := permissionRanks[to]
	if !known {
		return true
	}
	return rank > permissionRanks[from]
}
