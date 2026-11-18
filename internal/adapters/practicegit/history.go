package practicegit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

const (
	// secretScanMaxBytes is the size above which a file, or the text a commit added to one, is skipped.
	secretScanMaxBytes = 1 << 20
	// secretScanBinaryProbe is how many leading bytes are read for a NUL byte, which makes a file binary.
	secretScanBinaryProbe = 8000
	// secretScanBatchBytes bounds the blobs one `git cat-file --batch` run reads.
	secretScanBatchBytes = 8 << 20
)

// secretScanEnvironment is what every git command of the scan runs under, so the repository's own settings cannot
// change what it reads: paths print as they are and not in quotes, no file system monitor runs, and a replacement
// ref cannot stand in for an object.
var secretScanEnvironment = []string{
	"GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_COUNT=2",
	"GIT_CONFIG_KEY_0=core.quotePath", "GIT_CONFIG_VALUE_0=false",
	"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
}

var _ ports.SecretScans = SecretScanner{}

// SecretScanner scans a checkout for secrets with read-only git commands: `rev-parse`, `merge-base`, `ls-tree`,
// `cat-file`, and `log`. Env replaces the process environment when it is set, as it does for gitcli.Git.
type SecretScanner struct {
	Env []string
}

// Scan scans the tracked files at tip, and the patches of the history reachable from tip. The history scan skips
// what `from` reaches when `from` is an ancestor of tip, and otherwise reads the whole history. It reads the lines
// each commit added and no others. A match found in the tree and in the history is one match, with the earliest
// commit that added it; a match only the tree holds is reported at tip. No match and no error holds the matched text.
func (s SecretScanner) Scan(ctx context.Context, dir, tip, from string) (ports.SecretScan, error) {
	if err := ctx.Err(); err != nil {
		return ports.SecretScan{}, fmt.Errorf("scan for secrets: %w", err)
	}
	git := gitcli.Git{Dir: dir, Env: s.Env, Extra: secretScanEnvironment}
	tipCommit, err := secretScanCommit(ctx, git, tip)
	if err != nil {
		return ports.SecretScan{}, fmt.Errorf("scan for secrets: %w", err)
	}
	// The options fix what the patches hold whatever the repository's settings and attributes say: every commit with
	// a patch, a merge by its diff against its first parent, every file as text, paths from the root of the
	// repository, and no context lines between hunks.
	args := []string{"log", "-p", "--no-color", "--no-ext-diff", "--no-textconv", "--no-show-signature", "--root",
		"--diff-merges=first-parent", "--text", "--no-relative", "--unified=0", "--inter-hunk-context=0", "--no-renames",
		"--src-prefix=a/", "--dst-prefix=b/", "--topo-order", "--reverse", "--format=%H", tipCommit}
	scanned := ""
	if base, ok := secretScanBase(ctx, git, from, tipCommit); ok {
		scanned = from
		args = append(args, "^"+base)
	}
	args = append(args, "--")
	if err := ctx.Err(); err != nil {
		return ports.SecretScan{}, fmt.Errorf("scan for secrets: %w", err)
	}
	found := newSecretFindings()
	if err := git.Stream(ctx, func(r io.Reader) error { return found.readPatches(r) }, args...); err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "scan the history for secrets", err)
	}
	if err := found.scanTree(ctx, git, tipCommit); err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "scan the tree for secrets", err)
	}
	return found.result(scanned), nil
}

// secretScanFailure reports a failed stage. A scan whose context ended reports that and not the signal that stopped git.
func secretScanFailure(ctx context.Context, stage string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return fmt.Errorf("%s: %w", stage, err)
}

// secretScanCommit resolves a revision to its commit. A revision that is empty or starts with `-` is no revision,
// so it never reaches git as an option.
func secretScanCommit(ctx context.Context, git gitcli.Git, revision string) (string, error) {
	if revision == "" || strings.HasPrefix(revision, "-") {
		return "", fmt.Errorf("%w: %q", gitcli.ErrNoRevision, revision)
	}
	return git.Commit(ctx, revision)
}

// secretScanBase resolves `from` to the commit an incremental scan starts after. It says no when `from` is empty,
// names no commit, or is not an ancestor of tip, and the scan then reads the whole history.
func secretScanBase(ctx context.Context, git gitcli.Git, from, tip string) (string, bool) {
	if from == "" {
		return "", false
	}
	base, err := secretScanCommit(ctx, git, from)
	if err != nil {
		return "", false
	}
	_, err = git.Output(ctx, "merge-base", "--is-ancestor", base, tip)
	return base, err == nil
}

// secretKey names a secret in a path: the same text in the same file is one finding.
type secretKey struct{ rule, path, digest string }

// secretFindings collects the matches of one scan, and the paths it skipped.
type secretFindings struct {
	matches map[secretKey]practices.SecretMatch
	skipped map[string]bool
}

