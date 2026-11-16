package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"ropeshift.com/ropeshift/internal/delivery"
)

// DeliveryPath is look back's read (FR-EST-5).
const DeliveryPath = "/v1/measures/delivery"

// DefaultDeliveryWindow is the window look back covers when a read names none.
const DefaultDeliveryWindow = "30d"

// CalibrationPath is estimate calibration's read (FR-EST-11).
const CalibrationPath = "/v1/measures/estimates"

// DefaultCalibrationWindow is the recent window calibration covers when a read names none, beside all time.
const DefaultCalibrationWindow = "30d"

// DeliveryGroupings are the groupings look back reads by, the default first.
var DeliveryGroupings = []string{delivery.ByKind, delivery.BySize, delivery.ByAgent, delivery.ByRepository}

// WorkTimeline is where one work order's time went, and its delivery record once a change or a rework ended.
type WorkTimeline struct {
	Timeline delivery.Timeline `json:"timeline"`
	Record   *delivery.Record  `json:"record,omitempty"`
}

// WorkTimelinePath is one work order's timeline.
func WorkTimelinePath(id string) string { return "/v1/work-orders/" + url.PathEscape(id) + "/timeline" }

// WorkEstimate is one work order's current estimate, or none when no estimate was made for it, and its baseline,
// or none before one is recorded.
type WorkEstimate struct {
	Estimate *delivery.Estimate `json:"estimate"`
	Baseline *WorkBaseline      `json:"baseline"`
}

// WorkBaseline is the estimate a work order's baseline holds, which its finished work is measured against
// (FR-EST-10): the first baseline recorded whose set estimated it, which a plan approval recorded as it showed it,
// or which the work recorded when it was made outside a plan, with no approval.
type WorkBaseline struct {
	ID         string            `json:"id"`
	Approval   string            `json:"approval_id,omitempty"`
	RecordedAt int64             `json:"recorded_at"`
	Estimate   delivery.Estimate `json:"estimate"`
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

// PlanForecast is the latest forecasts the forecast tick kept for one plan version, the plan's own first and then
// each milestone's by ID, with the estimate set they were made from. State says whether the tick has forecast the
// version; until it has, Forecasts is empty and Set absent.
type PlanForecast struct {
	Plan      string                `json:"plan_id"`
	Version   int64                 `json:"plan_version"`
	State     string                `json:"state"`
	Set       *delivery.EstimateSet `json:"estimate_set"`
	Forecasts []delivery.Forecast   `json:"forecasts"`
}

// The states of a plan version's forecast (FR-EST-10).
const (
	// ForecastPending is a version the forecast tick has not forecast yet, which every surface reads as "forecast
	// pending". The tick forecasts a version intake recorded within one tick, and never on a request.
	ForecastPending = "pending"
	// ForecastMade is a version the tick has forecast: Forecasts holds the latest it kept.
	ForecastMade = "forecast"
	// ForecastSuperseded is a version a newer one replaced before the tick forecast it. The tick forecasts only each
	// plan's newest version, so it never will.
	ForecastSuperseded = "superseded"
)

// PlanForecastPath is one plan version's forecast, the newest version's when version is zero.
func PlanForecastPath(plan string, version int64) string {
	path := "/v1/plans/" + url.PathEscape(plan) + "/forecast"
	if version != 0 {
		path += "?version=" + strconv.FormatInt(version, 10)
	}
	return path
}

// PlanForecast reads one plan version's latest forecasts, the newest version's when version is zero (FR-EST-8,
// FR-EST-14).
func (c *ProjectClient) PlanForecast(ctx context.Context, plan string, version int64) (PlanForecast, error) {
	var out PlanForecast
	err := c.do(ctx, http.MethodGet, PlanForecastPath(plan, version), nil, &out)
	return out, err
}

// EstimateCalibration reads how often finished work landed inside its baseline estimates, over a recent window and
// over all time (FR-EST-11).
func (c *ProjectClient) EstimateCalibration(ctx context.Context, window string) (delivery.Calibration, error) {
	var out delivery.Calibration
	path := CalibrationPath
	if window != "" {
		path += "?" + url.Values{"window": {window}}.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
