package claudecode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
)

// pieceDirs are the directories of a lane's worktree whose files can declare hooks in their frontmatter, which a lane can
// edit as it edits its settings. Skills and subagents take hooks "in the same configuration format as settings-based
// hooks" ([hooks]), and a command file "supports the same frontmatter" as a skill ([skills]). A skill's hooks run once
// it is invoked and a subagent's while it runs, so a hook a lane adds or changes there runs as a settings hook does, and
// holds the lane the same way.
//
// [hooks]: https://code.claude.com/docs/en/hooks#hooks-in-skills-and-agents
// [skills]: https://code.claude.com/docs/en/skills#frontmatter-reference
var pieceDirs = []string{".claude/skills", ".claude/agents", ".claude/commands"}

// skillsDir is the piece directory whose files define a skill only as its SKILL.md, the one file of a skill with
// frontmatter the CLI reads.
const skillsDir = ".claude/skills"

// A `.claude/skills/` directory below the worktree's root holds skills too: they "load the first time Claude reads or
// edits a file in that subdirectory and stay available for the rest of the session" ([skills]), so a hook a lane writes
// into one runs as a root skill's does, and the hold reads them as it reads the root's. The page names no directory
// discovery leaves out, such as one git ignores, so finding them reads the whole worktree but `.git`, in two parts:
//
//   - Every path git sees, its tracked files and the untracked ones it does not ignore, is read in full from git's own
//     listing, which runs through the git directory the clone records for the worktree and never the worktree's own
//     `.git` file, which a lane can edit (gitListing).
//   - The directories git ignores, such as `node_modules`, and those a link or a submodule git lists leads to, are walked
//     by their names alone, through links, each once, for a `.claude` that holds a `skills` directory, reading no file,
//     up to nestedWalkWait. Past it, the skills in them were not compared, and the hold says that the size of the tree,
//     and no change of the lane's, holds it.
//
// A worktree git cannot list is walked by names alone, whole. A running session's check reads the nested directories
// its start found (nestedDirs), because a reload has milliseconds: a nested skill a lane adds during a session in a
// directory the start did not find is held at the next generation's start.
//
// [skills]: https://code.claude.com/docs/en/skills#load-skills-in-monorepos-and-subdirectories
var nestedWalkWait = 3 * time.Second

// nestedRoot is the path a hold names for the nested skills it could not compare.
const nestedRoot = "."

// keyHooks is the key of a settings file and of a piece's frontmatter that holds its hooks.
const keyHooks = "hooks"

// maxPieceDepth bounds how far below a piece directory the worktree is read, links included.
const maxPieceDepth = 16

// isPiece says a file, by its slash-separated path in the worktree, is one whose frontmatter the CLI reads: a skill's
// SKILL.md, or any Markdown file of the subagents or commands.
func isPiece(path string) bool {
	name := pathpkg.Base(path)
	if strings.HasPrefix(path, skillsDir+"/") || strings.Contains(path, "/"+skillsDir+"/") {
		return strings.EqualFold(name, "SKILL.md")
	}
	return strings.EqualFold(pathpkg.Ext(name), ".md")
}

// nestedScan is what a walk of a worktree found of the skills directories below its root: each one's slash-separated
// path, and whether the walk of the directories git ignores ended within nestedWalkWait.
type nestedScan struct {
	dirs   []string
	within bool
}

// The modes of the entries git lists that lead to a directory it does not list into: a link, and a submodule.
const (
	linkMode      = "120000"
	submoduleMode = "160000"
)

// worktreeGit runs git in a lane's worktree, a folder ropeshift does not trust, as the reads of a member's clone run:
// with the lane's environment, no hook, no file system monitor, no lazy fetch of a missing object, and, as each
// listing asks, no optional lock, so a read never rewrites the worktree's index.
func worktreeGit(spec ports.SessionSpec) gitcli.Git {
	return gitcli.Git{
		Dir: spec.Dir, Env: spec.Env, Extra: []string{gitcli.NoLazyFetch},
		Config: append(gitcli.NoHooks(), [2]string{"core.fsmonitor", "false"}),
	}
}

