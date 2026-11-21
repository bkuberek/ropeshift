package claudecode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// A hook the repository holds runs as the repository wrote it, and a file it runs is one the lane can edit: a hook whose
// command is `./scripts/check.sh` runs whatever the lane wrote there. So the hold reads each hook a session would run
// from its worktree, in its settings files and in the frontmatter of its pieces, and the status line and the file
// suggestion command of its settings files, which run a command as a hook does, takes each word of its command that
// names a file inside the worktree, and holds the lane when that file differs from the repository's at the lane's base
// (atSettingsBase), as a change to the hook itself would. A command hook runs "in the current directory", the session's
// working directory, which for a lane is its worktree, and `${CLAUDE_PROJECT_DIR}` is "the project root where the
// session started" ([hooks]), so a word names a file when it is a path to a regular file of the worktree, given relative
// to it or under that variable: `./x`, `scripts/x`, and an interpreter's argument, such as `bash scripts/x` or
// `python x.py`. A file a hook reaches only through another, such as one a script sources or a Makefile target reads, is
// no word of the command, and the hold does not read it.
//
// [hooks]: https://code.claude.com/docs/en/hooks
const projectDirVariable = "CLAUDE_PROJECT_DIR"

// The settings keys besides `hooks` whose value runs a command as a hook does: the status line, which a pane runs after
// ropeshift's own, and the file suggestion command ([settings reference]).
//
// [settings reference]: https://code.claude.com/docs/en/settings-reference
const (
	keyStatusLine     = "statusLine"
	keyFileSuggestion = "fileSuggestion"
)

// commandKeys say what each key that runs a command is called in a hold's summary.
var commandKeys = map[string]string{keyHooks: "a hook", keyStatusLine: "the status line", keyFileSuggestion: "the file suggestion command"}

// ranCommand is the words of the commands one key of one file a session reads runs, by that file's slash-separated
// path in the worktree.
type ranCommand struct {
	from, key string
	words     []string
}

// sessionCommands are the commands a lane's session would run from its worktree: each settings file's hooks, status
// line, and file suggestion command, and each piece's hooks.
func sessionCommands(spec ports.SessionSpec, pieces []pieceFile) []ranCommand {
	var out []ranCommand
	for _, file := range settingsFiles(spec) {
		keys := settingsKeys(worktreeFile(spec, file))
		if value := keys[keyHooks]; value != nil {
			out = append(out, ranCommand{from: file, key: keyHooks, words: hookWords(value)})
		}
		for _, key := range []string{keyStatusLine, keyFileSuggestion} {
			if value := keys[key]; value != nil {
				out = append(out, ranCommand{from: file, key: key, words: commandWords(value)})
			}
		}
	}
	for _, piece := range pieces {
		if !piece.whole || !bytes.Contains(piece.raw, []byte(keyHooks)) {
			continue
		}
		if value := frontmatterKeys(piece.raw).values[keyHooks]; value != nil {
			out = append(out, ranCommand{from: piece.path, key: keyHooks, words: hookWords(value)})
		}
	}
	return out
}

// commandWords are the words of the command one setting runs, such as a status line: its `command`, split as a shell
// splits it, and the words of its `args`.
func commandWords(value json.RawMessage) []string {
	var setting struct {
		Command string          `json:"command"`
		Args    json.RawMessage `json:"args"`
	}
	if json.Unmarshal(value, &setting) != nil {
		return nil
	}
	out := shellWords(setting.Command)
	var args []string
	if json.Unmarshal(setting.Args, &args) == nil {
		out = append(out, args...)
	}
	return out
}

// hookWords are the words of every command one `hooks` value runs: each hook's `command`, split as a shell splits it,
// and the words of its `args`.
func hookWords(value json.RawMessage) []string {
	var events map[string][]struct {
		Hooks []struct {
			Command string          `json:"command"`
			Args    json.RawMessage `json:"args"`
		} `json:"hooks"`
	}
	if json.Unmarshal(value, &events) != nil {
		return nil
	}
	var out []string
	for _, groups := range events {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				out = append(out, shellWords(hook.Command)...)
				var args []string
				if json.Unmarshal(hook.Args, &args) == nil {
					out = append(out, args...)
				}
			}
		}
	}
	return out
}

// wordEnds are the characters outside quotes that end a shell word: a blank, a separator, a pipe, a redirection, a
// subshell, and a command substitution.
const wordEnds = " \t\r\n;&|<>()`"

