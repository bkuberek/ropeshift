package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// TelemetryRecord is one span, data point, or log record from the worker's local store, with its
// resource's and scope's attributes under its own. Value is a metric's value, a histogram's sum, or
// the context an agent's request log reports.
type TelemetryRecord struct {
	ID         int64          `json:"id"`
	Signal     string         `json:"signal"`
	Name       string         `json:"name"`
	Lane       string         `json:"lane,omitempty"`
	WorkOrder  string         `json:"work_order,omitempty"`
	Generation int64          `json:"generation,omitempty"`
	ObservedAt int64          `json:"observed_at"`
	ReceivedAt int64          `json:"received_at"`
	Value      *float64       `json:"value,omitempty"`
	Attributes map[string]any `json:"attributes"`
	Detail     map[string]any `json:"detail"`
}

// TelemetryRecords is one query's answer. Available says this control plane can read a telemetry
// store at all, which solo mode can and a plane with no local worker cannot.
type TelemetryRecords struct {
	Available bool              `json:"available"`
	Records   []TelemetryRecord `json:"records"`
}

// TelemetryQuery selects records. Empty fields select everything, and a zero Limit takes the store's
// default.
type TelemetryQuery struct {
	Signal string
	Name   string
	Lane   string
	Since  int64
	Limit  int
}

// Values writes the query as URL parameters.
func (q TelemetryQuery) Values() url.Values {
	values := url.Values{}
	for key, value := range map[string]string{"signal": q.Signal, "name": q.Name, "lane": q.Lane} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if q.Since != 0 {
		values.Set("since", strconv.FormatInt(q.Since, 10))
	}
	if q.Limit != 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	return values
}

// LaneTelemetry is what the local store says of one lane: context at its last call, tokens by type,
// and the cost its agent reported, which is a lower bound.
type LaneTelemetry struct {
	Lane          string           `json:"lane"`
	ContextTokens int64            `json:"context_tokens"`
	ContextAt     int64            `json:"context_at,omitempty"`
	Tokens        map[string]int64 `json:"tokens"`
	CostUSD       float64          `json:"cost_usd"`
	LastAt        int64            `json:"last_at"`
	// Agents are the tokens each roster agent spent inside the lane, by agent and then type, where the
	// driver reported which agent spent them. They are part of Tokens (FR-AGENT-12).
	Agents map[string]map[string]int64 `json:"agents,omitempty"`
}

// LaneTelemetryList is every lane the store holds anything of.
type LaneTelemetryList struct {
	Available bool            `json:"available"`
	Lanes     []LaneTelemetry `json:"lanes"`
}

// TelemetryRecordsPath and TelemetryLanesPath are the telemetry reads.
const (
	TelemetryRecordsPath = "/v1/telemetry/records"
	TelemetryLanesPath   = "/v1/telemetry/lanes"
)

// TelemetryRecords reads the worker's local store.
func (c *Client) TelemetryRecords(ctx context.Context, query TelemetryQuery) (TelemetryRecords, error) {
	var out TelemetryRecords
	path := TelemetryRecordsPath
	if values := query.Values(); len(values) > 0 {
		path += "?" + values.Encode()
	}
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// LaneTelemetry reads each lane's figures from the worker's local store.
func (c *Client) LaneTelemetry(ctx context.Context) (LaneTelemetryList, error) {
	var out LaneTelemetryList
	err := c.Do(ctx, http.MethodGet, TelemetryLanesPath, nil, &out)
	return out, err
}