// registeredGitDir is the git directory the lane's clone records for the lane's worktree, which git is told to list the
// worktree through. A lane can edit the `.git` file of its worktree, and git run there follows it to a git directory of
// the lane's own, whose `core.worktree` can name another folder, so every listing succeeds and lists nothing. So the
// directory is read from the clone's own registrations, `.git/worktrees/<name>` whose `gitdir` file names this
// worktree's `.git`, or from the clone itself when the worktree is its own, and never from the worktree's `.git`. It
// says no unless exactly one registration names the worktree and git, run in that directory with no work tree given,
// names the worktree as its top level, which a `core.worktree` set for that directory would not.
func registeredGitDir(ctx context.Context, spec ports.SessionSpec) (string, bool) {
	if spec.Repository == "" || spec.Dir == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(spec.Dir)
	if err != nil {
		return "", false
	}
	found, err := repositoryGit(spec).Text(ctx, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir")
	paths := strings.Split(found, "\n")
	if err != nil || len(paths) != 3 {
		return "", false
	}
	dir := ""
	if clone, err := filepath.EvalSymlinks(paths[0]); err == nil && clone == root {
		dir = paths[1]
	} else if dir = registrationOf(paths[2], root); dir == "" {
		return "", false
	}
	top, err := worktreeGit(spec).Text(ctx, "--git-dir="+dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(top); err != nil || real != root {
		return "", false
	}
	return dir, true
}

// registrationOf is the registration under a clone's common git directory whose `gitdir` file names the `.git` of the
// worktree at root, a path with its links resolved, or empty when none does or more than one does.
func registrationOf(common, root string) string {
	registrations := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(registrations)
	if err != nil {
		return ""
	}
	found := ""
	for _, entry := range entries {
		dir := filepath.Join(registrations, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "gitdir"))
		if err != nil {
			continue
		}
		named := strings.TrimRight(string(raw), "\r\n")
		if !filepath.IsAbs(named) {
			named = filepath.Join(dir, named)
		}
		if filepath.Base(named) != ".git" {
			continue
		}
		if real, err := filepath.EvalSymlinks(filepath.Dir(named)); err != nil || real != root {
			continue
		}
		if found != "" {
			return ""
		}
		found = dir
	}
	return found
}

// gitListing is what git lists of a lane's worktree: each path it sees, a tracked one and an untracked one it does not
// ignore, each path a link or a submodule git lists leads to a directory through, and each path it ignores, a directory
// as a whole, ending with a slash. It lists through the git directory the clone records for the worktree and with the
// worktree named as its work tree (registeredGitDir), and says whether git could list the worktree that way.
func gitListing(spec ports.SessionSpec) (seen, leads, ignored []string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	dir, ok := registeredGitDir(ctx, spec)
	if !ok {
		return nil, nil, nil, false
	}
	git, place := worktreeGit(spec), []string{"--git-dir=" + dir, "--work-tree=" + spec.Dir, "--no-optional-locks"}
	staged, err := git.Output(ctx, append(slices.Clone(place), "ls-files", "-z", "--stage")...)
	if err != nil {
		return nil, nil, nil, false
	}
	untracked, err := git.Output(ctx, append(slices.Clone(place), "ls-files", "-z", "--others", "--exclude-standard")...)
	if err != nil {
		return nil, nil, nil, false
	}
	skipped, err := git.Output(ctx, append(slices.Clone(place), "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")...)
	if err != nil {
		return nil, nil, nil, false
	}
	for _, entry := range nulFields(staged) {
		mode, path, found := strings.Cut(entry, "\t")
		if !found {
			continue
		}
		seen = append(seen, path)
		if strings.HasPrefix(mode, linkMode+" ") || strings.HasPrefix(mode, submoduleMode+" ") {
			leads = append(leads, path)
		}
	}
	for _, path := range nulFields(untracked) {
		seen = append(seen, path)
		if info, err := os.Lstat(filepath.Join(spec.Dir, filepath.FromSlash(path))); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			leads = append(leads, path)
		}
	}
	return seen, leads, nulFields(skipped), true
}

// nulFields are the fields of git's NUL-separated output.
func nulFields(raw []byte) []string {
	return slices.DeleteFunc(strings.Split(string(raw), "\x00"), func(field string) bool { return field == "" })
}

