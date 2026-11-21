package gitcli

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"slices"
	"strconv"
	"strings"
)

// The reads of one file at a commit.
var (
	// ErrNotAFullHash means a commit is not named by the full lowercase hash of a SHA-1 or SHA-256 repository.
	ErrNotAFullHash = errors.New("a commit is named by its full hash")
	// ErrNotAFile means what a commit holds at a path is a directory, a link, or a submodule, and not a file.
	ErrNotAFile = errors.New("not a regular file")
	// ErrObjectMismatch means an object's content does not hash to the name it was read by: a loose object written
	// over the real one, or one an alternate holds in its place.
	ErrObjectMismatch = errors.New("an object does not hash to its name")
)

// FileTooLargeError is a file over the limit of a read, with its size.
type FileTooLargeError struct {
	Size, Limit int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("the file is %d bytes, over the %d a read takes", e.Size, e.Limit)
}

// noReplaceObjects makes git read the objects a commit names and not the ones a replace ref names in their place
// (https://git-scm.com/docs/git#Documentation/git.txt-GITNOREPLACEOBJECTS).
const noReplaceObjects = "GIT_NO_REPLACE_OBJECTS=1"

// FileAt reads the file commit holds at path from the repository's objects, never from a working tree, and says
// whether commit holds one there. Each object on the way, the commit, each tree, and the file, must hash to the name
// it was read by, so a replace ref, an alternate, or a loose object written over the real one can never stand in for
// what the commit holds: such an object is ErrObjectMismatch. A path that is no regular file at commit is ErrNotAFile,
// and a file over limit is a FileTooLargeError, refused before it is read. path is slash-separated and relative to
// the commit's root.
func (g Git) FileAt(ctx context.Context, commit, path string, limit int64) ([]byte, bool, error) {
	g = g.objects()
	format, entry, found, err := g.entryAt(ctx, commit, path)
	switch {
	case err != nil || !found:
		return nil, false, err
	case !strings.HasPrefix(entry.mode, regularMode):
		return nil, true, fmt.Errorf("%w: %s has the mode %s", ErrNotAFile, path, entry.mode)
	}
	raw, err := g.verifiedBlob(ctx, format, entry.oid, limit)
	return raw, true, err
}

// TreeFile is one regular file a commit holds below a directory: its slash-separated path there, and its blob.
type TreeFile struct {
	Path, Blob string
}

// ErrTooManyFiles means a directory holds more regular files than a listing takes.
var ErrTooManyFiles = errors.New("more files than a listing takes")

