// Package lexical is the Matcher of the default install: lexical candidates from Store
// search, with no model and no network.
package lexical

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Matcher reads candidates through Store search.
type Matcher struct {
	Store ports.Store
}

// Candidates runs one search.
func (m Matcher) Candidates(ctx context.Context, scope ledger.WorkspaceID, request ports.MatchRequest) ([]ports.Candidate, error) {
	var out []ports.Candidate
	err := m.Store.Read(ctx, scope, func(tx ports.ReadTx) error {
		hits, err := tx.Search(ports.SearchQuery(request))
		if err != nil {
			return err
		}
		for _, hit := range hits {
			out = append(out, ports.Candidate(hit))
		}
		return nil
	})
	return out, err
}

var _ ports.Matcher = Matcher{}
