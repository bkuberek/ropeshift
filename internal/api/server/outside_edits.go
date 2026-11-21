package server

import (
	"cmp"
	"fmt"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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
	read := h.OutsideEdits.Recent
	switch r.URL.RawQuery {
	case "":
	case api.FreshQuery:
		read = h.OutsideEdits.Fresh
	default:
		invalid(w, "a read of the clones takes no query, or "+api.FreshQuery)
		return
	}
	reading, err := read(r.Context(), h.Store, h.Scope, project)
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
	out.Conflicts = make([]api.ConflictNeed, 0, len(reading.Conflicts))
	for _, conflict := range reading.Conflicts {
		out.Conflicts = append(out.Conflicts, conflictNeedOf(conflict, conflict.WorkOrder))
	}
	return out
}

// conflictNeedOf is what a client reads of a conflict, with the work order as the ledger holds it now.
func conflictNeedOf(conflict control.Conflict, order workorders.WorkOrder) api.ConflictNeed {
	name := cmp.Or(conflict.Repository.Name, conflict.Repository.ID)
	need := api.ConflictNeed{
		WorkOrder: order.ID, Title: ledger.PlainText(order.Title), State: order.State, Lane: conflict.Lane, Branch: ledger.PlainText(conflict.Branch),
		Base: ledger.PlainText(conflict.Base), Repository: conflict.Repository.ID, Name: ledger.PlainText(name), Clone: ledger.PlainText(conflict.Clone),
		Paths: make([]api.ConflictPathView, 0, len(conflict.Paths)), Cut: conflict.Cut, Options: nonNil(conflict.Options), Clears: ledger.PlainText(conflict.Clears),
	}
	for _, path := range conflict.Paths {
		need.Paths = append(need.Paths, api.ConflictPathView{Path: ledger.PlainText(path.Path), Person: path.Person, Hidden: !ledger.Commandable(path.Path)})
	}
	return need
}

// conflictNeeds are the conflicts the last reading of the project's clones found, of work that is still open as the
// ledger holds it now, each as one item of Needs you.
func (h *handler) conflictNeeds(f floor) []api.NeedItem {
	if h.OutsideEdits == nil {
		return nil
	}
	orders := make(map[string]workorders.WorkOrder, len(f.orders))
	for _, order := range f.orders {
		orders[order.ID] = order
	}
	var items []api.NeedItem
	for _, conflict := range h.OutsideEdits.Conflicts(h.Scope) {
		order, held := orders[conflict.WorkOrder.ID]
		if !held || !control.ConflictStillOpen(order) {
			continue
		}
		need := conflictNeedOf(conflict, order)
		items = append(items, api.NeedItem{Kind: api.NeedConflict, ID: order.ID, Since: conflict.Since, Conflict: &need})
	}
	return items
}
