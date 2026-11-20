// Package practicegit reads a repository with the git command line for the practices check. It runs only
// read-only git commands against the checkout, and never reads its working tree.
package practicegit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// Snapshots reads a checkout at one commit with git. Env replaces the process environment of the commands run
// in the checkout when it is set, and nil is this process's.
type Snapshots struct {
	Env []string

	ignoreLimits ignoreCopyLimits
}

var _ ports.PracticeSnapshots = Snapshots{}

// snapshotCommitID matches a full commit ID, SHA-1 or SHA-256, which is never a branch name.
var snapshotCommitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// snapshotHexName matches a name that can be an abbreviated commit ID.
var snapshotHexName = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// snapshotBlobLimit is the largest blob the snapshot reads, in bytes. A larger one is unreadable.
const snapshotBlobLimit = 1 << 20

// The ignore copy holds at most snapshotIgnoreCopyFiles files and snapshotIgnoreCopyBytes bytes of `.gitignore`
// blobs in all, however many the commit tracks.
const (
	snapshotIgnoreCopyFiles       = 10_000
	snapshotIgnoreCopyBytes int64 = 16 << 20
)

// ignoreCopyLimits are the caps of one ignore copy, and a zero field is the default cap.
type ignoreCopyLimits struct {
	files int
	bytes int64
}

func (l ignoreCopyLimits) withDefaults() ignoreCopyLimits {
	if l.files <= 0 {
		l.files = snapshotIgnoreCopyFiles
	}
	if l.bytes <= 0 {
		l.bytes = snapshotIgnoreCopyBytes
	}
	return l
}

// snapshotNoReplace makes git read the objects a commit names and not the ones a replace ref names in their place. Git
// 2.44 and later also fail on a missing object of a partial clone and do not fetch it; Snapshot reads no blob of a
// partial clone, because an older git ignores that setting.
var snapshotNoReplace = []string{"GIT_NO_REPLACE_OBJECTS=1", gitcli.NoLazyFetch}

// errSnapshotTooLarge means a blob is over snapshotBlobLimit.
var errSnapshotTooLarge = errors.New("the blob is over the size limit")

// snapshotIgnoreFile is the name of the file whose committed copies decide the ignore answers.
const snapshotIgnoreFile = ".gitignore"

// Snapshot returns the tracked paths at a commit, a reader of a tracked file's text, and the ignore answer for
// each path of practices.Probes. The commit is a commit ID, or the name of the base branch, which resolves to
// origin's branch when the checkout holds it and to the local branch otherwise. A name that resolves to
// neither is ports.ErrNotFound.
//
// A partial clone lacks blobs that git fetches from its promisor remote when a command reads one, so the snapshot of
// one lists the tracked paths, which its trees hold, and reads no blob: every file is unreadable and no ignore probe
// has an answer, and the practices that need either are unknown. A partial clone that lacks the commit or one of its
// trees, as a treeless clone does, has no snapshot, and reading it fetches nothing.
func (s Snapshots) Snapshot(ctx context.Context, dir, commit string) (practices.Snapshot, error) {
	git := gitcli.Git{Dir: dir, Env: s.Env, Extra: snapshotNoReplace}
	// A partial clone is asked for first, because resolving a commit ID it lacks fetches the commit.
	partialClone, err := git.PartialClone(ctx)
	if err != nil {
		return practices.Snapshot{}, err
	}
	resolved, err := snapshotResolve(ctx, git, commit, partialClone)
	if err != nil {
		return practices.Snapshot{}, err
	}
	if partialClone {
		if err := snapshotTreesHeld(ctx, git, resolved); err != nil {
			return practices.Snapshot{}, err
		}
	}
	entries, err := snapshotTree(ctx, git, resolved)
	if err != nil {
		return practices.Snapshot{}, err
	}
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i] = entry.path
	}
	if partialClone {
		return practices.Snapshot{
			Commit:  resolved,
			Paths:   paths,
			Read:    func(string) (string, bool) { return "", false },
			Ignored: map[string]bool{},
		}, nil
	}
	ignored, err := snapshotIgnored(ctx, git, resolved, entries, paths, s.ignoreLimits.withDefaults())
	if err != nil {
		return practices.Snapshot{}, err
	}
	return practices.Snapshot{
		Commit:  resolved,
		Paths:   paths,
		Read:    snapshotReader(ctx, git, resolved),
		Ignored: ignored,
	}, nil
}

