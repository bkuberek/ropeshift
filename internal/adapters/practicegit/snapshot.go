// Package practicegit reads a repository with the git command line for the practices check. It runs only
// read-only git commands against the checkout, and never reads its working tree.
package practicegit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
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
}

var _ ports.PracticeSnapshots = Snapshots{}

// snapshotCommitID matches a full commit ID, SHA-1 or SHA-256, which is never a branch name.
var snapshotCommitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// snapshotHexName matches a name that can be an abbreviated commit ID.
var snapshotHexName = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// snapshotBlobLimit is the largest blob the snapshot reads, in bytes. A larger one is unreadable.
const snapshotBlobLimit = 1 << 20

// snapshotNoReplace makes git read the objects a commit names and not the ones a replace ref names in their place.
var snapshotNoReplace = []string{"GIT_NO_REPLACE_OBJECTS=1"}

// errSnapshotTooLarge means a blob is over snapshotBlobLimit.
var errSnapshotTooLarge = errors.New("the blob is over the size limit")

// snapshotIgnoreFile is the name of the file whose committed copies decide the ignore answers.
const snapshotIgnoreFile = ".gitignore"

// Snapshot returns the tracked paths at a commit, a reader of a tracked file's text, and the ignore answer for
// each path of practices.Probes. The commit is a commit ID, or the name of the base branch, which resolves to
// origin's branch when the checkout holds it and to the local branch otherwise. A name that resolves to
// neither is ports.ErrNotFound.
func (s Snapshots) Snapshot(ctx context.Context, dir, commit string) (practices.Snapshot, error) {
	git := gitcli.Git{Dir: dir, Env: s.Env, Extra: snapshotNoReplace}
	resolved, err := snapshotResolve(ctx, git, commit)
	if err != nil {
		return practices.Snapshot{}, err
	}
	entries, err := snapshotTree(ctx, git, resolved)
	if err != nil {
		return practices.Snapshot{}, err
	}
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i] = entry.path
	}
	ignored, err := snapshotIgnored(ctx, git, resolved, entries, paths)
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

// snapshotResolve turns a commit ID or a base branch's name into a commit ID.
func snapshotResolve(ctx context.Context, git gitcli.Git, name string) (string, error) {
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
		id, err := git.Commit(ctx, ref)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, gitcli.ErrNoRevision) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: the repository holds no commit or branch %s", ports.ErrNotFound, name)
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
// and needs no close.
func snapshotReader(ctx context.Context, git gitcli.Git, commit string) func(string) (string, bool) {
	return func(name string) (string, bool) {
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

// snapshotCopyTarget returns where a tracked path is copied below root, and false for a path that would leave
// root or reach git's own directory. A tree can name an entry `..`, so a tracked path is not always local.
func snapshotCopyTarget(root, name string) (string, bool) {
	for part := range strings.SplitSeq(name, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return "", false
		}
	}
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) {
		return "", false
	}
	target := filepath.Join(root, local)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}

// snapshotIgnored answers, for each path of practices.Probes, whether the `.gitignore` files committed at the
// commit ignore it. It copies those files into a temporary repository of their own and asks git there, so
// the working tree, the person's global excludes, and the checkout's `info/exclude` never count. The
// temporary repository is gone when this returns.
func snapshotIgnored(ctx context.Context, git gitcli.Git, commit string, entries []snapshotEntry, paths []string) (map[string]bool, error) {
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

	root, err := os.MkdirTemp("", "ropeshift-practice-ignore-")
	if err != nil {
		return nil, fmt.Errorf("making a repository to check ignore rules in: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	scratch := gitcli.Git{Dir: root, Env: snapshotIsolatedEnv(root)}
	if _, err := scratch.Output(ctx, "init", "--quiet", "--template="); err != nil {
		return nil, err
	}
	for _, name := range ignoreFiles {
		target, ok := snapshotCopyTarget(root, name)
		if !ok {
			continue
		}
		content, err := snapshotBlob(ctx, git, commit, name)
		if errors.Is(err, errSnapshotTooLarge) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("copying %s: %w", name, err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return nil, fmt.Errorf("copying %s: %w", name, err)
		}
	}
	for _, probe := range probes {
		_, err := scratch.Output(ctx, "check-ignore", "--no-index", "-q", "--", snapshotLiteral(probe))
		var exit *exec.ExitError
		switch {
		case err == nil:
			ignored[probe] = true
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			ignored[probe] = false
		default:
			return nil, fmt.Errorf("checking whether %s is ignored: %w", probe, err)
		}
	}
	return ignored, nil
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
