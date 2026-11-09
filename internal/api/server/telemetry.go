package server

import (
	"net/http"
	"strconv"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ports"
)

// maxTelemetryRecords bounds one read.
const maxTelemetryRecords = 10000

func (h *handler) telemetryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/telemetry/records", h.telemetryRecords)
	mux.HandleFunc("GET /v1/telemetry/lanes", h.laneTelemetry)
}

func (h *handler) telemetryRecords(w http.ResponseWriter, r *http.Request) {
	query := ports.TelemetryQuery{
		Signal: r.URL.Query().Get("signal"), Name: r.URL.Query().Get("name"), Lane: r.URL.Query().Get("lane"),
	}
	for key, into := range map[string]*int64{"since": &query.Since} {
		if text := r.URL.Query().Get(key); text != "" {
			n, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: key + " is not a number"})
				return
			}
			*into = n
		}
	}
	if text := r.URL.Query().Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > maxTelemetryRecords {
			refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: "limit is a number up to " + strconv.Itoa(maxTelemetryRecords)})
			return
		}
		query.Limit = n
	}
	out := api.TelemetryRecords{Available: h.Telemetry != nil, Records: []api.TelemetryRecord{}}
	if h.Telemetry == nil {
		reply(w, http.StatusOK, out)
		return
	}
	records, err := h.Telemetry.Records(r.Context(), query)
	if err != nil {
		fail(w, err)
		return
	}
	for _, record := range records {
		out.Records = append(out.Records, api.TelemetryRecord{
			ID: record.ID, Signal: record.Signal, Name: record.Name, Lane: record.Lane, WorkOrder: record.WorkOrder,
			Generation: record.Generation, ObservedAt: record.ObservedAt, ReceivedAt: record.ReceivedAt, Value: record.Value,
			Attributes: record.Attributes, Detail: record.Detail,
		})
	}
	reply(w, http.StatusOK, out)
}

func (h *handler) laneTelemetry(w http.ResponseWriter, r *http.Request) {
	out := api.LaneTelemetryList{Available: h.Telemetry != nil, Lanes: []api.LaneTelemetry{}}
	if h.Telemetry == nil {
		reply(w, http.StatusOK, out)
		return
	}
	lanes, err := h.Telemetry.Lanes(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	for _, lane := range lanes {
		out.Lanes = append(out.Lanes, api.LaneTelemetry{
			Lane: lane.Lane, ContextTokens: lane.ContextTokens, ContextAt: lane.ContextAt, Tokens: lane.Tokens, CostUSD: lane.CostUSD, LastAt: lane.LastAt,
		})
	}
	reply(w, http.StatusOK, out)
}
