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
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// The project settings a lane's session reads come from its worktree, which the lane can edit, and no flag points the
// CLI's read at the commit the lane began from. The CLI reads the shared `.claude/settings.json` "from the session's
// primary working directory", which is the lane's worktree, for the `project` source, and for the `local` source
// `.claude/settings.local.json` at the repository's root, which in a worktree "uses the file at the main checkout's
// root", together with a file "an earlier version left" in the starting directory ([settings]). A lane names `project`
// alone (laneSources), so its one settings file in the worktree is `.claude/settings.json`; a session whose sources
// include `local` reads the worktree's `.claude/settings.local.json` too. The local file at the main checkout's root is
// the member's own, outside the worktree, which the lane never loads.
//
// The CLI also "watches your settings files and reloads them when they change", hooks and permissions included
// ([settings]), so this check holds a generation before its session starts, and the session's `ConfigChange` hook asks
// the worker for the same check before the CLI applies an edit within the session (reloadMatcher, Driver.SettingsChange).
//
// [settings]: https://code.claude.com/docs/en/settings#where-claude-code-keeps-the-local-file-in-a-git-repository
const (
	projectSettingsFile = ".claude/settings.json"
	localSettingsFile   = ".claude/settings.local.json"
)

// sourceLocal is the setting source of the member's own settings for a project, which a lane never names.
const sourceLocal = "local"

// safeSettings are the settings keys known to be harmless: a change to one holds nothing, because none can run code or
// widen what a session may do. Each sets text, a display, or the name of a model, or names the schema an editor checks
// the file against ([settings reference]). A change to any other key holds the lane: a key that runs a command, such as
// `hooks`, `statusLine`, `fileSuggestion`, `apiKeyHelper`, `otelHeadersHelper`, `awsAuthRefresh`, `awsCredentialExport`,
// and `gcpAuthRefresh`; a key that widens access, such as `permissions`, `env`, `enabledPlugins`, and the MCP server
// keys; and a key no list here knows, which a later version of the CLI may give a meaning. A list of the keys that run
// code would stay one release behind the CLI, so this is the list of the harmless ones, and it stays short.
//
// [settings reference]: https://code.claude.com/docs/en/settings-reference
var safeSettings = []string{"$schema", "attribution", "includeCoAuthoredBy", "language", "model", "outputStyle", "theme"}

// secretValues are the keys whose values the summary never shows, because each value can be a credential: an
// environment variable's value.
var secretValues = []string{"env"}

// settingsFiles are the files of a lane's worktree its session reads as project settings, by its setting sources.
func settingsFiles(spec ports.SessionSpec) []string {
	var files []string
	sources := sessionSources(spec)
	if slices.Contains(sources, sourceProject) {
		files = append(files, projectSettingsFile)
	}
	if slices.Contains(sources, sourceLocal) {
		files = append(files, localSettingsFile)
	}
	return files
}

// sessionSources are the setting sources a session's command names: the lane's, or the list a real-session suite's
// flags name in their place (isolationArgs).
func sessionSources(spec ports.SessionSpec) []string {
	args := spec.Override.Args()
	if at := slices.Index(args, flagSettingSources); at >= 0 && at+1 < len(args) {
		return strings.Split(args[at+1], ",")
	}
	return laneSources
}

// atSettingsBase is a spec that reads the repository's files at the commit the lane's work began from, its claim's
// base, and not at the tip of a branch it continues, which a lane wrote: a lane that committed its own settings edit
// would otherwise compare that edit with itself (ADR-0040). A spec with no such commit (SettingsUnbased) reads none: it
// is no commit at all, never the one the worktree began at, which holds what the lane under review wrote.
func atSettingsBase(spec ports.SessionSpec) ports.SessionSpec {
	switch {
	case spec.SettingsUnbased:
		spec.Base = ""
	case spec.SettingsBase != "":
		spec.Base = spec.SettingsBase
	}
	return spec
}

