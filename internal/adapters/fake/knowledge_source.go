package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
)

// knowledgeAdapter names the fake in document keys.
const knowledgeAdapter = "fake-knowledge"

type documentVersion struct {
	revision int
	body     string
}

// KnowledgeSource is a document store held in memory. Writing a document stands for a
// commit that changes it.
type KnowledgeSource struct {
	mu        sync.Mutex
	documents map[string][]documentVersion
	revision  int
}

// NewKnowledgeSource returns an empty source.
func NewKnowledgeSource() *KnowledgeSource {
	return &KnowledgeSource{documents: map[string][]documentVersion{}}
}

// Write stands for committing one document.
func (s *KnowledgeSource) Write(path, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	s.documents[path] = append(s.documents[path], documentVersion{revision: s.revision, body: body})
}

// Describe names the fake.
func (s *KnowledgeSource) Describe(context.Context) (ports.SourceDescription, error) {
	return ports.SourceDescription{Adapter: knowledgeAdapter, Instance: sourceInstance, APIVersions: []string{"fake"}, BudgetModel: "none"}, nil
}

func (s *KnowledgeSource) document(path string, at documentVersion) ports.KnowledgeDocument {
	version := strconv.Itoa(at.revision)
	return ports.KnowledgeDocument{
		Key:   ledger.ExternalKey{Adapter: knowledgeAdapter, Instance: sourceInstance, NativeID: path},
		Title: documentTitle(path, at.body), Version: version, Digest: documentDigest(at.body),
		Source: path + "@" + version, Body: at.body,
	}
}

func documentDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return planning.DigestSHA256 + hex.EncodeToString(sum[:])
}

func documentTitle(path, body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if title, ok := strings.CutPrefix(line, "# "); ok {
			return title
		}
	}
	return path
}

// List returns every document at its last version, with no body.
func (s *KnowledgeSource) List(context.Context) ([]ports.KnowledgeDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make([]string, 0, len(s.documents))
	for path := range s.documents {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	out := make([]ports.KnowledgeDocument, 0, len(paths))
	for _, path := range paths {
		versions := s.documents[path]
		document := s.document(path, versions[len(versions)-1])
		document.Body = ""
		out = append(out, document)
	}
	return out, nil
}

// Read returns one document.
func (s *KnowledgeSource) Read(_ context.Context, key ledger.ExternalKey, version string) (ports.KnowledgeDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.documents[key.NativeID]
	if key.Adapter != knowledgeAdapter || key.Instance != sourceInstance || len(versions) == 0 {
		return ports.KnowledgeDocument{}, fmt.Errorf("document %s: %w", key, ports.ErrNotFound)
	}
	if version == "" {
		return s.document(key.NativeID, versions[len(versions)-1]), nil
	}
	for _, candidate := range versions {
		if strconv.Itoa(candidate.revision) == version {
			return s.document(key.NativeID, candidate), nil
		}
	}
	return ports.KnowledgeDocument{}, fmt.Errorf("document %s at %s: %w", key, version, ports.ErrNotFound)
}

// Versions lists one document's versions, newest first.
func (s *KnowledgeSource) Versions(_ context.Context, key ledger.ExternalKey) ([]ports.KnowledgeVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.documents[key.NativeID]
	if key.Adapter != knowledgeAdapter || key.Instance != sourceInstance || len(versions) == 0 {
		return nil, fmt.Errorf("document %s: %w", key, ports.ErrNotFound)
	}
	out := make([]ports.KnowledgeVersion, 0, len(versions))
	for _, candidate := range slices.Backward(versions) {
		out = append(out, ports.KnowledgeVersion{Version: strconv.Itoa(candidate.revision), Digest: documentDigest(candidate.body)})
	}
	return out, nil
}

// Health reports a healthy source.
func (s *KnowledgeSource) Health(context.Context) (ports.SourceHealth, error) {
	return ports.SourceHealth{State: planning.HealthOK}, nil
}

var _ ports.KnowledgeSource = (*KnowledgeSource)(nil)