// addSkillDirs adds each `.claude/skills` directory below the worktree's root that a slash-separated path lies in.
func addSkillDirs(found map[string]bool, path string) {
	for at := 0; ; {
		i := strings.Index(path[at:], skillsDir+"/")
		if i < 0 {
			return
		}
		i += at
		if i > 0 && path[i-1] == '/' {
			found[path[:i+len(skillsDir)]] = true
		}
		at = i + len(skillsDir)
	}
}

// nestedSkillDirs finds each `.claude/skills` directory below a lane's worktree's root, as the CLI would reach it: from
// git's listing of what it sees, and by the names of what it ignores, up to nestedWalkWait.
func nestedSkillDirs(spec ports.SessionSpec) nestedScan {
	scan := nestedScan{within: true}
	if spec.Dir == "" {
		return scan
	}
	found := map[string]bool{}
	roots := []string{""}
	if seen, leads, ignored, ok := gitListing(spec); ok {
		roots = leads
		for _, path := range seen {
			addSkillDirs(found, path)
		}
		for _, path := range ignored {
			dir, isDir := strings.CutSuffix(path, "/")
			addSkillDirs(found, dir+"/")
			if isDir {
				roots = append(roots, dir)
			}
		}
	}
	deadline := time.Now().Add(nestedWalkWait)
	visited := map[string]bool{}
	var walk func(dir, at string)
	walk = func(dir, at string) {
		if !scan.within {
			return
		}
		if !time.Now().Before(deadline) {
			scan.within = false
			return
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || visited[real] {
			return
		}
		visited[real] = true
		if pathpkg.Base(at) == ".claude" && at != ".claude" {
			if info, err := os.Stat(filepath.Join(dir, "skills")); err == nil && info.IsDir() {
				found[pathpkg.Join(at, "skills")] = true
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Name() == ".git" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if entry.Type()&fs.ModeSymlink != 0 {
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					continue
				}
			} else if !entry.IsDir() {
				continue
			}
			walk(path, pathpkg.Join(at, entry.Name()))
		}
	}
	for _, root := range roots {
		walk(filepath.Join(spec.Dir, filepath.FromSlash(root)), root)
	}
	scan.dirs = slices.Sorted(maps.Keys(found))
	return scan
}

// nestedDirs keeps the nested skills directories each worktree's start found, which a running session's check reads.
type nestedDirs struct {
	mu   sync.Mutex
	kept map[string]nestedScan
}

// of is the nested skills directories of a worktree: found again when rescan says so, as each start does, and otherwise
// those its last start found.
func (n *nestedDirs) of(spec ports.SessionSpec, rescan bool) nestedScan {
	n.mu.Lock()
	scan, found := n.kept[spec.Dir]
	n.mu.Unlock()
	if found && !rescan {
		return scan
	}
	scan = nestedSkillDirs(spec)
	n.mu.Lock()
	if n.kept == nil || len(n.kept) >= maxBaseSides {
		n.kept = map[string]nestedScan{}
	}
	n.kept[spec.Dir] = scan
	n.mu.Unlock()
	return scan
}

// nestedDirOf is the nested skills directory a piece's path lies in, or empty for a piece of the root's directories.
func nestedDirOf(path string) string {
	at := strings.Index(path, "/"+skillsDir+"/")
	if at < 0 {
		return ""
	}
	return path[:at+len("/"+skillsDir)]
}

// keyAllowedTools is the frontmatter key of a skill or a command that names the "Tools Claude can use without asking
// permission during the turn that invokes this skill" ([skills]), which widens what a session may do as a permission
// rule does.
//
// [skills]: https://code.claude.com/docs/en/skills#frontmatter-reference
const keyAllowedTools = "allowed-tools"

// keyPermissionMode is the frontmatter key of a subagent that sets "the permission mode a subagent runs in", which in a
// session in `default` mode it runs in ([subagents]), so a mode such as `acceptEdits` widens what it may do.
//
// [subagents]: https://code.claude.com/docs/en/sub-agents#permission-modes
const keyPermissionMode = "permissionMode"

// keyShellCommands names the shell commands a piece's body runs before Claude reads it, inline as !`command`, "only
// recognized when `!` appears at the start of a line or immediately after whitespace", or in "a fenced code block opened
// with ```!" ([inject dynamic context]). The page names the session's permission rules as their check and no PreToolUse
// hook, so a command the lane writes there may run with none of the checks ropeshift's own hook makes, and a body whose
// commands differ from the base's holds the lane as a hook does.
//
// [inject dynamic context]: https://code.claude.com/docs/en/skills#inject-dynamic-context
const keyShellCommands = "!commands"

// heldFrontmatter are the keys of a piece that widen what a session runs, sorted: the shell commands its body runs, its
// tools allowed with no prompt, its hooks, which run a command as a settings hook does, and a subagent's permission
// mode.
var heldFrontmatter = []string{keyShellCommands, keyAllowedTools, keyHooks, keyPermissionMode}

// inlineCommand is one inline shell command of a piece's body: !`command` at the start of a line or after whitespace.
var inlineCommand = regexp.MustCompile("(?:^|\\s)!`([^`]+)`")

// bodyCommands are the shell commands a piece's body runs, in order: each inline one, and each block opened with ```!,
// as the lines up to the fence that closes it.
func bodyCommands(body string) []string {
	var out []string
	lines := strings.Split(body, "\n")
	for at := 0; at < len(lines); at++ {
		if strings.HasPrefix(strings.TrimLeft(lines[at], " \t"), "```!") {
			var block []string
			for at++; at < len(lines) && !strings.HasPrefix(strings.TrimLeft(lines[at], " \t"), "```"); at++ {
				block = append(block, lines[at])
			}
			out = append(out, strings.Join(block, "\n"))
			continue
		}
		for _, found := range inlineCommand.FindAllStringSubmatch(lines[at], -1) {
			out = append(out, found[1])
		}
	}
	return out
}

// withBodyCommands is what a piece declares, with the shell commands its body runs under keyShellCommands.
func withBodyCommands(front pieceFront, body string) pieceFront {
	commands := bodyCommands(body)
	if len(commands) == 0 {
		return front
	}
	encoded, err := json.Marshal(commands)
	if err != nil {
		return pieceFront{}
	}
	if front.values == nil {
		front.values = map[string]json.RawMessage{}
	}
	front.values[keyShellCommands] = encoded
	return front
}

// pieceFront is what one piece's frontmatter declares of the keys that widen what a session runs (heldFrontmatter),
// each as JSON, and none for a key it leaves out or leaves empty. A frontmatter that does not parse or that opens and is
// not closed, and a file past the bound a repository's file holds to, is not readable, and nothing says it declares
// none.
type pieceFront struct {
	values   map[string]json.RawMessage
	readable bool
}

// frontmatterKeys reads the keys that widen what a session runs that a piece's frontmatter declares: the YAML between a
// first line of exactly `---` and the next, with an empty or absent key declaring none, and a file that does not begin
// with `---` declaring none. A file that begins with `---` and is no such block, whose first line has more on it than the
// three dashes or whose block no line closes, is not readable: the CLI may read as a block what this cannot, and nothing
// says it declares none.
func frontmatterKeys(raw []byte) pieceFront {
	text := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(text, "---") {
		return withBodyCommands(pieceFront{readable: true}, text)
	}
	first, rest, _ := strings.Cut(text, "\n")
	if first != "---" {
		return pieceFront{}
	}
	lines := strings.Split(rest, "\n")
	end := slices.IndexFunc(lines, func(line string) bool { return strings.TrimRight(line, " \t") == "---" })
	if end < 0 {
		return pieceFront{}
	}
	var front map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines[:end], "\n")), &front); err != nil {
		return pieceFront{}
	}
	out := pieceFront{readable: true}
	for _, key := range heldFrontmatter {
		value, declared := front[key]
		if key == keyShellCommands || !declared || value == nil {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return pieceFront{}
		}
		if empty := canonical(encoded); empty == "{}" || empty == "[]" || empty == `""` {
			continue
		}
		if out.values == nil {
			out.values = map[string]json.RawMessage{}
		}
		out.values[key] = encoded
	}
	return withBodyCommands(out, strings.Join(lines[end+1:], "\n"))
}