func newSecretFindings() *secretFindings {
	return &secretFindings{matches: map[secretKey]practices.SecretMatch{}, skipped: map[string]bool{}}
}

// add keeps the first match of a key, which is the earliest commit, because the history is read oldest first and the
// tree is read after it.
func (f *secretFindings) add(m practices.SecretMatch) {
	key := secretKey{m.Rule, m.Path, m.Digest}
	if _, ok := f.matches[key]; !ok {
		f.matches[key] = m
	}
}

// addText scans text, whose first line is line `first` of a file, and adds what it finds.
func (f *secretFindings) addText(text, path, commit string, first int) {
	for _, m := range ledger.FindSecrets(text) {
		f.add(practices.SecretMatch{
			Rule: m.Rule, Path: path, Line: first + strings.Count(text[:m.Start], "\n"), Commit: commit, Digest: m.Digest,
		})
	}
}

func (f *secretFindings) result(scannedFrom string) ports.SecretScan {
	matches := make([]practices.SecretMatch, 0, len(f.matches))
	for _, m := range f.matches {
		matches = append(matches, m)
	}
	slices.SortFunc(matches, func(a, b practices.SecretMatch) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Rule+a.Digest, b.Rule+b.Digest)
	})
	return ports.SecretScan{Matches: matches, ScannedFrom: scannedFrom, Skipped: len(f.skipped)}
}

// binary says a file's bytes are no text: one of its first 8000 bytes is NUL, as git reads it.
func secretScanBinary(content []byte) bool {
	return bytes.IndexByte(content[:min(len(content), secretScanBinaryProbe)], 0) >= 0
}

// scanTree scans each tracked blob at commit. A blob is read once however many paths hold it.
func (f *secretFindings) scanTree(ctx context.Context, git gitcli.Git, commit string) error {
	listing, err := git.Output(ctx, "ls-tree", "-r", "-l", "-z", "--full-tree", commit)
	if err != nil {
		return err
	}
	paths := map[string][]string{}
	size := map[string]int{}
	var objects []string
	for _, entry := range strings.Split(string(listing), "\x00") {
		header, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 4 || fields[1] != "blob" {
			continue
		}
		length, err := strconv.Atoi(fields[3])
		if err != nil {
			return fmt.Errorf("read the size of %s", path)
		}
		if length > secretScanMaxBytes {
			f.skipped[path] = true
			continue
		}
		if _, seen := paths[fields[2]]; !seen {
			objects = append(objects, fields[2])
			size[fields[2]] = length
		}
		paths[fields[2]] = append(paths[fields[2]], path)
	}
	for len(objects) > 0 {
		n, total := 0, 0
		for n < len(objects) && (n == 0 || total+size[objects[n]] <= secretScanBatchBytes) {
			total += size[objects[n]]
			n++
		}
		if err := f.scanBlobs(ctx, git, commit, objects[:n], paths); err != nil {
			return err
		}
		objects = objects[n:]
	}
	return nil
}

// scanBlobs reads blobs with one `git cat-file --batch` run and scans each for every path that holds it.
func (f *secretFindings) scanBlobs(ctx context.Context, git gitcli.Git, commit string, objects []string, paths map[string][]string) error {
	out, err := git.Feed(ctx, []byte(strings.Join(objects, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return err
	}
	return readBlobs(bytes.NewReader(out), objects, func(object string, content []byte) {
		for _, path := range paths[object] {
			if secretScanBinary(content) {
				f.skipped[path] = true
				continue
			}
			f.addText(string(content), path, commit, 1)
		}
	})
}

// readBlobs reads the answers of `git cat-file --batch` for objects, in order, and hands each blob's content to visit.
func readBlobs(r io.Reader, objects []string, visit func(object string, content []byte)) error {
	reader := bufio.NewReader(r)
	for _, object := range objects {
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read blob %s: %w", object, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[1] != "blob" {
			return fmt.Errorf("read blob %s: unexpected answer", object)
		}
		length, err := strconv.Atoi(fields[2])
		if err != nil || length < 0 {
			return fmt.Errorf("read blob %s: unexpected size", object)
		}
		content := make([]byte, length+1)
		if _, err := io.ReadFull(reader, content); err != nil {
			return fmt.Errorf("read blob %s: %w", object, err)
		}
		visit(object, content[:length])
	}
	return nil
}

// readPatches reads the output of `git log -p --format=%H` and scans the lines each commit added. A hunk's added
// lines are joined before they are scanned, so a secret that spans lines matches. A file with a binary patch, or
// whose added text is over the size limit, is skipped.
func (f *secretFindings) readPatches(r io.Reader) error {
	p := patchReader{found: f}
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, truncated, err := readPatchLine(reader, secretScanMaxBytes)
		if err == io.EOF {
			p.endHunk()
			return nil
		}
		if err != nil {
			return err
		}
		p.line(string(line), truncated)
	}
}

