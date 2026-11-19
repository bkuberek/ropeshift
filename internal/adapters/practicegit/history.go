package practicegit

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"math"
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
	// secretScanCheckObjects bounds the objects one `git cat-file --batch-check` run is asked about.
	secretScanCheckObjects = 4096
	// secretScanFlushRuns is how many runs of added lines to files that were not created a history scan holds before
	// it reads those files, which bounds what it holds on a long history.
	secretScanFlushRuns = 1000
)

// secretScanEnvironment is what every git command of the scan runs under, so the repository's own settings cannot
// change what it reads: paths print as they are and not in quotes, no file system monitor runs, and a replacement
// ref cannot stand in for an object. Git 2.44 and later also fail on a missing object of a partial clone and do not
// fetch it; Scan reads no object of a partial clone, because an older git ignores that setting.
var (
	secretScanEnvironment = []string{"GIT_NO_REPLACE_OBJECTS=1", gitcli.NoLazyFetch}
	secretScanConfig      = [][2]string{{"core.quotePath", "false"}, {"core.fsmonitor", "false"}}
)

var _ ports.SecretScans = SecretScanner{}

// SecretScanner scans a checkout for secrets with read-only git commands: `rev-parse`, `merge-base`, `ls-tree`,
// `cat-file`, and `log`. Env replaces the process environment when it is set, as it does for gitcli.Git.
type SecretScanner struct {
	Env []string
	// flushAfter is how many runs of added lines to files that were not created a history scan holds before it reads
	// those files, and is secretScanFlushRuns when it is zero.
	flushAfter int
}

// Scan scans the tracked files at tip, and the patches of the history reachable from tip. The history scan skips
// what `from` reaches when `from` is an ancestor of tip, and otherwise reads the whole history. The rules that match
// one line read the lines each commit added and no others; the rules that match across lines, a private key, also read
// each file a commit changed, and count a match for the commit when it covers a line the commit added. A match found in the tree and in the history is one match, with the earliest
// commit that added it; a match only the tree holds is reported at tip. The result names `from` by its commit ID when
// the scan skipped what it reaches, and says it is partial for a shallow clone. A partial clone lacks objects that git
// fetches from its promisor remote when a command reads one, so the scan of one reads no object: it finds no match and
// says it is partial. No match and no error holds the matched text.
func (s SecretScanner) Scan(ctx context.Context, dir, tip, from string) (ports.SecretScan, error) {
	if err := ctx.Err(); err != nil {
		return ports.SecretScan{}, fmt.Errorf("scan for secrets: %w", err)
	}
	git := gitcli.Git{Dir: dir, Env: s.Env, Extra: secretScanEnvironment, Config: secretScanConfig}
	// A partial clone is asked for first, because resolving a commit ID it lacks fetches the commit.
	partialClone, err := git.PartialClone(ctx)
	if err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "check the repository for a partial clone", err)
	}
	if partialClone {
		return ports.SecretScan{Partial: secretScanPartialCloneReason}, nil
	}
	tipCommit, err := secretScanCommit(ctx, git, tip)
	if err != nil {
		return ports.SecretScan{}, fmt.Errorf("scan for secrets: %w", err)
	}
	// The options fix what the patches hold whatever the repository's settings and attributes say: every commit with
	// a patch, a merge by its diff against its first parent, every file as text, paths from the root of the
	// repository, and no context lines between hunks.
	args := []string{"log", "-p", "--no-color", "--no-ext-diff", "--no-textconv", "--no-show-signature", "--root",
		"--diff-merges=first-parent", "--text", "--no-relative", "--unified=0", "--inter-hunk-context=0", "--no-renames",
		"--full-index", "--src-prefix=a/", "--dst-prefix=b/", "--topo-order", "--reverse", "--format=%H", tipCommit}
	scanned := ""
	if base, ok := secretScanBase(ctx, git, from, tipCommit); ok {
		scanned = base
		args = append(args, "^"+base)
	}
	args = append(args, "--")
	if err := ctx.Err(); err != nil {
		return ports.SecretScan{}, fmt.Errorf("scan for secrets: %w", err)
	}
	found := newSecretFindings()
	found.flushAfter = cmp.Or(s.flushAfter, secretScanFlushRuns)
	found.flush = func(touched map[string][]secretTouch) error { return found.scanTouchedBlobs(ctx, git, touched) }
	if err := git.Stream(ctx, func(r io.Reader) error { return found.readPatches(r) }, args...); err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "scan the history for secrets", err)
	}
	if err := found.scanTouchedBlobs(ctx, git, found.touched); err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "scan the changed files for secrets", err)
	}
	if err := found.scanTree(ctx, git, tipCommit); err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "scan the tree for secrets", err)
	}
	partial, err := secretScanPartial(ctx, git)
	if err != nil {
		return ports.SecretScan{}, secretScanFailure(ctx, "check the repository for a shallow history", err)
	}
	result := found.result(scanned)
	result.Partial = partial
	return result, nil
}

