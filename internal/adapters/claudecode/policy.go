package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// usualDefaults are guarded whatever a repository reports, because a failed read of its
// default branch must not open it.
var usualDefaults = []string{"main", "master"}

// maxEditRules bounds what a generation's settings hold. The nearest levels come first,
// because other lanes and the repository sit there, and the hook covers what this drops.
const maxEditRules = 2000

func defaultBranches(spec ports.SessionSpec) []string {
	branches := slices.Clone(usualDefaults)
	if spec.DefaultBranch != "" && !slices.Contains(branches, spec.DefaultBranch) {
		branches = append(branches, spec.DefaultBranch)
	}
	return branches
}

func homeOf(spec ports.SessionSpec) string {
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			return value
		}
	}
	return ""
}

// compilePolicy builds the file a generation's hook reads, so a denial needs no worker
// (FR-AUTH-8). It runs before the session starts, because the hook touches nothing else.
func compilePolicy(spec ports.SessionSpec) lanes.DenyPolicy {
	worktree := spec.Dir
	real, err := filepath.EvalSymlinks(spec.Dir)
	if err != nil {
		real = worktree
	}
	var writable []string
	if spec.TempDir != "" {
		writable = append(writable, spec.TempDir)
	}
	var readOnly []string
	for _, dir := range spec.ReadOnlyDirs {
		readOnly = append(readOnly, dir)
		if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
			readOnly = append(readOnly, resolved)
		}
	}
	var readRoots, askReadRoots []string
	program := ""
	if spec.Assistant {
		readRoots, program = readRootsOf(spec), spec.Binary
	} else {
		askReadRoots = laneReadRoots(spec)
	}
	var waits *lanes.WaitRules
	if spec.Waits.Version != 0 {
		waits = &spec.Waits
	}
	policy := lanes.CompilePolicy(lanes.PolicySpec{
		Waits:    waits,
		Worktree: worktree, WorktreeReal: real, Home: homeOf(spec), DefaultBranches: defaultBranches(spec), Kept: spec.Kept, Writable: writable,
		ReadOnlyRoots: readOnly,
		QuestionTool:  questionTool(spec), AttachedMarker: attachedMarker(spec), OwnTools: toolRule(""), ReadOnly: spec.ReadOnly || spec.Assistant,
		Ground:         lanes.Grounding{Contract: spec.Contract, ContractVersion: spec.ContractVersion, Pack: spec.SystemPromptFile},
		ApprovalWaitMs: spec.ApprovalWait.Milliseconds(), Gates: policyGates(spec), GatesAskOnly: spec.RunMode == ports.RunModeInteractive, ReadRoots: readRoots, AskReadRoots: askReadRoots,
		Program: program, Commands: commandsOf(spec), Tools: assistantCalls(spec), RefusedFlags: refusedFlagsOf(spec),
	})
	if spec.Assistant {
		for _, tool := range assistantDenied {
			policy.Deny = append(policy.Deny, lanes.DenyRule{Tool: tool, Reason: reasonNoWeb})
		}
	}
	return policy
}

// laneReadRoots are the directories a lane reads with no prompt: its worktree, its git directory and the common git
// directory it shares with its clone, the read-only trees of other repositories, and its private temp directory, each by
// its name and the name its links resolve to. The hook asks for a read anywhere else, because the CLI's own `auto` mode
// runs such a read with no prompt.
func laneReadRoots(spec ports.SessionSpec) []string {
	roots := readRootsOf(spec)
	for _, dir := range laneGitDirs(spec) {
		roots = withRoot(roots, dir)
		if named := underClonesName(spec.Repository, dir); named != "" {
			roots = withRoot(roots, named)
		}
	}
	if spec.TempDir != "" {
		temp := filepath.Clean(spec.TempDir)
		roots = append(roots, temp)
		if real, err := filepath.EvalSymlinks(temp); err == nil && real != temp {
			roots = append(roots, real)
		}
	}
	return roots
}

