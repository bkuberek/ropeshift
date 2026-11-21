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

// keyHooks is the key of a settings file and of a piece's frontmatter that holds its hooks.
const keyHooks = "hooks"

// maxPieceDepth bounds how far below a piece directory the worktree is read, links included.
const maxPieceDepth = 16

// isPiece says a file, by its slash-separated path in the worktree, is one whose frontmatter the CLI reads: a skill's
// SKILL.md, or any Markdown file of the subagents or commands.
func isPiece(path string) bool {
	name := pathpkg.Base(path)
	if strings.HasPrefix(path, skillsDir+"/") {
		return strings.EqualFold(name, "SKILL.md")
	}
	return strings.EqualFold(pathpkg.Ext(name), ".md")
}

// pieceHooks are the hooks one piece's frontmatter declares, as JSON, nil for none. A frontmatter that does not parse or
// that opens and is not closed, and a file past the bound a repository's file holds to, is not readable, and nothing
// says it declares none.
type pieceHooks struct {
	value    json.RawMessage
	readable bool
}

// frontmatterHooks reads the hooks a piece's frontmatter declares: the YAML between a first line of exactly `---` and the
// next, with an empty or absent `hooks` declaring none, and a file that does not begin with `---` declaring none. A file
// that begins with `---` and is no such block, whose first line has more on it than the three dashes or whose block no
// line closes, is not readable: the CLI may read as a block what this cannot, and nothing says it declares none.
func frontmatterHooks(raw []byte) pieceHooks {
	text := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(text, "---") {
		return pieceHooks{readable: true}
	}
	first, rest, _ := strings.Cut(text, "\n")
	if first != "---" {
		return pieceHooks{}
	}
	lines := strings.Split(rest, "\n")
	end := slices.IndexFunc(lines, func(line string) bool { return strings.TrimRight(line, " \t") == "---" })
	if end < 0 {
		return pieceHooks{}
	}
	var front map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines[:end], "\n")), &front); err != nil {
		return pieceHooks{}
	}
	value, declared := front[keyHooks]
	if !declared || value == nil {
		return pieceHooks{readable: true}
	}
	out, err := json.Marshal(value)
	if err != nil {
		return pieceHooks{}
	}
	if empty := canonical(out); empty == "{}" || empty == "[]" {
		return pieceHooks{readable: true}
	}
	return pieceHooks{value: out, readable: true}
}

// pieceFile is one piece file the worktree holds: its slash-separated path there, and its content, whole unless it is
// past the bound a repository's file holds to.
type pieceFile struct {
	path  string
	raw   []byte
	whole bool
}

// worktreePieces are the piece files of a lane's worktree, read as the CLI reads them, through links, each directory
// once, up to the bound the repository's side holds to. It says whether the read stayed within that bound.
func worktreePieces(spec ports.SessionSpec) ([]pieceFile, bool) {
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
	for _, dir := range pieceDirs {
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

// heldPiece is one piece whose frontmatter hooks the lane added or changed: its path, the repository's blob of it at the
// base, empty where the base holds none, and the hooks on each side. An overflow stands for the pieces past the bound,
// which were not compared.
type heldPiece struct {
	path       string
	blob       string
	base, lane pieceHooks
	raw        []byte
	overflow   bool
}

// comparePieces are the pieces of a lane's worktree (worktreePieces) whose frontmatter hooks differ from the repository's
// side, where the worktree's declare any: a hook the lane added or changed, and a frontmatter that cannot be read. A piece
// whose hooks the lane removed runs less, and holds nothing. A piece the worktree holds as the base does is not read again.
func comparePieces(spec ports.SessionSpec, base *baseSide, files []pieceFile, within bool) []heldPiece {
	var out []heldPiece
	for _, file := range files {
		blob := base.pieces[file.path]
		if id, ok := gitcli.BlobID(spec.Base, file.raw); ok && file.whole && blob != "" && id == blob {
			continue
		}
		lane := pieceHooks{}
		if file.whole {
			lane = frontmatterHooks(file.raw)
		}
		if lane.readable && lane.value == nil {
			continue
		}
		var at pieceHooks
		if blob != "" {
			at = base.hooksOf(spec, blob)
		}
		if lane.readable && at.readable && canonical(lane.value) == canonical(at.value) {
			continue
		}
		out = append(out, heldPiece{path: file.path, blob: blob, base: at, lane: lane, raw: file.raw})
	}
	slices.SortFunc(out, func(a, b heldPiece) int { return strings.Compare(a.path, b.path) })
	if !within {
		out = append(out, heldPiece{path: pieceDirs[0], overflow: true})
	}
	return out
}

// summary is what the hold says of one piece, as describeChange says it of a settings key.
func (p heldPiece) summary() []string {
	switch {
	case p.overflow:
		return []string{fmt.Sprintf(".claude: more than %d skill, subagent, and command files, so the hooks of those past the bound were not compared", maxRepositoryPieceFiles)}
	case !p.lane.readable:
		return []string{p.path + " hooks: frontmatter that cannot be read, held as a change"}
	}
	return describeChange(p.path, keyHooks, p.base.value, p.lane.value)
}

// bound is what a hold's digest binds of one piece: the hooks it declares, or the bytes of one that cannot be read.
func (p heldPiece) bound() string {
	switch {
	case p.overflow:
		return "overflow"
	case !p.lane.readable:
		sum := sha256.Sum256(p.raw)
		return "unreadable\x00" + hex.EncodeToString(sum[:])
	}
	return keyHooks + "\x00" + canonical(p.lane.value)
}

// restorePieces puts each piece whose frontmatter hooks the lane added or changed back as the commit the lane's work
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
	files, within := worktreePieces(spec)
	for _, piece := range comparePieces(spec, base, files, within) {
		if piece.overflow {
			continue
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