// secretScanPartialCloneReason is why the scan of a partial clone read nothing.
const secretScanPartialCloneReason = "the repository is a partial clone, so its history was not scanned: reading it would fetch the missing objects"

// secretScanPartial says why the history a scan can read is less than the repository's: a shallow clone holds
// only the commits down to its boundary. It is empty for a repository with its whole history.
func secretScanPartial(ctx context.Context, git gitcli.Git) (string, error) {
	answer, err := git.Text(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", err
	}
	if answer == "true" {
		return "the repository is a shallow clone, so the history before its shallow boundary was not scanned", nil
	}
	return "", nil
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

// secretFound is a match and the place of its commit in the history, oldest first.
type secretFound struct {
	match practices.SecretMatch
	rank  int
}

// secretTouch is a run of lines a commit added to a file, which is the blob it left at a path.
type secretTouch struct {
	path, commit string
	first, last  int
}

// secretFindings collects the matches of one scan, and the paths it skipped.
type secretFindings struct {
	matches map[secretKey]secretFound
	skipped map[string]bool
	// rank holds the place of each commit the history scan read, oldest first.
	rank map[string]int
	// touched holds, for each blob a commit left in a file it changed and did not create, the runs of lines the commit
	// added to it. pending counts the runs, and flush reads the blobs when flushAfter runs are held, which a history
	// scan sets so it holds no more than that.
	touched    map[string][]secretTouch
	pending    int
	flushAfter int
	flush      func(touched map[string][]secretTouch) error
	// find scans a text with the secret rules.
	find func(text string) []ledger.SecretMatch
}

func newSecretFindings() *secretFindings {
	return &secretFindings{
		matches: map[secretKey]secretFound{}, skipped: map[string]bool{}, rank: map[string]int{}, touched: map[string][]secretTouch{},
		find: ledger.FindSecrets,
	}
}

// add keeps the match of a key that sits in the earliest commit. A commit the history scan did not read, such as the
// tip when no commit is new, comes after every commit it did.
func (f *secretFindings) add(m practices.SecretMatch) {
	key := secretKey{m.Rule, m.Path, m.Digest}
	rank, ok := f.rank[m.Commit]
	if !ok {
		rank = math.MaxInt
	}
	if kept, seen := f.matches[key]; !seen || rank < kept.rank {
		f.matches[key] = secretFound{m, rank}
	}
}

// locatedMatch is a match with the first and last line of a file it covers.
type locatedMatch struct {
	ledger.SecretMatch
	line, last int
}

// locate gives each match, which are sorted and apart, the lines it covers in text, whose first line is line `first`
// of a file. It reads the text once.
func locate(text string, first int, matches []ledger.SecretMatch) []locatedMatch {
	located := make([]locatedMatch, 0, len(matches))
	line, offset := first, 0
	for _, m := range matches {
		line += strings.Count(text[offset:m.Start], "\n")
		inside := strings.Count(text[m.Start:m.End], "\n")
		last := line + inside
		if m.End > m.Start && text[m.End-1] == '\n' {
			last--
		}
		located = append(located, locatedMatch{SecretMatch: m, line: line, last: last})
		line, offset = line+inside, m.End
	}
	return located
}

// addMatches adds what a scan of a text found, as found in a path at a commit.
func (f *secretFindings) addMatches(located []locatedMatch, path, commit string) {
	for _, m := range located {
		f.add(practices.SecretMatch{Rule: m.Rule, Path: path, Line: m.line, Commit: commit, Digest: m.Digest})
	}
}

// addText scans text, whose first line is line `first` of a file, and adds what it finds.
func (f *secretFindings) addText(text, path, commit string, first int) {
	f.addMatches(locate(text, first, f.find(text)), path, commit)
}

func (f *secretFindings) result(scannedFrom string) ports.SecretScan {
	matches := make([]practices.SecretMatch, 0, len(f.matches))
	for _, found := range f.matches {
		matches = append(matches, found.match)
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

// scanTree scans each tracked blob at commit. A blob is read and scanned once however many paths hold it.
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
	return readBlobsIn(ctx, git, objects, size, func(object string, content []byte) {
		if secretScanBinary(content) {
			for _, path := range paths[object] {
				f.skipped[path] = true
			}
			return
		}
		text := string(content)
		located := locate(text, 1, f.find(text))
		for _, path := range paths[object] {
			f.addMatches(located, path, commit)
		}
	})
}

// scanTouchedBlobs scans, for the secret rules that match across lines, each blob a commit left in a file it
// changed and did not create. A commit that changes lines inside such a match shows none of the lines around them, so
// its added lines alone do not hold the match. A match counts for a commit when it covers a line the commit added.
func (f *secretFindings) scanTouchedBlobs(ctx context.Context, git gitcli.Git, touched map[string][]secretTouch) error {
	objects := make([]string, 0, len(touched))
	for object := range touched {
		objects = append(objects, object)
	}
	slices.Sort(objects)
	size, err := blobSizes(ctx, git, objects)
	if err != nil {
		return err
	}
	var readable []string
	for _, object := range objects {
		if size[object] > secretScanMaxBytes {
			for _, touch := range touched[object] {
				f.skipped[touch.path] = true
			}
			continue
		}
		readable = append(readable, object)
	}
	return readBlobsIn(ctx, git, readable, size, func(object string, content []byte) {
		touches := touched[object]
		if secretScanBinary(content) {
			for _, touch := range touches {
				f.skipped[touch.path] = true
			}
			return
		}
		text := string(content)
		for _, m := range locate(text, 1, ledger.FindBlockSecrets(text)) {
			for _, touch := range touches {
				if touch.first <= m.last && m.line <= touch.last {
					f.addMatches([]locatedMatch{m}, touch.path, touch.commit)
				}
			}
		}
	})
}

// blobSizes reads the size of each blob with `git cat-file --batch-check`.
func blobSizes(ctx context.Context, git gitcli.Git, objects []string) (map[string]int, error) {
	size := make(map[string]int, len(objects))
	for len(objects) > 0 {
		n := min(len(objects), secretScanCheckObjects)
		out, err := git.Feed(ctx, []byte(strings.Join(objects[:n], "\n")+"\n"), "cat-file", "--batch-check")
		if err != nil {
			return nil, err
		}
		answers := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if len(answers) != n {
			return nil, fmt.Errorf("read the sizes of %d blobs: %d answers", n, len(answers))
		}
		for i, answer := range answers {
			fields := strings.Fields(answer)
			length, err := 0, error(nil)
			if len(fields) == 3 {
				length, err = strconv.Atoi(fields[2])
			}
			if len(fields) != 3 || fields[0] != objects[i] || fields[1] != "blob" || err != nil || length < 0 {
				return nil, fmt.Errorf("read the size of blob %s: unexpected answer", objects[i])
			}
			size[objects[i]] = length
		}
		objects = objects[n:]
	}
	return size, nil
}

// readBlobsIn reads blobs, whose sizes are known, in runs of about secretScanBatchBytes, and hands each blob's
// content to visit.
func readBlobsIn(ctx context.Context, git gitcli.Git, objects []string, size map[string]int, visit func(object string, content []byte)) error {
	for len(objects) > 0 {
		n, total := 0, 0
		for n < len(objects) && (n == 0 || total+size[objects[n]] <= secretScanBatchBytes) {
			total += size[objects[n]]
			n++
		}
		out, err := git.Feed(ctx, []byte(strings.Join(objects[:n], "\n")+"\n"), "cat-file", "--batch")
		if err != nil {
			return err
		}
		if err := readBlobs(bytes.NewReader(out), objects[:n], visit); err != nil {
			return err
		}
		objects = objects[n:]
	}
	return nil
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
		if err := f.flushTouched(); err != nil {
			return err
		}
	}
}

// flushTouched hands on the files held when flushAfter runs of added lines are held, and holds none after.
func (f *secretFindings) flushTouched() error {
	if f.flush == nil || f.pending < f.flushAfter {
		return nil
	}
	touched := f.touched
	f.touched, f.pending = map[string][]secretTouch{}, 0
	return f.flush(touched)
}

// patchReader is the state of a read of patches: the commit, the file, and the hunk it is in.
type patchReader struct {
	found  *secretFindings
	commit string
	path   string
	// blob is the file's blob after the commit, empty when the commit deleted the file, created it, or left a
	// submodule there: a file a commit created holds all it matches in the lines it added.
	blob               string
	created, submodule bool
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
		if _, seen := p.found.rank[line]; !seen {
			p.found.rank[line] = len(p.found.rank)
		}
	case strings.HasPrefix(line, "diff --git "):
		p.path, p.skipFile, p.added, p.blob, p.created, p.submodule = patchPath(strings.TrimPrefix(line, "diff --git ")), false, 0, "", false, false
	case strings.HasPrefix(line, "new file mode "):
		p.created, p.submodule = true, strings.HasSuffix(line, " 160000")
	case strings.HasPrefix(line, "new mode "):
		p.submodule = strings.HasSuffix(line, " 160000")
	case strings.HasPrefix(line, "index "):
		p.readIndex(line)
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

// readIndex reads the blob a file has after the commit from `index <old>..<new> [mode]`, which holds full IDs.
func (p *patchReader) readIndex(line string) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	_, after, ok := strings.Cut(fields[1], "..")
	if !ok || strings.Trim(after, "0") == "" {
		return
	}
	p.blob = after
	if len(fields) > 2 && fields[2] == "160000" {
		p.submodule = true
	}
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
			if p.blob != "" && !p.created && !p.submodule {
				p.found.touched[p.blob] = append(p.found.touched[p.blob],
					secretTouch{path: p.path, commit: p.commit, first: p.first, last: p.first + len(p.text) - 1})
				p.found.pending++
			}
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