// snapshotTreesHeld refuses a commit of a partial clone when the clone lacks the commit or one of its trees, which
// `ls-tree` would fetch from the promisor remote. `rev-list --missing=print` names the missing objects and never
// fetches them, and the blob filter leaves the blobs out, which a partial clone is allowed to lack.
func snapshotTreesHeld(ctx context.Context, git gitcli.Git, commit string) error {
	out, err := git.Text(ctx, "rev-list", "--objects", "--missing=print", "--filter=blob:none", "--no-walk", commit)
	if snapshotBadObject(err, commit) {
		return fmt.Errorf("%w: the repository holds no commit %s", ports.ErrNotFound, commit)
	}
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(out, "\n") {
		missing, found := strings.CutPrefix(line, "?")
		switch {
		case !found:
		case missing == commit:
			return fmt.Errorf("%w: the repository holds no commit %s", ports.ErrNotFound, commit)
		default:
			return fmt.Errorf("the repository is a partial clone that lacks the tree %s of %s, and reading it would fetch the tree", missing, commit)
		}
	}
	return nil
}

// snapshotResolve turns a commit ID or a base branch's name into a commit ID. In a partial clone it reads the ID a ref or
// a full commit ID names and does not read the commit, which the clone may lack and git would fetch;
// snapshotTreesHeld then says whether it holds the commit.
func snapshotResolve(ctx context.Context, git gitcli.Git, name string, partialClone bool) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%w: %q is not a commit or a branch", ports.ErrNotFound, name)
	}
	refs := []string{name}
	if !snapshotCommitID.MatchString(name) {
		refs = []string{"refs/remotes/origin/" + name, "refs/heads/" + name}
		if snapshotHexName.MatchString(name) {
			refs = append(refs, name)
		}
	}
	for _, ref := range refs {
		id, err := snapshotRevision(ctx, git, ref, partialClone)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, gitcli.ErrNoRevision) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: the repository holds no commit or branch %s", ports.ErrNotFound, name)
}

// snapshotBadObject reports whether a failed `rev-list` is git's answer that the commit is absent. Git before 2.44
// exits 128 with `fatal: bad object <commit>` where later versions print `?<commit>`.
func snapshotBadObject(err error, commit string) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 128 && strings.Contains(err.Error(), "fatal: bad object "+commit)
}

// snapshotRevision resolves a ref to a commit ID, and in a partial clone to the object ID it names without reading the
// object.
func snapshotRevision(ctx context.Context, git gitcli.Git, ref string, partialClone bool) (string, error) {
	if !partialClone {
		return git.Commit(ctx, ref)
	}
	id, err := git.Text(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err != nil {
		return "", fmt.Errorf("%w: %s", gitcli.ErrNoRevision, ref)
	}
	return id, nil
}

// snapshotEntry is one tracked path at a commit and its file mode.
type snapshotEntry struct {
	mode, path string
}

// snapshotTree lists the tracked paths at a commit with their modes.
func snapshotTree(ctx context.Context, git gitcli.Git, commit string) ([]snapshotEntry, error) {
	out, err := git.Output(ctx, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	var entries []snapshotEntry
	for record := range strings.SplitSeq(string(out), "\x00") {
		if record == "" {
			continue
		}
		// A record is "<mode> <type> <object>\t<path>", and a path may hold a tab.
		meta, name, found := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !found || len(fields) != 3 {
			return nil, fmt.Errorf("reading the tree of %s: unexpected entry %q", commit, record)
		}
		entries = append(entries, snapshotEntry{mode: fields[0], path: name})
	}
	return entries, nil
}

// snapshotBlob reads one blob of a commit. A blob over snapshotBlobLimit is errSnapshotTooLarge, and is
// never read.
func snapshotBlob(ctx context.Context, git gitcli.Git, commit, name string) ([]byte, error) {
	object := commit + ":" + name
	size, err := git.Text(ctx, "cat-file", "-s", object)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("reading the size of %s: %w", name, err)
	}
	if n > snapshotBlobLimit {
		return nil, errSnapshotTooLarge
	}
	return git.Output(ctx, "cat-file", "blob", object)
}

// snapshotReader returns a reader that asks git for one blob of the commit per call, so it holds no process
// and needs no close. A path that is not safe is unreadable, and git is not asked for it.
func snapshotReader(ctx context.Context, git gitcli.Git, commit string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if !snapshotSafePath(name) {
			return "", false
		}
		content, err := snapshotBlob(ctx, git, commit, name)
		if err != nil {
			return "", false
		}
		return string(content), true
	}
}

