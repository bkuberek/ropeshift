package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/delivery"
)

// DeliveryPath is look back's read (FR-EST-5).
const DeliveryPath = "/v1/measures/delivery"

// DefaultDeliveryWindow is the window look back covers when a read names none.
const DefaultDeliveryWindow = "30d"

// DeliveryGroupings are the groupings look back reads by, the default first.
var DeliveryGroupings = []string{delivery.ByKind, delivery.BySize, delivery.ByAgent, delivery.ByRepository}

// WorkTimeline is where one work order's time went, and its delivery record once a change or a rework ended.
type WorkTimeline struct {
	Timeline delivery.Timeline `json:"timeline"`
	Record   *delivery.Record  `json:"record,omitempty"`
}

// WorkTimelinePath is one work order's timeline.
func WorkTimelinePath(id string) string { return "/v1/work-orders/" + url.PathEscape(id) + "/timeline" }

// WorkEstimate is one work order's current estimate, or none when no estimate was made for it.
type WorkEstimate struct {
	Estimate *delivery.Estimate `json:"estimate"`
}

// WorkEstimatePath is one work order's current estimate.
func WorkEstimatePath(id string) string { return "/v1/work-orders/" + url.PathEscape(id) + "/estimate" }

// WorkEstimate reads one work order's current estimate (FR-EST-14).
func (c *ProjectClient) WorkEstimate(ctx context.Context, id string) (WorkEstimate, error) {
	var out WorkEstimate
	err := c.do(ctx, http.MethodGet, WorkEstimatePath(id), nil, &out)
	return out, err
}

// WorkTimeline reads where one work order's time went (FR-EST-1).
func (c *ProjectClient) WorkTimeline(ctx context.Context, id string) (WorkTimeline, error) {
	var out WorkTimeline
	err := c.do(ctx, http.MethodGet, WorkTimelinePath(id), nil, &out)
	return out, err
}

// Delivery reads look back over a window, by a grouping (FR-EST-5).
func (c *ProjectClient) Delivery(ctx context.Context, window, by string) (delivery.LookBack, error) {
	var out delivery.LookBack
	query := url.Values{}
	if window != "" {
		query.Set("window", window)
	}
	if by != "" {
		query.Set("by", by)
	}
	path := DeliveryPath
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
