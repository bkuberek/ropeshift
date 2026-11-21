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
	format, ok := formatOf(commit)
	if !ok {
		return nil, false, fmt.Errorf("%w, not %q", ErrNotAFullHash, commit)
	}
	names := strings.Split(path, "/")
	if slices.Contains(names, "") {
		return nil, false, fmt.Errorf("the path %q names no file", path)
	}
	g.Extra = append(slices.Clone(g.Extra), noReplaceObjects)
	raw, err := g.verified(ctx, format, "commit", commit)
	if err != nil {
		return nil, false, err
	}
	header, _, _ := bytes.Cut(raw, []byte("\n"))
	tree, ok := bytes.CutPrefix(header, []byte("tree "))
	if !ok || !format.names(string(tree)) {
		return nil, false, fmt.Errorf("%w: the commit %s names no tree", ErrObjectMismatch, commit)
	}
	oid := string(tree)
	for i, name := range names {
		raw, err := g.verified(ctx, format, "tree", oid)
		if err != nil {
			return nil, false, err
		}
		mode, entry, found := entryIn(raw, name, format.size)
		switch {
		case !found:
			return nil, false, nil
		case i < len(names)-1 && mode != treeMode:
			return nil, false, nil
		case i < len(names)-1:
			oid = entry
		case !strings.HasPrefix(mode, regularMode):
			return nil, true, fmt.Errorf("%w: %s has the mode %s", ErrNotAFile, path, mode)
		default:
			return g.verifiedBlob(ctx, format, entry, limit)
		}
	}
	return nil, false, nil
}

// The modes git records in a tree for a directory and, as their prefix, for a regular file. A link is 120000 and a
// submodule 160000.
const (
	treeMode    = "40000"
	regularMode = "100"
)

// verifiedBlob reads a file's blob, refusing one over limit before it is read.
func (g Git) verifiedBlob(ctx context.Context, format objectFormat, oid string, limit int64) ([]byte, bool, error) {
	text, err := g.Text(ctx, "cat-file", "-s", oid)
	if err != nil {
		return nil, true, err
	}
	size, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, true, fmt.Errorf("the size of %s reads %q", oid, text)
	}
	if size > limit {
		return nil, true, &FileTooLargeError{Size: size, Limit: limit}
	}
	raw, err := g.verified(ctx, format, "blob", oid)
	if err != nil {
		return nil, true, err
	}
	return raw, true, nil
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

// entryIn finds name among a tree's entries, each a mode, a space, a name, a NUL, and the object's hash in size
// bytes, and gives its mode and the object it names.
func entryIn(raw []byte, name string, size int) (mode, oid string, found bool) {
	for len(raw) > 0 {
		space := bytes.IndexByte(raw, ' ')
		nul := bytes.IndexByte(raw, 0)
		if space < 0 || nul < space || nul+1+size > len(raw) {
			return "", "", false
		}
		if string(raw[space+1:nul]) == name {
			return string(raw[:space]), hex.EncodeToString(raw[nul+1 : nul+1+size]), true
		}
		raw = raw[nul+1+size:]
	}
	return "", "", false
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
