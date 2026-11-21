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
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

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
// into one runs as a root skill's does, and the hold reads them as it reads the root's. Finding them reads every
// directory of the worktree but `.git`, through links, each once, up to maxNestedDirs; past it, the skills below were not
// compared, and the hold says so. A running session's check reads the nested directories its start found (nestedDirs),
// because a reload has milliseconds: a nested skill a lane adds during a session in a directory the start did not find
// is held at the next generation's start.
//
// The walk reads every directory, those git ignores, such as `node_modules`, among them: the skills page says nested
// skills "load the first time Claude reads or edits a file in that subdirectory" and names no directory discovery leaves
// out ([skills]). So a tree past the bound holds the lane for its size, and the hold says that no change of the lane's
// is what holds it.
//
// [skills]: https://code.claude.com/docs/en/skills#load-skills-in-monorepos-and-subdirectories
var maxNestedDirs = 1 << 16

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
// path, and whether the walk stayed within maxNestedDirs.
type nestedScan struct {
	dirs   []string
	within bool
}

// nestedSkillDirs finds each `.claude/skills` directory below a lane's worktree's root, as the CLI would reach it,
// through links, reading each directory once and none named `.git`.
func nestedSkillDirs(spec ports.SessionSpec) nestedScan {
	scan := nestedScan{within: true}
	seen := map[string]bool{}
	read := 0
	var walk func(dir, at string)
	walk = func(dir, at string) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] || !scan.within {
			return
		}
		seen[real] = true
		if read++; read > maxNestedDirs {
			scan.within = false
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Name() == ".git" {
				continue
			}
			path, name := filepath.Join(dir, entry.Name()), pathpkg.Join(at, entry.Name())
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				continue
			}
			if entry.Name() == ".claude" && at != "" {
				if info, err := os.Stat(filepath.Join(path, "skills")); err == nil && info.IsDir() {
					scan.dirs = append(scan.dirs, pathpkg.Join(at, skillsDir))
				}
			}
			walk(path, name)
		}
	}
	walk(spec.Dir, "")
	slices.Sort(scan.dirs)
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

// heldFrontmatter are the frontmatter keys of a piece that widen what a session runs, sorted: its tools allowed with no
// prompt, its hooks, which run a command as a settings hook does, and a subagent's permission mode.
var heldFrontmatter = []string{keyAllowedTools, keyHooks, keyPermissionMode}

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
		return pieceFront{readable: true}
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
		if !declared || value == nil {
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
	return out
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

// nestedOverflow is the held piece that stands for the nested skills a walk past maxNestedDirs did not compare.
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
		return []string{fmt.Sprintf("the worktree: more than %d directories, so the skills nested below its root were not compared: the size of the tree holds the lane, and no change the lane made", maxNestedDirs)}
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