// pieceFile is one piece file the worktree holds: its slash-separated path there, and its content, whole unless it is
// past the bound a repository's file holds to.
type pieceFile struct {
	path  string
	raw   []byte
	whole bool
}

// worktreePieces are the piece files of a lane's worktree, in its root's piece directories and in the nested skills
// directories given, read as the CLI reads them, through links, each directory once, up to the bound the repository's
// side holds to. It says whether the read stayed within that bound.
func worktreePieces(spec ports.SessionSpec, nested []string) ([]pieceFile, bool) {
	var out []pieceFile
	seen := map[string]bool{}
	within := true
	var walk func(dir, at string, depth int)
	walk = func(dir, at string, depth int) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] || !within {
			return
		}
		seen[real] = true
		if depth > maxPieceDepth {
			within = false
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			path, name := filepath.Join(dir, entry.Name()), at+"/"+entry.Name()
			info, err := os.Stat(path)
			switch {
			case err != nil:
			case info.IsDir():
				walk(path, name, depth+1)
			case info.Mode().IsRegular() && isPiece(name):
				if len(out) == maxRepositoryPieceFiles {
					within = false
					return
				}
				raw, whole := boundedFile(path)
				out = append(out, pieceFile{path: name, raw: raw, whole: whole})
			}
		}
	}
	for _, dir := range slices.Concat(pieceDirs, nested) {
		walk(filepath.Join(spec.Dir, filepath.FromSlash(dir)), dir, 0)
	}
	return out, within
}