// shellWords splits a command into the words a shell would, with their quotes and escapes taken off, and expands nothing.
func shellWords(command string) []string {
	var words []string
	var word strings.Builder
	open, escaped := false, false
	quote := rune(0)
	for _, r := range command {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case quote == '"' && r == '"':
			quote = 0
		case quote == '"' && r == '\\':
			escaped = true
		case quote == '"':
			word.WriteRune(r)
		case r == '\\':
			escaped, open = true, true
		case r == '\'' || r == '"':
			quote, open = r, true
		case strings.ContainsRune(wordEnds, r):
			if open {
				words = append(words, word.String())
				word.Reset()
			}
			open = false
		default:
			word.WriteRune(r)
			open = true
		}
	}
	if open {
		words = append(words, word.String())
	}
	return words
}

// wordPaths are the slash-separated paths inside a lane's worktree that one word of a hook's command names: the word,
// and the value of a word such as `--file=x`, given relative to the worktree or under `$CLAUDE_PROJECT_DIR`. A path
// outside the worktree, one that starts at a home directory, and one that holds any other variable name none.
func wordPaths(spec ports.SessionSpec, word string) []string {
	candidates := []string{word}
	if _, value, ok := strings.Cut(word, "="); ok {
		candidates = append(candidates, value)
	}
	var out []string
	for _, candidate := range candidates {
		rest := candidate
		for _, name := range []string{"${" + projectDirVariable + "}", "$" + projectDirVariable} {
			rest = strings.ReplaceAll(rest, name, "")
			candidate = strings.ReplaceAll(candidate, name, spec.Dir)
		}
		if candidate == "" || strings.HasPrefix(candidate, "~") || strings.Contains(rest, "$") {
			continue
		}
		path := candidate
		if !filepath.IsAbs(path) {
			path = filepath.Join(spec.Dir, path)
		}
		rel, err := filepath.Rel(spec.Dir, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

// heldHookFile is one file of the worktree that a hook or another command runs and that differs from the repository's
// at the lane's base: its path, the file and the key whose command runs it, its content, whole unless it is past the
// bound, and whether the base holds one.
type heldHookFile struct {
	path, from string
	key        string
	raw        []byte
	whole      bool
	based      bool
	// unread says the repository's copy could not be read, so nothing says the file matches it.
	unread bool
}

// hookFiles are the files of a lane's worktree that a word of a hook's or another setting's command names, which differ
// from the repository's at the lane's base (baseSide.fileAt), by path. A path that names no regular file of the
// worktree, as the CLI would read it through its links, runs nothing of the lane's.
func hookFiles(spec ports.SessionSpec, base *baseSide, commands []ranCommand) []heldHookFile {
	var out []heldHookFile
	seen := map[string]bool{}
	for _, ran := range commands {
		for _, word := range ran.words {
			for _, path := range wordPaths(spec, word) {
				if seen[path] {
					continue
				}
				seen[path] = true
				full := filepath.Join(spec.Dir, filepath.FromSlash(path))
				if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
					continue
				}
				raw, whole := boundedFile(full)
				at, err := base.fileAt(spec, path)
				if err == nil && whole && at.found && bytes.Equal(raw, at.raw) {
					continue
				}
				out = append(out, heldHookFile{path: path, from: ran.from, key: ran.key, raw: raw, whole: whole, based: at.found, unread: err != nil})
			}
		}
	}
	slices.SortFunc(out, func(a, b heldHookFile) int { return strings.Compare(a.path, b.path) })
	return out
}

// summary is what the hold says of one file a hook or another command runs.
func (f heldHookFile) summary() string {
	how := "changed"
	switch {
	case f.unread:
		how = "the repository's copy could not be read, held as a change"
	case !f.based:
		how = "added"
	}
	return f.path + ": " + how + ", and " + commandKeys[f.key] + " in " + f.from + " runs it"
}

// bound is what a hold's digest binds of one file a hook runs: its content, and whether it was read whole.
func (f heldHookFile) bound() string {
	sum := sha256.Sum256(f.raw)
	if !f.whole {
		return "partial\x00" + hex.EncodeToString(sum[:])
	}
	return hex.EncodeToString(sum[:])
}

// baseFile is one file of the repository at the lane's base, as fileAt read it.
type baseFile struct {
	raw   []byte
	found bool
}

// fileAt is a file the repository holds at the lane's base, by its slash-separated path, read from git's objects as
// repositoryFile reads it, once per side: whether the commit holds one, and what. A read that fails, such as one of a
// path that is a link there, is an error and is read again next time.
func (b *baseSide) fileAt(spec ports.SessionSpec, path string) (baseFile, error) {
	b.mu.Lock()
	file, read := b.ran[path]
	b.mu.Unlock()
	if read || spec.SettingsUnbased {
		return file, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	raw, found, err := repositoryGit(spec).FileAt(ctx, spec.Base, path, maxRepositoryFile)
	if err != nil {
		return baseFile{}, err
	}
	file = baseFile{raw: raw, found: found}
	b.mu.Lock()
	b.ran[path] = file
	b.mu.Unlock()
	return file, nil
}