// patchReader is the state of a read of patches: the commit, the file, and the hunk it is in.
type patchReader struct {
	found  *secretFindings
	commit string
	path   string
	// skipFile says the file's added text is not scanned; added counts the bytes of it so far.
	skipFile bool
	added    int
	// oldLeft and newLeft count the lines of the hunk not read yet. next is the new file's line number of the next line
	// of the hunk, and first the new file's line number of the first line in text.
	oldLeft, newLeft, next, first int
	text                          []string
}

func (p *patchReader) line(line string, truncated bool) {
	if (p.oldLeft > 0 || p.newLeft > 0) && p.hunkLine(line, truncated) {
		return
	}
	switch {
	case isCommitID(line):
		p.commit = line
	case strings.HasPrefix(line, "diff --git "):
		p.path, p.skipFile, p.added = patchPath(strings.TrimPrefix(line, "diff --git ")), false, 0
	case strings.HasPrefix(line, "Binary files ") && !strings.HasSuffix(line, " and /dev/null differ"):
		p.found.skipped[p.path] = true
	case strings.HasPrefix(line, "@@ "):
		p.startHunk(line)
	}
}

// hunkLine reads a line of a hunk, which the counts of its header say is still open. It says no for a line that is
// none, which ends the hunk and is read as a header.
func (p *patchReader) hunkLine(line string, truncated bool) bool {
	switch {
	case strings.HasPrefix(line, "+"):
		p.newLeft--
		if len(p.text) == 0 {
			p.first = p.next
		}
		p.next++
		p.addLine(line[1:], truncated)
	case strings.HasPrefix(line, "-"):
		p.oldLeft--
	case strings.HasPrefix(line, `\`):
	case line == "" || strings.HasPrefix(line, " "):
		// A context line, which ends the run of added lines before it, and is no line of the added text.
		p.endHunk()
		p.oldLeft--
		p.newLeft--
		p.next++
	default:
		p.oldLeft, p.newLeft = 0, 0
		p.endHunk()
		return false
	}
	if p.oldLeft <= 0 && p.newLeft <= 0 {
		p.endHunk()
	}
	return true
}

func (p *patchReader) addLine(text string, truncated bool) {
	if p.skipFile {
		return
	}
	p.added += len(text) + 1
	if truncated || p.added > secretScanMaxBytes {
		p.skipFile, p.text = true, nil
		p.found.skipped[p.path] = true
		return
	}
	p.text = append(p.text, text)
}

// startHunk reads the counts and the new start of a header: @@ -old[,n] +new[,n] @@.
func (p *patchReader) startHunk(header string) {
	p.endHunk()
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return
	}
	_, p.oldLeft = hunkRange(fields[1])
	p.next, p.newLeft = hunkRange(fields[2])
}

// endHunk scans the lines added so far in the hunk.
func (p *patchReader) endHunk() {
	if len(p.text) > 0 && !p.skipFile {
		text := strings.Join(p.text, "\n")
		if strings.IndexByte(text, 0) >= 0 {
			p.skipFile = true
			p.found.skipped[p.path] = true
		} else {
			p.found.addText(text, p.path, p.commit, p.first)
		}
	}
	p.text = nil
}

// hunkRange reads `-start[,count]` or `+start[,count]`; a missing count is 1.
func hunkRange(field string) (start, count int) {
	count = 1
	first, second, ok := strings.Cut(strings.TrimLeft(field, "+-"), ",")
	start, _ = strconv.Atoi(first)
	if ok {
		count, _ = strconv.Atoi(second)
	}
	return start, count
}

// patchPath reads the path of a `diff --git a/<path> b/<path>` header. With renames off both sides are one path.
func patchPath(header string) string {
	if strings.HasPrefix(header, `"`) {
		if quoted, err := strconv.QuotedPrefix(header); err == nil {
			if name, err := strconv.Unquote(quoted); err == nil {
				return strings.TrimPrefix(name, "a/")
			}
		}
		return header
	}
	if length := (len(header) - 5) / 2; length > 0 && len(header) == 2*length+5 && strings.HasPrefix(header, "a/") && header[2+length:2+length+3] == " b/" {
		return header[2 : 2+length]
	}
	return header
}

// isCommitID says a line is a commit's ID: 40 or 64 hexadecimal digits and nothing else.
func isCommitID(line string) bool {
	if len(line) != 40 && len(line) != 64 {
		return false
	}
	for _, c := range line {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// readPatchLine reads one line without its newline. A line over limit bytes is cut there and reported as truncated,
// and the rest of it is read and dropped.
func readPatchLine(r *bufio.Reader, limit int) (line []byte, truncated bool, err error) {
	for {
		chunk, readErr := r.ReadSlice('\n')
		if len(line)+len(chunk) > limit+1 {
			truncated = true
		} else {
			line = append(line, chunk...)
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr == io.EOF && (len(line) > 0 || truncated) {
			readErr = nil
		}
		return bytes.TrimSuffix(line, []byte("\n")), truncated, readErr
	}
}
