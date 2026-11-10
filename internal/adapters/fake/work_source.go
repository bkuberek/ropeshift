package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// sourceAdapter and sourceInstance name the fake in external keys.
const (
	sourceAdapter  = "fake"
	sourceInstance = "memory"
)

// WorkSource is a work source held in memory. Writing a file stands for a commit.
type WorkSource struct {
	mu       sync.Mutex
	files    map[string][]byte
	revision int
	health   ports.SourceHealth
	// line names a fork's commits, and held is every digest this line's history has seen.
	line    string
	held    map[string]bool
	history *sourceHistory
	// remote and branch are what Describe says of the repository, empty for a source that has none.
	remote, branch string
}

// sourceHistory is what a source and its forks share, as a repository's objects are shared.
type sourceHistory struct {
	mu    sync.Mutex
	forks int
	// held is what each commit's history had seen, and files the digest of each version read.
	held  map[string]map[string]bool
	files map[string]string
}

// NewWorkSource returns an empty source.
func NewWorkSource() *WorkSource {
	return &WorkSource{
		files: map[string][]byte{}, health: ports.SourceHealth{State: planning.HealthOK}, held: map[string]bool{},
		history: &sourceHistory{held: map[string]map[string]bool{}, files: map[string]string{}},
	}
}

func fileDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return planning.DigestSHA256 + hex.EncodeToString(sum[:])
}

func (s *WorkSource) commit() string { return s.line + strconv.Itoa(s.revision) }

// commitOf reads the commit a version names.
func commitOf(source string) string {
	return source[strings.LastIndexByte(source, '@')+1:]
}

// committed moves this line on and keeps what its history has seen. The caller holds mu.
func (s *WorkSource) committed() {
	s.revision++
	for _, content := range s.files {
		s.held[fileDigest(content)] = true
	}
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	s.history.held[s.commit()] = maps.Clone(s.held)
}

// Write stands for committing one file.
func (s *WorkSource) Write(name string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = slices.Clone(content)
	s.committed()
}

// Fork stands for a branch cut here: it holds this history, and what either side writes next the
// other does not hold.
func (s *WorkSource) Fork() *WorkSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	s.history.forks++
	fork := &WorkSource{
		files: maps.Clone(s.files), revision: s.revision, health: s.health, held: maps.Clone(s.held), history: s.history,
		line: fmt.Sprintf("f%d.", s.history.forks),
	}
	s.history.held[fork.commit()] = maps.Clone(fork.held)
	return fork
}

// Merge stands for merging a branch and committing one file as a person resolved it, so this line's
// history holds what the branch's did.
func (s *WorkSource) Merge(branch *WorkSource, name string, content []byte) {
	branch.mu.Lock()
	seen := maps.Clone(branch.held)
	branch.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	maps.Copy(s.held, seen)
	s.files[name] = slices.Clone(content)
	s.committed()
}

// Rename stands for moving one file.
func (s *WorkSource) Rename(from, to string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[to] = s.files[from]
	delete(s.files, from)
	s.committed()
}

// source names one file at this commit, and keeps what it held there for Relate to read. The caller
// holds mu.
func (s *WorkSource) source(name string) string {
	source := name + "@" + s.commit()
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	s.history.files[source] = fileDigest(s.files[name])
	return source
}

// SetHealth sets what Health reports.
func (s *WorkSource) SetHealth(health ports.SourceHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = health
}

// SetRepository sets the remote and the default branch Describe reports, as a source on a git
// repository does. A source with neither says nothing of a repository.
func (s *WorkSource) SetRepository(remote, defaultBranch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remote, s.branch = remote, defaultBranch
}

// Describe names the fake.
func (s *WorkSource) Describe(context.Context) (ports.SourceDescription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ports.SourceDescription{
		Adapter: sourceAdapter, Instance: sourceInstance, APIVersions: []string{"fake"}, BudgetModel: "none",
		Remote: s.remote, DefaultBranch: s.branch,
	}, nil
}

// Fields returns the required keys of the format.
func (s *WorkSource) Fields(context.Context, ports.ScopeRef) ([]ports.FieldSchema, error) {
	return []ports.FieldSchema{
		{Name: "kind", Type: "string", Required: true},
		{Name: "id", Type: "string", Required: true},
		{Name: "title", Type: "string", Required: true},
	}, nil
}

// Poll returns every file when the revision moved.
func (s *WorkSource) Poll(_ context.Context, cursor ports.Cursor) (ports.ChangeBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := ports.Cursor(strconv.Itoa(s.revision))
	if current == cursor {
		return ports.ChangeBatch{Cursor: cursor}, nil
	}
	batch := ports.ChangeBatch{Cursor: current}
	names := make([]string, 0, len(s.files))
	for name := range s.files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		doc, err := repofiles.ParseFile(s.files[name])
		switch {
		case errors.Is(err, planning.ErrNotOurs):
			continue
		case err != nil:
			batch.Problems = append(batch.Problems, planning.SourceProblem{Path: name, Detail: err.Error(), Source: s.source(name)})
			continue
		case doc.Contract == nil:
			if record, ok := ports.RecordOf(doc, sourceAdapter, sourceInstance, name, s.source(name), fileDigest(s.files[name])); ok {
				batch.Records = append(batch.Records, record)
			}
			continue
		}
		batch.Items = append(batch.Items, s.item(name, doc))
	}
	ports.OrderRecords(batch.Records)
	return batch, nil
}

func (s *WorkSource) item(name string, doc planning.Document) ports.WorkItem {
	return ports.WorkItem{
		Key:       ledger.ExternalKey{Adapter: sourceAdapter, Instance: sourceInstance, NativeID: doc.Contract.ID},
		Digest:    fileDigest(s.files[name]),
		Source:    s.source(name),
		Path:      name,
		Contract:  *doc.Contract,
		Statement: doc.Statement,
	}
}

// Read returns one item as it is now.
func (s *WorkSource) Read(ctx context.Context, key ledger.ExternalKey) (ports.WorkItem, error) {
	batch, err := s.Poll(ctx, "")
	if err != nil {
		return ports.WorkItem{}, err
	}
	for _, item := range batch.Items {
		if item.Key == key {
			return item, nil
		}
	}
	return ports.WorkItem{}, fmt.Errorf("%s: %w", key, ports.ErrNotFound)
}

// Relate reads what each version's commit had seen, as the repository source reads its history. A
// version from any other source is one this history has no word on.
func (s *WorkSource) Relate(_ context.Context, read, recorded string) (ports.Lineage, error) {
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	readDigest, known := s.history.files[read]
	recordedDigest, knownToo := s.history.files[recorded]
	switch {
	case !known || !knownToo, s.history.held[commitOf(read)][recordedDigest]:
		return ports.LineageAhead, nil
	case s.history.held[commitOf(recorded)][readDigest]:
		return ports.LineageBehind, nil
	}
	return ports.LineageDiverged, nil
}

// VerifyDelivery reports that the fake takes no webhooks.
func (s *WorkSource) VerifyDelivery(context.Context, ports.InboundDelivery) (ports.ChangeHint, error) {
	return ports.ChangeHint{}, ports.ErrUnsupported
}

// Apply reports `unsupported`, as the repository source does.
func (s *WorkSource) Apply(context.Context, ports.OutboxItem) (ports.WriteResult, error) {
	return ports.WriteResult{Status: ports.WriteUnsupported}, nil
}

// Health returns what SetHealth set.
func (s *WorkSource) Health(context.Context) (ports.SourceHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health, nil
}

var _ ports.WorkSource = (*WorkSource)(nil)