// snapshotIsIgnoreFile reports whether a tracked entry is a regular file named `.gitignore`. A symlink or a
// gitlink of that name is not one.
func snapshotIsIgnoreFile(entry snapshotEntry) bool {
	return (entry.mode == "100644" || entry.mode == "100755") && path.Base(entry.path) == snapshotIgnoreFile
}

// snapshotSafePath reports whether a tracked path stays inside a checkout and clear of git's own directory. A
// tree can name an entry `..`, so a tracked path is not always local. Its components are split at a slash and at a
// backslash, which separates them on Windows.
func snapshotSafePath(name string) bool {
	for part := range strings.SplitSeq(strings.ReplaceAll(name, `\`, "/"), "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(name))
}

// snapshotCopyTarget returns where a tracked path is copied below root, and false for a path that is not safe
// or would leave root.
func snapshotCopyTarget(root, name string) (string, bool) {
	if !snapshotSafePath(name) {
		return "", false
	}
	target := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}

// snapshotIgnored answers, for each path of practices.Probes, whether the `.gitignore` files committed at the
// commit ignore it. It copies those files into a temporary repository of their own and asks git there once for all
// of the probes, so the working tree, the person's global excludes, and the checkout's `info/exclude` never count.
// A probe whose path is not safe is answered as not ignored, and git is never asked about it. The copy holds at most
// limits.files files and limits.bytes bytes: once a file would pass either, it and every file after it are not copied.
// A file over snapshotBlobLimit is not copied either. The probes below the folder of each file not copied have no
// answer, which the evaluator reads as unknown. The
// temporary repository is gone when this returns.
func snapshotIgnored(ctx context.Context, git gitcli.Git, commit string, entries []snapshotEntry, paths []string, limits ignoreCopyLimits) (map[string]bool, error) {
	probes := practices.Probes(paths)
	ignored := make(map[string]bool, len(probes))
	var ignoreFiles []string
	for _, entry := range entries {
		if snapshotIsIgnoreFile(entry) {
			ignoreFiles = append(ignoreFiles, entry.path)
		}
	}
	if len(ignoreFiles) == 0 {
		for _, probe := range probes {
			ignored[probe] = false
		}
		return ignored, nil
	}

	tempDir := os.TempDir()
	root, err := os.MkdirTemp("", "ropeshift-practice-ignore-")
	if err != nil {
		return nil, snapshotWithoutPaths(fmt.Errorf("making a repository to check ignore rules in: %w", err), tempDir)
	}
	defer func() { _ = os.RemoveAll(root) }()
	fail := func(err error) error { return snapshotWithoutPaths(err, root, tempDir) }
	scratch := gitcli.Git{Dir: root, Env: snapshotIsolatedEnv(root)}
	if _, err := scratch.Output(ctx, "init", "--quiet", "--template="); err != nil {
		return nil, fail(err)
	}
	var uncopied []string
	copied, total := 0, int64(0)
	for _, name := range ignoreFiles {
		target, ok := snapshotCopyTarget(root, name)
		if !ok {
			continue
		}
		folder := path.Dir(name)
		if len(uncopied) > 0 || copied >= limits.files {
			uncopied = append(uncopied, folder)
			continue
		}
		content, err := snapshotBlob(ctx, git, commit, name)
		if errors.Is(err, errSnapshotTooLarge) {
			uncopied = append(uncopied, folder)
			continue
		}
		if err != nil {
			return nil, fail(err)
		}
		if total+int64(len(content)) > limits.bytes {
			uncopied = append(uncopied, folder)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fail(fmt.Errorf("copying %s: %w", name, err))
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return nil, fail(fmt.Errorf("copying %s: %w", name, err))
		}
		copied++
		total += int64(len(content))
	}

	asked := make(map[string]string, len(probes))
	var stdin bytes.Buffer
	for _, probe := range probes {
		switch {
		case !snapshotSafePath(probe):
			ignored[probe] = false
		case snapshotBelowAny(probe, uncopied):
		default:
			literal := snapshotLiteral(probe)
			asked[literal] = probe
			stdin.WriteString(literal)
			stdin.WriteByte(0)
		}
	}
	if len(asked) == 0 {
		return ignored, nil
	}
	for _, probe := range asked {
		ignored[probe] = false
	}
	// Without -v, check-ignore writes each ignored path as it was given, ends it with NUL, and exits 1 when none is.
	out, err := scratch.Feed(ctx, stdin.Bytes(), "check-ignore", "--no-index", "--stdin", "-z")
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return nil, fail(fmt.Errorf("checking which paths are ignored: %w", err))
	}
	for literal := range strings.SplitSeq(string(out), "\x00") {
		if literal == "" {
			continue
		}
		probe, ok := asked[literal]
		if !ok {
			return nil, fail(fmt.Errorf("checking which paths are ignored: git answered for %q, which was not asked", literal))
		}
		ignored[probe] = true
	}
	return ignored, nil
}

// snapshotBelowAny reports whether a path is in one of the folders, where "." is the root of the repository.
func snapshotBelowAny(name string, folders []string) bool {
	for _, folder := range folders {
		if folder == "." || strings.HasPrefix(name, folder+"/") {
			return true
		}
	}
	return false
}

// snapshotPathlessError is an error whose text names none of the paths of the temporary repository, and that still
// unwraps to the error it was made from, so a caller can tell what failed.
type snapshotPathlessError struct {
	err  error
	text string
}

func (e snapshotPathlessError) Error() string { return e.text }
func (e snapshotPathlessError) Unwrap() error { return e.err }

// snapshotTempPlaceholder stands for the temporary repository's paths in an error.
const snapshotTempPlaceholder = "<temp>"

// snapshotWithoutPaths returns the error with each of the paths, the longest first, replaced by a placeholder: the
// folder of the temporary repository is a path of the worker's machine, which an error that reaches a report must not name.
func snapshotWithoutPaths(err error, paths ...string) error {
	text := err.Error()
	for _, path := range slices.SortedFunc(slices.Values(paths), func(a, b string) int { return len(b) - len(a) }) {
		if len(path) > 1 {
			text = strings.ReplaceAll(text, path, snapshotTempPlaceholder)
		}
	}
	if text == err.Error() {
		return err
	}
	return snapshotPathlessError{err: err, text: text}
}

// snapshotIsolatedEnv is the whole environment of a command in the temporary repository: no global or system
// configuration, and an empty core.excludesFile, which git reads from its environment as it does `-c`
// (https://git-scm.com/docs/git-config#ENVIRONMENT).
func snapshotIsolatedEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.excludesFile",
		"GIT_CONFIG_VALUE_0=",
	}
}

// snapshotLiteral keeps a path that starts with a colon from reading as pathspec magic, which check-ignore refuses.
func snapshotLiteral(name string) string {
	if strings.HasPrefix(name, ":") {
		return "./" + name
	}
	return name
}
