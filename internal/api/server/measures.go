package server

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
)

func (h *handler) measureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/measures", h.readMeasures)
	mux.HandleFunc("GET /v1/measures/delivery", h.readDelivery)
	mux.HandleFunc("GET /v1/work-orders/{id}/timeline", h.readTimeline)
}

// readDelivery reads look back over one window from the delivery records the fold keeps (FR-EST-5).
func (h *handler) readDelivery(w http.ResponseWriter, r *http.Request) {
	length, err := api.ParseWindow(cmp.Or(r.URL.Query().Get("window"), api.DefaultDeliveryWindow))
	by := cmp.Or(r.URL.Query().Get("by"), delivery.ByKind)
	if err == nil && !slices.Contains(api.DeliveryGroupings, by) {
		err = fmt.Errorf("by %q is not one of %s", by, strings.Join(api.DeliveryGroupings, ", "))
	}
	if err != nil {
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
		return
	}
	var report delivery.LookBack
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		records, err := tx.Deliveries().Records()
		if err != nil {
			return err
		}
		report = delivery.LookBackOf(records, by, now-length.Milliseconds(), now)
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, report)
}

// readTimeline derives where one work order's time went as the fold would, with the spans it keeps of a full
// work-in-progress cap and the time it first ran (FR-EST-1).
func (h *handler) readTimeline(w http.ResponseWriter, r *http.Request) {
	var view api.WorkTimeline
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		opts := control.DeliveryOptions{Now: now}
		rows := tx.Deliveries()
		state, err := rows.FoldState()
		switch {
		case err == nil:
			full, err := rows.WIPFull()
			if err != nil {
				return err
			}
			opts.WIPFull, opts.CapacitySince = append(full, state.Open(now)...), state.FirstRunAt
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		view.Timeline, view.Record, err = control.DeliveryOf(tx, r.PathValue("id"), opts)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// readMeasures computes the product measures over one window from the ledger, which every surface
// reads the same way (FR-OBS-6).
func (h *handler) readMeasures(w http.ResponseWriter, r *http.Request) {
	name := cmp.Or(r.URL.Query().Get("window"), api.DefaultMeasureWindow)
	length, err := api.ParseWindow(name)
	if err != nil {
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
		return
	}
	var report measures.Report
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		report, err = control.ReportIn(tx, name, length)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, report)
}
