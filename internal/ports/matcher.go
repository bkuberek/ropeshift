package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Candidate is one record a question may match. An agent judges whether it does.
type Candidate struct {
	Kind     string
	RecordID string
	Score    float64
}

// MatchRequest asks for candidates of some kinds of record.
type MatchRequest struct {
	Text  string
	Kinds []string
	Limit int
}

// Matcher produces candidates for the question ladder, best first. It decides nothing:
// the core gives no score a meaning, so a wrong match stays an agent's recorded judgment
// (ADR-0007).
type Matcher interface {
	Candidates(ctx context.Context, scope ledger.WorkspaceID, request MatchRequest) ([]Candidate, error)
}