// boundedFile reads a file up to the bound a repository's file holds to, and says whether it read all of it.
func boundedFile(path string) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRepositoryFile+1))
	if err != nil || len(raw) > maxRepositoryFile {
		return raw, false
	}
	return raw, true
}

// heldPiece is one piece whose frontmatter hooks or allowed tools the lane added or changed: its path, the repository's
// blob of it at the base, empty where the base holds none, what each side declares, and the keys that differ. An
// overflow stands for the pieces past the bound, which were not compared.
type heldPiece struct {
	path       string
	blob       string
	base, lane pieceFront
	keys       []string
	raw        []byte
	overflow   bool
	// unread says the repository's nested skills directory of the piece could not be read, so nothing says the base
	// holds none of it.
	unread bool
}

// comparePieces are the pieces of a lane's worktree (worktreePieces) whose frontmatter hooks or allowed tools differ from
// the repository's side, where the worktree's declare any: a hook or a tool the lane added or changed, and a frontmatter
// that cannot be read. A piece whose hooks or tools the lane removed runs less, and holds nothing. A piece the worktree
// holds as the base does is not read again.
func comparePieces(spec ports.SessionSpec, base *baseSide, files []pieceFile, within bool) []heldPiece {
	var out []heldPiece
	for _, file := range files {
		blob, known := base.blobOf(spec, file.path)
		if id, ok := gitcli.BlobID(spec.Base, file.raw); ok && file.whole && blob != "" && id == blob {
			continue
		}
		lane := pieceFront{}
		if file.whole {
			lane = frontmatterKeys(file.raw)
		}
		if lane.readable && len(lane.values) == 0 {
			continue
		}
		var at pieceFront
		if blob != "" {
			at = base.frontOf(spec, blob)
		}
		keys := widened(lane, at)
		if lane.readable && len(keys) == 0 {
			continue
		}
		out = append(out, heldPiece{path: file.path, blob: blob, base: at, lane: lane, keys: keys, raw: file.raw, unread: !known})
	}
	slices.SortFunc(out, func(a, b heldPiece) int { return strings.Compare(a.path, b.path) })
	if !within {
		out = append(out, heldPiece{path: pieceDirs[0], overflow: true})
	}
	return out
}

// widened are the keys a piece's frontmatter in the worktree declares otherwise than the repository's, sorted: each the
// lane added or changed, and each it declares when the repository's cannot be read.
func widened(lane, base pieceFront) []string {
	var out []string
	for _, key := range heldFrontmatter {
		value := lane.values[key]
		if value != nil && (!base.readable || canonical(value) != canonical(base.values[key])) {
			out = append(out, key)
		}
	}
	return out
}

