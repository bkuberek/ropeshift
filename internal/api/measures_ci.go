package api

import (
	"context"
	"net/http"
)

// CIDurationPath is what each repository's CI takes (FR-SPEND-21).
const CIDurationPath = "/v1/measures/ci"

// CIDuration is what one repository's CI takes: the p50 and p85 of the push-to-completed time and of the queue time
// of its most recent heads with a test result. A repository with fewer such heads than ci_history_min_records has no
// history, and the schedule and the limits then read the no-history keys, which the row names.
type CIDuration struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	// Records is how many recent heads with a test result the figures cover.
	Records    int64 `json:"records"`
	MinRecords int64 `json:"min_records"`
	History    bool  `json:"history"`
	P50Ms      int64 `json:"p50_ms,omitempty"`
	P85Ms      int64 `json:"p85_ms,omitempty"`
	QueueP50Ms int64 `json:"queue_p50_ms,omitempty"`
	QueueP85Ms int64 `json:"queue_p85_ms,omitempty"`
	// NoHistoryP50Ms and NoHistoryP85Ms are the expected time the keys give while the repository has no history.
	NoHistoryP50Ms int64 `json:"no_history_p50_ms"`
	NoHistoryP85Ms int64 `json:"no_history_p85_ms"`
}

// CIDurations is each repository's CI duration, by name.
type CIDurations struct {
	Repositories []CIDuration `json:"repositories"`
}

// CIDurations reads what each repository's CI takes.
func (c *ProjectClient) CIDurations(ctx context.Context) (CIDurations, error) {
	var out CIDurations
	err := c.do(ctx, http.MethodGet, CIDurationPath, nil, &out)
	return out, err
}
