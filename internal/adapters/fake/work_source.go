package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"ropeshift.dev/ropeshift/internal/adapters/repofiles"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
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
}

// NewWorkSource returns an empty source.
func NewWorkSource() *WorkSource {
	return &WorkSource{files: map[string][]byte{}, health: ports.SourceHealth{State: planning.HealthOK}}
}

// Write stands for committing one file.
func (s *WorkSource) Write(name string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = slices.Clone(content)
	s.revision++
}

// Rename stands for moving one file.
func (s *WorkSource) Rename(from, to string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[to] = s.files[from]
	delete(s.files, from)
	s.revision++
}

// SetHealth sets what Health reports.
func (s *WorkSource) SetHealth(health ports.SourceHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = health
}

// Describe names the fake.
func (s *WorkSource) Describe(context.Context) (ports.SourceDescription, error) {
	return ports.SourceDescription{Adapter: sourceAdapter, Instance: sourceInstance, APIVersions: []string{"fake"}, BudgetModel: "none"}, nil
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
			batch.Problems = append(batch.Problems, planning.SourceProblem{Path: name, Detail: err.Error()})
			continue
		case doc.Contract == nil:
			continue
		}
		batch.Items = append(batch.Items, s.item(name, doc))
	}
	return batch, nil
}

func (s *WorkSource) item(name string, doc planning.Document) ports.WorkItem {
	sum := sha256.Sum256(s.files[name])
	return ports.WorkItem{
		Key:       ledger.ExternalKey{Adapter: sourceAdapter, Instance: sourceInstance, NativeID: doc.Contract.ID},
		Digest:    planning.DigestSHA256 + hex.EncodeToString(sum[:]),
		Source:    fmt.Sprintf("%s@%d", name, s.revision),
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