// underClonesName spells a git directory git reports by real path with the name its clone is given, which is the
// spelling a lane's tool calls use when that name is a link. It is empty when the directory is not under the clone or
// the clone's name is its real path.
func underClonesName(clone, dir string) string {
	if clone == "" {
		return ""
	}
	clone = filepath.Clean(clone)
	real, err := filepath.EvalSymlinks(clone)
	if err != nil || real == clone {
		return ""
	}
	rel, err := filepath.Rel(real, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.Join(clone, rel)
}

// gitDirsTimeout bounds the reads of a lane's git directories, which a policy is compiled with before its session starts.
const gitDirsTimeout = 10 * time.Second

// laneGitDirs are what `git rev-parse --git-dir` and `--git-common-dir` give for a lane's worktree: the git directory
// its clone registered for it, and the common one the two share, which holds the objects, the refs, and the
// configuration git reads in every command. They are read from the clone, never from the `.git` file of the worktree,
// which a lane can edit to name any directory, so a lane that does reads nothing more with no prompt (registeredGitDir).
// A lane with no clone, or whose registration does not read, has none.
func laneGitDirs(spec ports.SessionSpec) []string {
	if spec.Assistant {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitDirsTimeout)
	defer cancel()
	dir, ok := registeredGitDir(ctx, spec)
	if !ok {
		return nil
	}
	dirs := []string{dir}
	if common, err := repositoryGit(spec).Text(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && common != "" {
		dirs = append(dirs, common)
	}
	return dirs
}

// assistantCalls are the only tools an assistant's session may call: its built-in tools and ropeshift's
// context tools. Any other, a vendor tool, a tool of another MCP server, or one a later CLI adds, its hook
// denies. Every other session calls what its rules allow.
func assistantCalls(spec ports.SessionSpec) []string {
	if !spec.Assistant {
		return nil
	}
	calls := slices.Clone(assistantBuiltIns)
	for _, tool := range spec.Tools {
		calls = append(calls, toolRule(tool))
	}
	return calls
}

// refusedFlagsOf are the flags no command of an assistant's shell may take: those that would point it at
// another listener, which would receive its credential.
func refusedFlagsOf(spec ports.SessionSpec) []string {
	if !spec.Assistant {
		return nil
	}
	return slices.Clone(authority.RedirectingFlags)
}

// commandsOf are the commands an assistant's shell may run, and none for any other session, whose shell no
// list holds.
func commandsOf(spec ports.SessionSpec) []string {
	if !spec.Assistant {
		return nil
	}
	return slices.Clone(spec.Commands)
}

// reasonNoWeb is what an assistant's session is told when it reaches for the web.
const reasonNoWeb = "ropeshift policy: the assistant does not use the web. Answer from the project's records and its read-only trees."

// holdPolicy changes the one field a worker changes after a start. The file goes by a rename,
// because a hook reads it with no lock, and a file it cannot read denies every tool.
func holdPolicy(path, reason string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var policy lanes.DenyPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return err
	}
	policy.Hold = reason
	raw, err = json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(append(raw, '\n'))
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// questionTool is what the agent's own question tool becomes. Only `-p` honors `defer`, so an
// interactive session is denied and told to use `ask`. A planning lane's person answers in the
// conversation, so while one is attached the tool is left to the terminal, and the denial stands for
// the rest of the time.
func questionTool(spec ports.SessionSpec) string {
	switch {
	case spec.RunMode != ports.RunModeInteractive:
		return lanes.QuestionToolDefer
	case spec.Planning:
		return lanes.QuestionToolTerminalIfAttached
	}
	return lanes.QuestionToolDeny
}

// markerPath is the file a worker touches while a person is attached to a generation's pane. It sits
// beside the policy that names it, outside every directory a lane may write. A session with no
// directory of its own, as a probe's has none, gets no marker, because a relative path would land
// wherever the worker stands.
func markerPath(spec ports.SessionSpec) string {
	if spec.ConfigDir == "" {
		return ""
	}
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("attached-%d", spec.Generation))
}

// toolsListedPath is the file ropeshift's MCP server makes once it has answered its CLI's tools/list, which a pane's
// driver holds the first prompt for (awaitTools). It sits in the generation's own directory, as the attached marker does.
// A session has none where its first turn already waits for its servers, as a headless session's does
// (https://code.claude.com/docs/en/agent-sdk/mcp#connection-timing), where it has no directory of its own, and where it
// has no server of ropeshift's: a reference to a variable its environment lacks would fail the server's configuration, so
// no part of the session names the file then.
func toolsListedPath(spec ports.SessionSpec) string {
	if spec.RunMode != ports.RunModeInteractive || spec.ConfigDir == "" || spec.Binary == "" {
		return ""
	}
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("tools-listed-%d", spec.Generation))
}

