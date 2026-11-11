package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// backup copies this handler's project to a file a member names. It changes nothing in the ledger, and
// it needs a member's session all the same, because it writes a file that holds every record of the
// project, at a path the caller chose.
func (h *handler) backup(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	var in api.BackupRequest
	if !decode(w, r, &in) {
		return
	}
	project, err := h.self(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	size, err := h.Projects.Backup(r.Context(), project, in.Path)
	answerBackup(w, in.Path, size, err)
}

// backupInstallation copies the installation's own records: the registry, the members and the
// passphrase's hash, and the workers.
func (h *handler) backupInstallation(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	var in api.BackupRequest
	if !decode(w, r, &in) {
		return
	}
	size, err := h.Projects.BackupInstallation(r.Context(), in.Path)
	answerBackup(w, in.Path, size, err)
}

func answerBackup(w http.ResponseWriter, path string, size int64, err error) {
	switch {
	case errors.Is(err, ports.ErrNoBackup):
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{Code: api.CodeUnavailable, Message: err.Error()})
	case errors.Is(err, ports.ErrBackupTarget):
		invalid(w, "%v", err)
	case err != nil:
		fail(w, err)
	default:
		reply(w, http.StatusCreated, api.BackupResult{Path: path, Bytes: size})
	}
}

// self is the registry's record of this handler's project, which a rename changes and the ID does not.
func (h *handler) self(ctx context.Context) (ledger.Project, error) {
	projects, err := h.Projects.List(ctx)
	if err != nil {
		return ledger.Project{}, err
	}
	for _, p := range projects {
		if p.ID == h.Scope {
			return p, nil
		}
	}
	return ledger.Project{}, fmt.Errorf("project %s: %w", h.Scope, ports.ErrNotFound)
}
