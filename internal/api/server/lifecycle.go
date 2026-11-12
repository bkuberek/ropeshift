package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// lifecycleRoutes are a project's lifecycle (ADR-0021), each a member's step at the installation's level:
// the project's own routes cannot reach an archived or removed project.
func (h *handler) lifecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/projects", h.member(h.createProject))
	mux.HandleFunc("POST /v1/projects/import", h.member(h.importProject))
	mux.HandleFunc("POST "+api.ImportUploadPath, h.member(h.importUploaded))
	mux.HandleFunc("POST /v1/projects/{project}/rename", h.member(h.renameProject))
	mux.HandleFunc("POST /v1/projects/{project}/archive", h.member(h.archiveProject))
	mux.HandleFunc("POST /v1/projects/{project}/restore", h.member(h.restoreProject))
	mux.HandleFunc("POST /v1/projects/{project}/remove", h.member(h.removeProject))
}

// answerProject replies with a project's record and the repositories it holds.
func (h *handler) answerProject(w http.ResponseWriter, ctx context.Context, status int, p ledger.Project, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	index, err := h.Projects.Index(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	var held []string
	for _, entry := range index {
		if entry.Project == p.ID {
			held = append(held, entry.Identity)
		}
	}
	reply(w, status, projectBody(p, held))
}

func (h *handler) createProject(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.CreateProject
	if !decode(w, r, &in) {
		return
	}
	p, err := h.lifecycle.Create(r.Context(), in.Name, in.DisplayName, actor)
	h.answerProject(w, r.Context(), http.StatusCreated, p, err)
}

func (h *handler) importProject(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ImportProject
	if !decode(w, r, &in) {
		return
	}
	p, err := h.lifecycle.Import(r.Context(), in.Path, in.Name, actor)
	h.answerProject(w, r.Context(), http.StatusCreated, p, err)
}

// importUploaded registers a project from a backup the request carries, as a browser uploads it. The
// backup goes to a file of its own, which the import reads the way it reads a path a member names, and
// which is gone when the answer ends.
func (h *handler) importUploaded(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ImportUpload
	// base64 takes four bytes for every three, and the rest of the body is a name.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ImportUploadMaxBytes/3*4+maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		invalid(w, "%v", err)
		return
	}
	if len(in.Backup) == 0 {
		invalid(w, "an upload carries a project's backup, and this one carries none")
		return
	}
	dir, err := os.MkdirTemp("", "ropeshift-import-")
	if err != nil {
		fail(w, err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "upload.db")
	if err := os.WriteFile(path, in.Backup, 0o600); err != nil {
		fail(w, err)
		return
	}
	p, err := h.lifecycle.Import(r.Context(), path, in.Name, actor)
	h.answerProject(w, r.Context(), http.StatusCreated, p, err)
}

func (h *handler) renameProject(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RenameProject
	if !decode(w, r, &in) {
		return
	}
	name := r.PathValue("project")
	p, err := h.lifecycle.Rename(r.Context(), name, in.Name, actor)
	h.answerLifecycle(w, r, name, p, err)
}

func (h *handler) archiveProject(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	name := r.PathValue("project")
	p, err := h.lifecycle.Archive(r.Context(), name, actor)
	h.answerLifecycle(w, r, name, p, err)
}

func (h *handler) restoreProject(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	name := r.PathValue("project")
	p, err := h.lifecycle.Restore(r.Context(), name, actor)
	h.answerLifecycle(w, r, name, p, err)
}

func (h *handler) removeProject(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RemoveProject
	if !decode(w, r, &in) {
		return
	}
	name := r.PathValue("project")
	project, err := h.Projects.Named(r.Context(), name)
	if err != nil {
		failProject(w, name, err)
		return
	}
	if err := h.lifecycle.Remove(r.Context(), name, control.Removal{Confirm: in.Confirm, Backup: in.Backup, NoBackup: in.NoBackup}, actor); err != nil {
		fail(w, err)
		return
	}
	h.router.forget(project.ID)
	w.WriteHeader(http.StatusNoContent)
}

// answerLifecycle answers a step on a project a path names, which may name none.
func (h *handler) answerLifecycle(w http.ResponseWriter, r *http.Request, name string, p ledger.Project, err error) {
	if err != nil && p.ID == "" {
		if _, lookup := h.Projects.Named(r.Context(), name); lookup != nil {
			failProject(w, name, lookup)
			return
		}
	}
	h.answerProject(w, r.Context(), http.StatusOK, p, err)
}
