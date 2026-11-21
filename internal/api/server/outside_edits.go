package server

import (
	"fmt"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// outsideEdits reads what each registered repository's clone on the daemon's worker's machine holds that intake and
// lanes cannot read, with the steps that make it readable (FR-UI-14). It only reads.
func (h *handler) outsideEdits(w http.ResponseWriter, r *http.Request) {
	if h.OutsideEdits == nil {
		fail(w, fmt.Errorf("%w: reading the edits in a clone: this daemon reads no clone", ports.ErrRepositoryReadsUnavailable))
		return
	}
	project := ledger.DefaultProjectName
	if held, ok := projectOf(r.Context()); ok {
		project = held.Name
	}
	reading, err := h.OutsideEdits.Read(r.Context(), h.Store, h.Scope, project)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, outsideEditsView(reading))
}

// outsideEditsView is what a client reads of a reading. Every name, path, and problem git gave is plain text, and a path
// that is not as a page shows it is marked, so no client offers a command for it.
func outsideEditsView(reading control.OutsideReading) api.OutsideEdits {
	out := api.OutsideEdits{AsOf: reading.At, Limit: control.CloneEditsLimit, Clones: make([]api.CloneEditsView, 0, len(reading.Clones))}
	for _, clone := range reading.Clones {
		name := clone.Repository.Name
		if name == "" {
			name = clone.Repository.ID
		}
		view := api.CloneEditsView{
			Repository: clone.Repository.ID, Name: ledger.PlainText(name), Planning: clone.Planning, Path: ledger.PlainText(clone.Path),
			Branch: ledger.PlainText(clone.Edits.Branch), Head: clone.Edits.Head, Remote: ledger.PlainText(clone.Edits.Remote),
			Edits: make([]api.EditView, 0, len(clone.Edits.Edits)), Cut: clone.Edits.Cut, Steps: nonNil(clone.Steps), Problem: ledger.PlainText(clone.Problem),
		}
		taken := map[string][]string{}
		for _, step := range clone.Steps {
			for _, path := range step.Paths {
				taken[path] = append(taken[path], step.Kind)
			}
		}
		for _, edit := range clone.Edits.Edits {
			view.Edits = append(view.Edits, api.EditView{
				Path: ledger.PlainText(edit.Path), State: edit.State, Steps: nonNil(taken[edit.Path]), Hidden: !ledger.Commandable(edit.Path),
			})
		}
		out.Clones = append(out.Clones, view)
	}
	return out
}