// settingsChange compares the project settings a lane's session would read from its worktree with the repository's at
// the commit the lane's work began from (atSettingsBase), read from git's objects as repositoryFile reads them, and
// names each key not known to be harmless (safeSettings) that differs. A file absent from either side holds no keys, and so does a file that does not
// parse, which the CLI skips ([settings]). A lane with no base, an orchestrator's or an assistant's among them, has
// nothing to compare, and a review with no commit to compare with (SettingsUnbased) compares with an empty repository:
// every setting its worktree holds that runs code holds it.
//
// [settings]: https://code.claude.com/docs/en/settings#fix-a-broken-settings-file
func settingsChange(spec ports.SessionSpec) *lanes.SettingsChange {
	spec = atSettingsBase(spec)
	if !comparable(spec) {
		return nil
	}
	base, _ := readBaseSide(spec)
	return compareSettings(spec, base, nestedSkillDirs(spec))
}

// comparable says a spec names a worktree and the clone and commit its repository's side is read at, or says its
// repository's side is empty (SettingsUnbased).
func comparable(spec ports.SessionSpec) bool {
	if spec.Dir == "" {
		return false
	}
	return spec.SettingsUnbased || (spec.Repository != "" && spec.Base != "")
}

// SettingsChange is what a lane's worktree changed now of the settings its session reads, as its preflight compares
// them, which a running session's `ConfigChange` hook asks for before the CLI applies an edit (reloadMatcher). The hook
// has a few milliseconds, so the repository's side, which is fixed by the commit it is read at, is read once per clone
// and commit and kept, and each later check reads only the worktree.
func (d *Driver) SettingsChange(_ context.Context, spec ports.SessionSpec) *lanes.SettingsChange {
	return d.settingsChangeOf(spec, false)
}

// settingsChangeOf is SettingsChange, which finds the worktree's nested skills directories again when rescan says so, as
// a start's preflight does, and otherwise reads those its start found (nestedDirs).
func (d *Driver) settingsChangeOf(spec ports.SessionSpec, rescan bool) *lanes.SettingsChange {
	spec = atSettingsBase(spec)
	if !comparable(spec) {
		return nil
	}
	return compareSettings(spec, d.bases.of(spec), d.nested.of(spec, rescan))
}

// baseSide is the repository's side of what a hold compares, at the commit a lane's work began from: each settings file
// the session reads, as that commit holds it, and nil where it holds none, and the blob of each piece file it holds
// (pieceDirs), whose hooks are read only for a piece the worktree holds otherwise. The pieces of a nested skills
// directory are read once a worktree holds that directory (blobOf).
type baseSide struct {
	files  map[string][]byte
	pieces map[string]string
	// nested holds the blob of each piece of each nested skills directory read, by the directory, under mu.
	nested map[string]map[string]string
	// mu guards hooks, the frontmatter hooks read of each piece blob, and ran, each file a hook runs (fileAt).
	mu    sync.Mutex
	hooks map[string]pieceFront
	ran   map[string]baseFile
}

// newBaseSide is a repository's side that holds nothing yet.
func newBaseSide() *baseSide {
	return &baseSide{
		files: map[string][]byte{}, pieces: map[string]string{}, nested: map[string]map[string]string{},
		hooks: map[string]pieceFront{}, ran: map[string]baseFile{},
	}
}

