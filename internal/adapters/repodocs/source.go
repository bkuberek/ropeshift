// Package repodocs is the KnowledgeSource on repository documents: Markdown files at a
// committed ref, versioned by the commits that changed them.
package repodocs

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// Adapter is the first part of every key this source returns.
const Adapter = "repodocs"

// The lengths of a full commit name, on SHA-1 and on SHA-256.
const (
	sha1Name   = 40
	sha256Name = 64
)

// Source reads documents at the same ref intake reads, so what an agent reads is what the
// repository committed.
type Source struct {
	files *repofiles.Source
	git   gitcli.Git
}

// New returns a source on one repository.
func New(config repofiles.Config) *Source {
	return &Source{files: repofiles.New(config), git: gitcli.Git{Dir: config.Repository, Env: config.Env}}
}

// Describe names the repository by its history, as intake does.
func (s *Source) Describe(ctx context.Context) (ports.SourceDescription, error) {
	description, err := s.files.Describe(ctx)
	if err != nil {
		return ports.SourceDescription{}, err
	}
	description.Adapter = Adapter
	return description, nil
}

func (s *Source) key(ctx context.Context, path string) (ledger.ExternalKey, error) {
	description, err := s.Describe(ctx)
	if err != nil {
		return ledger.ExternalKey{}, err
	}
	return ledger.ExternalKey{Adapter: description.Adapter, Instance: description.Instance, NativeID: path}, nil
}

// document reads one file of one commit.
func (s *Source) document(ctx context.Context, key ledger.ExternalKey, blob repofiles.Blob, commit string) (ports.KnowledgeDocument, error) {
	raw, err := s.git.Output(ctx, "cat-file", "blob", blob.Hash)
	if err != nil {
		return ports.KnowledgeDocument{}, err
	}
	return ports.KnowledgeDocument{
		Key: key, Title: title(blob.Path, raw), Kind: kindOf(blob.Path, raw), Version: commit, Digest: planning.DigestGitBlob + blob.Hash,
		Source: blob.Path + "@" + commit, Body: string(raw),
	}, nil
}

// kindOf takes a repository file's own word for what it is, and reads any other document's kind from
// its path. A plan, a milestone, or a contract is a record first, so it is no document a person looks
// for by kind.
func kindOf(path string, raw []byte) planning.DocumentKind {
	doc, err := repofiles.ParseFile(raw)
	switch {
	case err != nil:
		return planning.KindOfPath(path)
	case doc.Requirements != nil:
		return planning.DocumentRequirements
	case doc.Spec != nil:
		return planning.DocumentSpec
	case doc.Architecture != nil:
		return planning.DocumentArchitecture
	case doc.Experience != nil:
		return planning.DocumentExperienceDesign
	}
	return planning.DocumentOther
}

// title reads a document's first heading, or else its front matter's title, or else
// names its file.
func title(path string, raw []byte) string {
	body := raw
	if doc, err := repofiles.ParseFile(raw); err == nil {
		switch {
		case doc.Contract != nil:
			return doc.Contract.Title
		case doc.Goal != nil:
			return doc.Goal.Title
		case doc.Plan != nil:
			return doc.Plan.Title
		}
		if head, ok := doc.Head(); ok && head.Title != "" {
			return head.Title
		}
		body = []byte(doc.Statement)
	}
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		if heading, ok := bytes.CutPrefix(line, []byte("# ")); ok {
			return strings.TrimSpace(string(heading))
		}
	}
	return path
}

// List returns every Markdown file at the ref, with no body.
func (s *Source) List(ctx context.Context) ([]ports.KnowledgeDocument, error) {
	commit, err := s.files.Head(ctx)
	if err != nil {
		return nil, err
	}
	blobs, err := s.files.Tree(ctx, commit)
	if err != nil {
		return nil, err
	}
	out := make([]ports.KnowledgeDocument, 0, len(blobs))
	for _, blob := range blobs {
		key, err := s.key(ctx, blob.Path)
		if err != nil {
			return nil, err
		}
		document, err := s.document(ctx, key, blob, commit)
		if err != nil {
			return nil, err
		}
		document.Body = ""
		out = append(out, document)
	}
	return out, nil
}

// find locates a path in a commit, by listing the commit, so a name that means something to
// git as a pathspec means nothing here.
func (s *Source) find(ctx context.Context, commit, path string) (repofiles.Blob, bool, error) {
	blobs, err := s.files.Tree(ctx, commit)
	if err != nil {
		return repofiles.Blob{}, false, err
	}
	for _, blob := range blobs {
		if blob.Path == path {
			return blob, true, nil
		}
	}
	return repofiles.Blob{}, false, nil
}

// Read returns one document. A version must be a commit this repository holds.
func (s *Source) Read(ctx context.Context, key ledger.ExternalKey, version string) (ports.KnowledgeDocument, error) {
	notFound := fmt.Errorf("document %s at %q: %w", key, version, ports.ErrNotFound)
	if own, err := s.key(ctx, key.NativeID); err != nil {
		return ports.KnowledgeDocument{}, err
	} else if own != key {
		return ports.KnowledgeDocument{}, notFound
	}
	commit, err := s.commitOf(ctx, version)
	if err != nil {
		return ports.KnowledgeDocument{}, err
	}
	if commit == "" {
		return ports.KnowledgeDocument{}, notFound
	}
	blob, found, err := s.find(ctx, commit, key.NativeID)
	if err != nil {
		return ports.KnowledgeDocument{}, err
	}
	if !found {
		return ports.KnowledgeDocument{}, notFound
	}
	return s.document(ctx, key, blob, commit)
}

// commitOf resolves a version, and returns "" for one this repository does not hold.
func (s *Source) commitOf(ctx context.Context, version string) (string, error) {
	if version == "" {
		return s.files.Head(ctx)
	}
	if !isCommitName(version) {
		return "", nil
	}
	commit, err := s.git.Commit(ctx, version)
	if err != nil {
		return "", nil
	}
	return commit, nil
}

// isCommitName accepts a full hash only, so a version can never be a ref or an option.
func isCommitName(version string) bool {
	if len(version) != sha1Name && len(version) != sha256Name {
		return false
	}
	_, err := hex.DecodeString(version)
	return err == nil
}

// Versions lists the commits that changed a document, newest first. A commit that removed
// it has no version to read, so it is left out.
func (s *Source) Versions(ctx context.Context, key ledger.ExternalKey) ([]ports.KnowledgeVersion, error) {
	head, err := s.files.Head(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.git.Text(ctx, "--literal-pathspecs", "log", "--format=%H", head, "--", key.NativeID)
	if err != nil {
		return nil, err
	}
	var versions []ports.KnowledgeVersion
	for _, commit := range strings.Fields(out) {
		blob, found, err := s.find(ctx, commit, key.NativeID)
		if err != nil {
			return nil, err
		}
		if found {
			versions = append(versions, ports.KnowledgeVersion{Version: commit, Digest: planning.DigestGitBlob + blob.Hash})
		}
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("document %s: %w", key, ports.ErrNotFound)
	}
	return versions, nil
}

// Health reports the last fetch.
func (s *Source) Health(ctx context.Context) (ports.SourceHealth, error) {
	return s.files.Health(ctx)
}

var _ ports.KnowledgeSource = (*Source)(nil)
