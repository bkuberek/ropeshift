package server

import (
	"cmp"
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/measures"
	"ropeshift.dev/ropeshift/internal/ports"
)

func (h *handler) measureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/measures", h.readMeasures)
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
