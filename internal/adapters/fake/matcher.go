package fake

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

type matcherEntry struct {
	scope ledger.WorkspaceID
	kind  string
	id    string
	terms []string
}

// Matcher is a Matcher held in memory. It scores a record by the terms it shares with the
// question, which is enough to order candidates in a test.
type Matcher struct {
	mu      sync.Mutex
	entries []matcherEntry
}

// Add holds one record for matching.
func (m *Matcher) Add(scope ledger.WorkspaceID, kind, id, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, matcherEntry{scope: scope, kind: kind, id: id, terms: questions.Terms(text)})
}

// Candidates returns the records that share a term, best first.
func (m *Matcher) Candidates(_ context.Context, scope ledger.WorkspaceID, request ports.MatchRequest) ([]ports.Candidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	asked := questions.Terms(request.Text)
	out := []ports.Candidate{}
	for _, entry := range m.entries {
		if entry.scope != scope || !slices.Contains(request.Kinds, entry.kind) {
			continue
		}
		shared := 0
		for _, term := range asked {
			if slices.Contains(entry.terms, term) {
				shared++
			}
		}
		if shared > 0 {
			out = append(out, ports.Candidate{Kind: entry.kind, RecordID: entry.id, Score: float64(shared)})
		}
	}
	slices.SortStableFunc(out, func(a, b ports.Candidate) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), strings.Compare(a.RecordID, b.RecordID))
	})
	if request.Limit > 0 && len(out) > request.Limit {
		out = out[:request.Limit]
	}
	return out, nil
}

var _ ports.Matcher = (*Matcher)(nil)