// FilesUnder lists the regular files commit holds below dir at any depth, checking every tree on the way as FileAt
// checks each object, so BlobOf reads each file's content as the commit holds it. A dir the commit does not hold, or
// holds as no directory, has none. A link and a submodule are left out. A directory of more than limit files is
// ErrTooManyFiles.
func (g Git) FilesUnder(ctx context.Context, commit, dir string, limit int) ([]TreeFile, error) {
	g = g.objects()
	format, entry, found, err := g.entryAt(ctx, commit, dir)
	if err != nil || !found || entry.mode != treeMode {
		return nil, err
	}
	var out []TreeFile
	if err := g.walk(ctx, format, entry.oid, "", limit, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BlobOf reads one blob by its full hash, a TreeFile's, checking that it hashes to that name, and refuses one over
// limit, as a FileTooLargeError, before it is read.
func (g Git) BlobOf(ctx context.Context, oid string, limit int64) ([]byte, error) {
	format, ok := formatOf(oid)
	if !ok {
		return nil, fmt.Errorf("%w, not %q", ErrNotAFullHash, oid)
	}
	return g.objects().verifiedBlob(ctx, format, oid, limit)
}

// objects is g reading the objects a commit names and not the ones a replace ref names in their place.
func (g Git) objects() Git {
	g.Extra = append(slices.Clone(g.Extra), noReplaceObjects)
	return g
}

// entryAt walks path from commit's root tree, checking each object it reads, and gives the entry the last tree holds
// for path's last name. A path whose directory is absent or no directory at commit is not found.
func (g Git) entryAt(ctx context.Context, commit, path string) (objectFormat, treeItem, bool, error) {
	format, ok := formatOf(commit)
	if !ok {
		return objectFormat{}, treeItem{}, false, fmt.Errorf("%w, not %q", ErrNotAFullHash, commit)
	}
	names := strings.Split(path, "/")
	if slices.Contains(names, "") {
		return objectFormat{}, treeItem{}, false, fmt.Errorf("the path %q names no file", path)
	}
	raw, err := g.verified(ctx, format, "commit", commit)
	if err != nil {
		return objectFormat{}, treeItem{}, false, err
	}
	header, _, _ := bytes.Cut(raw, []byte("\n"))
	tree, ok := bytes.CutPrefix(header, []byte("tree "))
	if !ok || !format.names(string(tree)) {
		return objectFormat{}, treeItem{}, false, fmt.Errorf("%w: the commit %s names no tree", ErrObjectMismatch, commit)
	}
	entry := treeItem{mode: treeMode, oid: string(tree)}
	for _, name := range names {
		if entry.mode != treeMode {
			return format, treeItem{}, false, nil
		}
		raw, err := g.verified(ctx, format, "tree", entry.oid)
		if err != nil {
			return objectFormat{}, treeItem{}, false, err
		}
		entries := entriesOf(raw, format.size)
		i := slices.IndexFunc(entries, func(item treeItem) bool { return item.name == name })
		if i < 0 {
			return format, treeItem{}, false, nil
		}
		entry = entries[i]
	}
	return format, entry, true, nil
}

// walk adds the regular files below one tree to out, each by its path under prefix.
func (g Git) walk(ctx context.Context, format objectFormat, tree, prefix string, limit int, out *[]TreeFile) error {
	raw, err := g.verified(ctx, format, "tree", tree)
	if err != nil {
		return err
	}
	for _, entry := range entriesOf(raw, format.size) {
		switch {
		case entry.mode == treeMode:
			if err := g.walk(ctx, format, entry.oid, prefix+entry.name+"/", limit, out); err != nil {
				return err
			}
		case strings.HasPrefix(entry.mode, regularMode):
			if len(*out) == limit {
				return fmt.Errorf("%w: %d", ErrTooManyFiles, limit)
			}
			*out = append(*out, TreeFile{Path: prefix + entry.name, Blob: entry.oid})
		}
	}
	return nil
}

// The modes git records in a tree for a directory and, as their prefix, for a regular file. A link is 120000 and a
// submodule 160000.
const (
	treeMode    = "40000"
	regularMode = "100"
)

// verifiedBlob reads a file's blob, refusing one over limit before it is read.
func (g Git) verifiedBlob(ctx context.Context, format objectFormat, oid string, limit int64) ([]byte, error) {
	text, err := g.Text(ctx, "cat-file", "-s", oid)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("the size of %s reads %q", oid, text)
	}
	if size > limit {
		return nil, &FileTooLargeError{Size: size, Limit: limit}
	}
	return g.verified(ctx, format, "blob", oid)
}

// verified reads one object of a kind and checks that it hashes to oid.
func (g Git) verified(ctx context.Context, format objectFormat, kind, oid string) ([]byte, error) {
	raw, err := g.Output(ctx, "cat-file", kind, oid)
	if err != nil {
		return nil, err
	}
	sum := format.hash()
	fmt.Fprintf(sum, "%s %d\x00", kind, len(raw))
	sum.Write(raw)
	if hex.EncodeToString(sum.Sum(nil)) != oid {
		return nil, fmt.Errorf("%w: the %s %s", ErrObjectMismatch, kind, oid)
	}
	return raw, nil
}

// treeItem is one entry of a tree: its mode, its name, and the object it names.
type treeItem struct {
	mode, name, oid string
}

// entriesOf reads a tree's entries, each a mode, a space, a name, a NUL, and the object's hash in size bytes.
func entriesOf(raw []byte, size int) []treeItem {
	var out []treeItem
	for len(raw) > 0 {
		space := bytes.IndexByte(raw, ' ')
		nul := bytes.IndexByte(raw, 0)
		if space < 0 || nul < space || nul+1+size > len(raw) {
			return out
		}
		out = append(out, treeItem{mode: string(raw[:space]), name: string(raw[space+1 : nul]), oid: hex.EncodeToString(raw[nul+1 : nul+1+size])})
		raw = raw[nul+1+size:]
	}
	return out
}

// objectFormat is how a repository names its objects: SHA-1 or SHA-256 (https://git-scm.com/docs/hash-function-transition).
type objectFormat struct {
	hash func() hash.Hash
	size int
}

// formatOf is the object format whose full hash commit is, read from its length.
func formatOf(commit string) (objectFormat, bool) {
	var format objectFormat
	switch len(commit) {
	case 2 * sha1.Size:
		format = objectFormat{hash: sha1.New, size: sha1.Size}
	case 2 * sha256.Size:
		format = objectFormat{hash: sha256.New, size: sha256.Size}
	default:
		return objectFormat{}, false
	}
	return format, format.names(commit)
}

// names says text is the full lowercase hexadecimal hash of an object in this format.
func (f objectFormat) names(text string) bool {
	return len(text) == 2*f.size && strings.Trim(text, "0123456789abcdef") == ""
}