// heldKeys are the keys a hold names for one piece: those that differ, or its hooks for a piece that could not be read
// or compared.
func (p heldPiece) heldKeys() []string {
	if len(p.keys) == 0 {
		return []string{keyHooks}
	}
	return p.keys
}

// nestedOverflow is the held piece that stands for the nested skills a walk past nestedWalkWait did not compare.
func nestedOverflow(scan nestedScan) []heldPiece {
	if scan.within {
		return nil
	}
	return []heldPiece{{path: nestedRoot, overflow: true}}
}

// summary is what the hold says of one piece, as describeChange says it of a settings key.
func (p heldPiece) summary() []string {
	switch {
	case p.overflow && p.path == nestedRoot:
		return []string{fmt.Sprintf("the worktree: the directories git ignores could not all be read within %s, so the skills nested in them were not compared: the size of the tree holds the lane, and no change the lane made", nestedWalkWait)}
	case p.overflow:
		return []string{fmt.Sprintf(".claude: more than %d skill, subagent, and command files, so the hooks of those past the bound were not compared", maxRepositoryPieceFiles)}
	case !p.lane.readable:
		return []string{p.path + " hooks: frontmatter that cannot be read, held as a change"}
	}
	var out []string
	for _, key := range p.keys {
		out = append(out, describeChange(p.path, key, p.base.values[key], p.lane.values[key])...)
	}
	return out
}

// bound is what a hold's digest binds of one piece: what it declares of each key that differs, or the bytes of one that
// cannot be read.
func (p heldPiece) bound() string {
	switch {
	case p.overflow:
		return "overflow\x00" + p.path
	case !p.lane.readable:
		sum := sha256.Sum256(p.raw)
		return "unreadable\x00" + hex.EncodeToString(sum[:])
	}
	parts := make([]string, 0, len(p.keys))
	for _, key := range p.keys {
		parts = append(parts, key+"\x00"+canonical(p.lane.values[key]))
	}
	return strings.Join(parts, "\x00")
}

// restorePieces puts each piece whose frontmatter hooks or allowed tools the lane added or changed back as the commit the lane's work
// began from holds it, or removes it where that commit holds none, and leaves every other piece as the lane wrote it.
// It acts only on a complete read of the repository's side, so no piece is removed that the base holds, and it writes
// only through directories of the worktree's own: a link on the way is refused.
func restorePieces(spec ports.SessionSpec) error {
	base, complete := readBaseSide(spec)
	if !complete {
		return fmt.Errorf("the repository's skills, subagents, and commands at %s could not be read, so none is restored", spec.Base)
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	files, within := worktreePieces(spec, nestedSkillDirs(spec).dirs)
	for _, piece := range comparePieces(spec, base, files, within) {
		if piece.overflow {
			continue
		}
		if piece.unread {
			return fmt.Errorf("the repository's skills in %s at %s could not be read, so none is restored", nestedDirOf(piece.path), spec.Base)
		}
		path := filepath.Join(spec.Dir, filepath.FromSlash(piece.path))
		if piece.blob == "" {
			present, err := ownParents(spec.Dir, piece.path, false)
			if err != nil {
				return err
			}
			if present {
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			continue
		}
		raw, err := repositoryGit(spec).BlobOf(ctx, piece.blob, maxRepositoryFile)
		if err != nil {
			return err
		}
		if _, err := ownParents(spec.Dir, piece.path, true); err != nil {
			return err
		}
		if current, whole := boundedFile(path); whole && bytes.Equal(current, raw) {
			continue
		}
		if err := replaceFile(path, raw); err != nil {
			return err
		}
	}
	return nil
}

// ownParents says whether every directory between a worktree and one of its files is a directory of its own, and makes
// each that is missing when create says so. A link or a file on the way is refused, so nothing is written or removed
// outside the worktree.
func ownParents(worktree, path string, create bool) (bool, error) {
	at := worktree
	for _, name := range strings.Split(pathpkg.Dir(path), "/") {
		at = filepath.Join(at, name)
		info, err := os.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if !create {
				return false, nil
			}
			if err := os.Mkdir(at, 0o755); err != nil {
				return false, err
			}
		case err != nil:
			return false, err
		case !info.IsDir():
			return false, errSettingsNotRestorable
		}
	}
	return true, nil
}