// blobOf is the blob of a piece the repository holds at the lane's base, by its path, empty for none, and whether that
// was read: a piece of a nested skills directory is read with the rest of its directory once, and a read that fails says
// false and is read again next time.
func (b *baseSide) blobOf(spec ports.SessionSpec, path string) (string, bool) {
	dir := nestedDirOf(path)
	if dir == "" {
		return b.pieces[path], true
	}
	b.mu.Lock()
	pieces, read := b.nested[dir]
	b.mu.Unlock()
	if read || spec.SettingsUnbased {
		return pieces[path], true
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	files, err := repositoryGit(spec).FilesUnder(ctx, spec.Base, dir, maxRepositoryPieceFiles)
	if err != nil {
		return "", false
	}
	pieces = map[string]string{}
	for _, file := range files {
		if isPiece(dir + "/" + file.Path) {
			pieces[dir+"/"+file.Path] = file.Blob
		}
	}
	b.mu.Lock()
	b.nested[dir] = pieces
	b.mu.Unlock()
	return pieces[path], true
}

// readBaseSide reads the repository's side of a hold from git's objects, and says whether every read completed. A read
// that failed reads as no file, as repositoryFile reads it, and leaves the side incomplete, so it is not kept.
func readBaseSide(spec ports.SessionSpec) (*baseSide, bool) {
	side := newBaseSide()
	if spec.SettingsUnbased {
		return side, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	git := repositoryGit(spec)
	complete := true
	for _, file := range settingsFiles(spec) {
		raw, found, err := git.FileAt(ctx, spec.Base, file, maxRepositoryFile)
		switch {
		case err != nil:
			complete = false
		case found:
			side.files[file] = raw
		}
	}
	for _, dir := range pieceDirs {
		files, err := git.FilesUnder(ctx, spec.Base, dir, maxRepositoryPieceFiles)
		if err != nil {
			complete = false
		}
		for _, file := range files {
			if isPiece(dir + "/" + file.Path) {
				side.pieces[dir+"/"+file.Path] = file.Blob
			}
		}
	}
	return side, complete
}

// frontOf is what the frontmatter of a piece blob the repository holds declares of the keys that widen what a session
// runs, read once. A blob that cannot be read declares none, so a hook or a tool the worktree's piece declares reads as
// added, and it is read again next time.
func (b *baseSide) frontOf(spec ports.SessionSpec, blob string) pieceFront {
	b.mu.Lock()
	hooks, found := b.hooks[blob]
	b.mu.Unlock()
	if found {
		return hooks
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	raw, err := repositoryGit(spec).BlobOf(ctx, blob, maxRepositoryFile)
	if err != nil {
		return pieceFront{readable: true}
	}
	hooks = frontmatterKeys(raw)
	b.mu.Lock()
	b.hooks[blob] = hooks
	b.mu.Unlock()
	return hooks
}

// maxBaseSides bounds the sides a driver keeps. Past it, the driver forgets them all and reads each again once.
const maxBaseSides = 256

// baseSides keeps the repository's side of each hold a driver has read completely, by the clone, the commit, and the
// files compared. A commit's objects are fixed by its hash, which every read checks, so a kept side never goes stale.
type baseSides struct {
	mu   sync.Mutex
	kept map[string]*baseSide
}

// of is the repository's side of a spec's hold, read once and kept.
func (b *baseSides) of(spec ports.SessionSpec) *baseSide {
	key := strings.Join(append([]string{spec.Repository, spec.Base}, settingsFiles(spec)...), "\x00")
	b.mu.Lock()
	side, found := b.kept[key]
	b.mu.Unlock()
	if found {
		return side
	}
	side, complete := readBaseSide(spec)
	if complete {
		b.mu.Lock()
		if b.kept == nil || len(b.kept) >= maxBaseSides {
			b.kept = map[string]*baseSide{}
		}
		b.kept[key] = side
		b.mu.Unlock()
	}
	return side
}

// compareSettings names each key not known to be harmless (safeSettings) that the worktree's settings files hold
// otherwise than the repository's side, each piece whose frontmatter hooks the lane added or changed, which runs a
// command as a settings hook does, or whose allowed tools it added or changed, which widen what it may do as a
// permission rule does, and each file a hook, the status line, or the file suggestion command runs that the lane changed
// (hookFiles).
func compareSettings(spec ports.SessionSpec, base *baseSide, nested nestedScan) *lanes.SettingsChange {
	change := &lanes.SettingsChange{}
	digest := sha256.New()
	for _, file := range settingsFiles(spec) {
		lane := settingsKeys(worktreeFile(spec, file))
		at := settingsKeys(base.files[file])
		keys := heldKeys(lane, at)
		changed := false
		for _, key := range keys {
			if canonical(lane[key]) == canonical(at[key]) {
				continue
			}
			changed = true
			if !slices.Contains(change.Keys, key) {
				change.Keys = append(change.Keys, key)
			}
			change.Summary = append(change.Summary, describeChange(file, key, at[key], lane[key])...)
		}
		if changed {
			change.Files = append(change.Files, file)
		}
		for _, key := range keys {
			fmt.Fprintf(digest, "%s\x00%s\x00%s\x00", file, key, canonical(lane[key]))
		}
	}
	pieces, within := worktreePieces(spec, nested.dirs)
	for _, piece := range slices.Concat(comparePieces(spec, base, pieces, within), nestedOverflow(nested)) {
		if !piece.overflow {
			change.Files = append(change.Files, piece.path)
		}
		for _, key := range piece.heldKeys() {
			if !slices.Contains(change.Keys, key) {
				change.Keys = append(change.Keys, key)
			}
		}
		change.Summary = append(change.Summary, piece.summary()...)
		fmt.Fprintf(digest, "%s\x00%s\x00", piece.path, piece.bound())
	}
	for _, held := range hookFiles(spec, base, sessionCommands(spec, pieces)) {
		if !slices.Contains(change.Files, held.path) {
			change.Files = append(change.Files, held.path)
		}
		if !slices.Contains(change.Keys, held.key) {
			change.Keys = append(change.Keys, held.key)
		}
		change.Summary = append(change.Summary, held.summary())
		fmt.Fprintf(digest, "%s\x00ran\x00%s\x00", held.path, held.bound())
	}
	if len(change.Keys) == 0 {
		return nil
	}
	slices.Sort(change.Keys)
	if len(change.Summary) > lanes.MaxSettingsLines {
		change.Summary = append(change.Summary[:lanes.MaxSettingsLines-1], fmt.Sprintf("and %d more changes", len(change.Summary)-lanes.MaxSettingsLines+1))
	}
	change.Digest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	return change
}

// heldKeys are the top-level keys of a lane's settings file and of the repository's, sorted, that are not known to be
// harmless (safeSettings): the keys a change to which holds the lane.
func heldKeys(lane, base map[string]json.RawMessage) []string {
	keys := map[string]bool{}
	for _, side := range []map[string]json.RawMessage{lane, base} {
		for key := range side {
			if !slices.Contains(safeSettings, key) {
				keys[key] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(keys))
}

// worktreeFile reads one file of a lane's worktree as the CLI would, following its links, up to the bound a repository's
// file holds to. A file that is absent, unreadable, or past the bound reads as none.
func worktreeFile(spec ports.SessionSpec, name string) []byte {
	file, err := os.Open(filepath.Join(spec.Dir, filepath.FromSlash(name)))
	if err != nil {
		return nil
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRepositoryFile+1))
	if err != nil || len(raw) > maxRepositoryFile {
		return nil
	}
	return raw
}

// settingsKeys are a settings file's top-level keys. A file that is absent or does not parse holds none.
func settingsKeys(raw []byte) map[string]json.RawMessage {
	var keys map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &keys) != nil {
		return map[string]json.RawMessage{}
	}
	return keys
}

// canonical is a value as JSON with its objects' keys sorted and no space, so two spellings of one value are one. An
// absent value is empty.
func canonical(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// describeChange says what changed of one key, a line for each entry of it that differs: the hook events, plugins,
// permission lists, variables, and servers it names, by their names, and an entry's value only where it is a command or
// a rule and never under a key whose values can be credentials. The control plane makes each line plain text and takes
// out the shapes of credentials before anyone reads it (control.recordedSettings).
func describeChange(file, key string, base, lane json.RawMessage) []string {
	at := file + " " + key
	switch {
	case base == nil:
		return append([]string{at + ": added"}, entries(at, nil, lane, key)...)
	case lane == nil:
		return []string{at + ": removed"}
	}
	if lines := entries(at, base, lane, key); len(lines) != 0 {
		return lines
	}
	return []string{at + ": changed" + shownValue(lane, key)}
}

// entries are the lines for each entry of an object or a list that differs between two values of one key.
func entries(at string, base, lane json.RawMessage, key string) []string {
	var baseObject, laneObject map[string]json.RawMessage
	if json.Unmarshal(lane, &laneObject) == nil && laneObject != nil {
		_ = json.Unmarshal(base, &baseObject)
		var out []string
		names := slices.Sorted(maps.Keys(laneObject))
		for name := range baseObject {
			if _, kept := laneObject[name]; !kept {
				names = append(names, name)
			}
		}
		for _, name := range names {
			before, after := baseObject[name], laneObject[name]
			switch {
			case before == nil:
				out = append(out, at+"."+name+": added"+shownValue(after, key))
			case after == nil:
				out = append(out, at+"."+name+": removed")
			case canonical(before) != canonical(after):
				if listed := listChanges(at+"."+name, before, after, key); len(listed) != 0 {
					out = append(out, listed...)
				} else {
					out = append(out, at+"."+name+": changed"+shownValue(after, key))
				}
			}
		}
		return out
	}
	return listChanges(at, base, lane, key)
}

// listChanges are the entries added to and removed from a list of text, such as permission rules or server names.
func listChanges(at string, base, lane json.RawMessage, key string) []string {
	var before, after []string
	if json.Unmarshal(lane, &after) != nil || (base != nil && json.Unmarshal(base, &before) != nil) {
		return nil
	}
	var out []string
	for _, entry := range after {
		if !slices.Contains(before, entry) {
			out = append(out, at+": added "+shown(entry, key))
		}
	}
	for _, entry := range before {
		if !slices.Contains(after, entry) {
			out = append(out, at+": removed "+shown(entry, key))
		}
	}
	return out
}

// shownValue is a value as a summary line ends with it: text as itself, and anything else by its kind alone.
func shownValue(raw json.RawMessage, key string) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return " " + shown(text, key)
	}
	var on bool
	if json.Unmarshal(raw, &on) == nil {
		return fmt.Sprintf(" to %t", on)
	}
	return ""
}

// shown is one entry of a summary line, held to its bound, and hidden under a key whose values can be credentials.
func shown(text, key string) string {
	if slices.Contains(secretValues, key) {
		return "(value hidden)"
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > lanes.MaxSettingsText/2 {
		text = text[:lanes.MaxSettingsText/2] + "..."
	}
	return fmt.Sprintf("%q", text)
}

// errSettingsNotRestorable says a worktree's settings file cannot be put back without writing past the worktree.
var errSettingsNotRestorable = errors.New("a directory on the way to a settings file or a piece in the worktree is a link or no directory, so it cannot be restored")

// restoreBaseSettings puts each project settings file a member chose the repository's version of back as the commit the
// lane's work began from holds it (atSettingsBase), or removes it where that commit holds none, each piece whose
// frontmatter hooks the lane added or changed (restorePieces), and then each file a hook runs that the lane changed
// (restoreHookFiles). It writes only inside the worktree: a `.claude` that is a
// link is refused, and a file that is a link is replaced, never written through.
func restoreBaseSettings(spec ports.SessionSpec) error {
	spec = atSettingsBase(spec)
	if !spec.SettingsAtBase || !comparable(spec) {
		return nil
	}
	dir := filepath.Join(spec.Dir, ".claude")
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case !info.IsDir():
		return errSettingsNotRestorable
	}
	for _, file := range settingsFiles(spec) {
		path := filepath.Join(spec.Dir, filepath.FromSlash(file))
		base := repositoryFile(spec, file)
		current := worktreeFile(spec, file)
		if bytes.Equal(base, current) {
			continue
		}
		if base == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := replaceFile(path, base); err != nil {
			return err
		}
	}
	if err := restorePieces(spec); err != nil {
		return err
	}
	return restoreHookFiles(spec)
}

// replaceFile puts a regular file at path in one step: it writes a file of its own beside it, which it creates and
// never opens through a link, and moves it over whatever path names, a link included.
func replaceFile(path string, body []byte) error {
	next := path + ".ropeshift"
	if err := os.Remove(next); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	if closed := file.Close(); err == nil {
		err = closed
	}
	if err == nil {
		err = os.Rename(next, path)
	}
	if err != nil {
		_ = os.Remove(next)
	}
	return err
}
