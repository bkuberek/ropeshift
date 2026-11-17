package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
)

// templateRoutes are the installation's route for the project templates this binary embeds.
func (h *handler) templateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+api.TemplatesPath, h.listTemplates)
}

// listTemplates answers every template with the version of this binary. It is a read of what the binary
// holds, so it names no operation.
func (h *handler) listTemplates(w http.ResponseWriter, _ *http.Request) {
	if h.Templates == nil {
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{Code: api.CodeUnavailable, Message: "this daemon serves no templates"})
		return
	}
	reply(w, http.StatusOK, api.TemplateList{Version: h.Build.Version, Templates: h.Templates})
}