// clearToolsListed removes the file an earlier start of the same generation may have left, because it says nothing of
// the server this start's CLI runs.
func clearToolsListed(spec ports.SessionSpec) error {
	path := toolsListedPath(spec)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// attachedMarker is what a policy names for a hook to read. Only a pane has anyone to attach.
func attachedMarker(spec ports.SessionSpec) string {
	if spec.RunMode != ports.RunModeInteractive {
		return ""
	}
	return markerPath(spec)
}

// denyRules writes each denial as a deny rule too: a `Bash` rule for a command, and an
// `Edit` rule for a path, because a `Bash` rule does not match a redirect target.
func denyRules(spec ports.SessionSpec) []string {
	rules := []string{"Bash(git push --force*)", "Bash(git push -f*)", "Bash(git push * --force*)", "Bash(git push * -f*)"}
	for _, branch := range slices.Concat(defaultBranches(spec), spec.Kept) {
		rules = append(rules, "Bash(git push * "+branch+")", "Bash(git push * *:"+branch+")", "Bash(git push * +"+branch+")")
	}
	if spec.ReadOnly || spec.Assistant {
		rules = append(rules, readOnlyRules()...)
	}
	if spec.Assistant {
		rules = append(rules, assistantDenied...)
		for _, command := range append(slices.Clone(authority.MachineLocalCommands), spec.Routes...) {
			rules = append(rules, "Bash("+lanes.CommandWord(spec.Binary)+" "+command+"*)")
		}
	}
	for _, dir := range spec.ReadOnlyDirs {
		rules = append(rules, "Edit(/"+dir+")", "Edit(/"+dir+"/**)")
	}
	rules = append(rules, laneDenyRules(spec)...)
	return append(rules, editRules(spec.Dir)...)
}

// readOnlyRules deny a session that only reads every tool that edits a file and every git subcommand
// that writes a commit or a ref. The CLI enforces them whatever the permission mode, and the hook says
// the same from the generation's policy, so a lane with nobody to answer a prompt is denied and not left
// waiting.
func readOnlyRules() []string {
	rules := lanes.ReadOnlyTools()
	for _, subcommand := range lanes.ReadOnlyGit() {
		rules = append(rules, "Bash(git "+subcommand+"*)")
	}
	return rules
}

// editRules names what lies beside the worktree at each level above it, because no rule
// can say "everything but this directory", and a deny always beats an allow. Something
// created later is the hook's to catch. A name a rule cannot hold is the hook's too, and
// so is a write through a link back to the worktree, which reads as outside.
func editRules(worktree string) []string {
	if worktree == "" {
		return nil
	}
	real, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		real = worktree
	}
	dir := real
	var rules []string
	for dir != string(filepath.Separator) && dir != "." {
		parent := filepath.Dir(dir)
		entries, err := os.ReadDir(parent)
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if name == filepath.Base(dir) || strings.ContainsAny(name, patternCharacters) || leadsToWorktree(filepath.Join(parent, name), real) {
					continue
				}
				rules = append(rules, "Edit(/"+filepath.Join(parent, name)+")", "Edit(/"+filepath.Join(parent, name)+"/**)")
			}
		}
		dir = parent
	}
	if len(rules) > maxEditRules {
		rules = rules[:maxEditRules]
	}
	return rules
}

// leadsToWorktree reports a link whose target holds the worktree or lies inside it. A deny
// rule written through a link applies at the link's real location too, so a rule on one of
// these would deny every write in the worktree. `/tmp` is such a link on macOS.
func leadsToWorktree(path, worktree string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return within(worktree, target) || within(target, worktree)
}

// within reports a path at or under a directory.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// askRules make a push on a lane that writes, and each action a profile gates, ask in every mode, as the call is
// written. An ask rule beats an allow rule and holds in a mode that asks nothing, and one on a call that would ask
// anyway changes nothing, so no mode needs its own reading. The hook asks for a push and a gated shell command in
// every other form (lanes.DenyPolicy.Asks, FR-LANE-19).
func askRules(spec ports.SessionSpec) []string {
	var rules []string
	if !spec.OnlyTools && !spec.ReadOnly {
		rules = append(rules, pushRules...)
	}
	for _, rule := range slices.Concat(spec.Gates, perUseRules(spec)) {
		if !slices.Contains(rules, rule) {
			rules = append(rules, rule)
		}
	}
	return rules
}

// policyGates are what the hook holds as gated, in both run modes. Only `-p` honors `defer`, and a pane has a
// screen for its wait, so a pane's policy asks for each and defers none (GatesAskOnly).
func policyGates(spec ports.SessionSpec) []lanes.GateRule {
	gates := make([]lanes.GateRule, 0, len(spec.Gates))
	for _, rule := range spec.Gates {
		gates = append(gates, lanes.ReadGate(rule))
	}
	return gates
}
