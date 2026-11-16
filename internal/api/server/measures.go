package server

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

func (h *handler) measureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/measures", h.readMeasures)
	mux.HandleFunc("GET /v1/measures/delivery", h.readDelivery)
	mux.HandleFunc("GET /v1/measures/estimates", h.readCalibration)
	mux.HandleFunc("GET /v1/measures/history", h.readHistory)
	mux.HandleFunc("GET /v1/work-orders/{id}/timeline", h.readTimeline)
	mux.HandleFunc("GET /v1/work-orders/{id}/estimate", h.readEstimate)
	mux.HandleFunc("GET /v1/plans/{id}/forecast", h.readForecast)
}

// readForecast reads the latest forecasts the forecast tick kept for a plan's newest version, or for the version
// asked for, with the estimate set they were made from (FR-EST-8, FR-EST-14). It never forecasts: a large plan's
// simulation takes seconds, so only the tick runs it.
func (h *handler) readForecast(w http.ResponseWriter, r *http.Request) {
	var wanted int64
	if raw := r.URL.Query().Get("version"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			invalid(w, "a version is a whole number from 1")
			return
		}
		wanted = parsed
	}
	var view api.PlanForecast
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		plan, err := tx.Planning().Plan(r.PathValue("id"))
		if err != nil {
			return err
		}
		var version planning.PlanVersion
		if wanted == 0 {
			version, err = tx.Planning().LatestPlanVersion(plan.ID)
		} else {
			version, err = tx.Planning().PlanVersion(plan.ID, wanted)
		}
		switch {
		case errors.Is(err, ports.ErrNotFound) && wanted == 0:
			return fmt.Errorf("plan %s has no version yet: %w", plan.ID, ports.ErrNotFound)
		case errors.Is(err, ports.ErrNotFound):
			return fmt.Errorf("plan %s has no version %d: %w", plan.ID, wanted, ports.ErrNotFound)
		}
		if err != nil {
			return err
		}
		view, err = planForecastOf(tx, plan.ID, version.Version)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// planForecastOf reads what a plan version's approval shows of its estimates and forecasts, which approving
// records as the baseline: the latest forecasts the forecast tick kept and the estimate set they were made from,
// or the version's newest set before the tick has forecast it, and says whether the tick has (FR-EST-10). A
// version it has not forecast is pending while it is the plan's newest, or not yet recorded, and is superseded
// once a newer one is recorded, because the tick forecasts only the newest.
func planForecastOf(tx ports.ReadTx, plan string, version int64) (api.PlanForecast, error) {
	view := api.PlanForecast{Plan: plan, Version: version, State: api.ForecastMade, Forecasts: []delivery.Forecast{}}
	set, latest, err := control.ApprovalShows(tx.Estimates(), tx.Forecasts(), plan, version)
	if err != nil {
		return view, err
	}
	view.Set = set
	if len(latest) != 0 {
		view.Forecasts = latest
		return view, nil
	}
	view.State = api.ForecastPending
	newest, err := tx.Planning().LatestPlanVersion(plan)
	switch {
	case errors.Is(err, ports.ErrNotFound):
	case err != nil:
		return view, err
	case newest.Version > version:
		view.State = api.ForecastSuperseded
	}
	return view, nil
}

// readEstimate reads one work order's current estimate as it was made, with the explanation it showed, or none
// when no estimate was made for it, and its baseline once one is recorded (FR-EST-10, FR-EST-14).
func (h *handler) readEstimate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var view api.WorkEstimate
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		if _, err := tx.WorkOrders().Get(id); err != nil {
			return err
		}
		e, err := tx.Estimates().Current(id)
		switch {
		case err == nil:
			view.Estimate = &e
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		baseline, shown, err := tx.Estimates().BaselineOf(id)
		switch {
		case err == nil:
			view.Baseline = &api.WorkBaseline{ID: baseline.ID, Approval: baseline.Approval, RecordedAt: baseline.CreatedAt, Estimate: shown}
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
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

// readCalibration reads estimate calibration from the outcomes the fold keeps: over the window asked for, and over all
// time (FR-EST-11). It reads the project's own records alone.
func (h *handler) readCalibration(w http.ResponseWriter, r *http.Request) {
	length, err := api.ParseWindow(cmp.Or(r.URL.Query().Get("window"), api.DefaultCalibrationWindow))
	if err != nil {
		refuse(w, http.StatusBadRequest, api.ErrorDetail{Code: api.CodeInvalid, Message: err.Error()})
		return
	}
	var report delivery.Calibration
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		report, err = control.CalibrationIn(tx, length)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, report)
}

// readHistory reads the delivery history a planning session sees in its pack, from the project's own records (FR-EST-12).
func (h *handler) readHistory(w http.ResponseWriter, r *http.Request) {
	var view api.DeliveryHistory
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		history, names, err := control.DeliveryHistoryIn(tx.Policies(), tx.Deliveries(), tx.Repositories())
		view = api.DeliveryHistory{History: history, Repositories: names}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
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
