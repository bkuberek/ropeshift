package server

import (
	"errors"
	"net/http"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// backup copies the ledger to a file a member names. It changes nothing in the ledger, and it needs
// a member's session all the same, because it writes a file that holds every record and the
// passphrase's hash, at a path the caller chose.
func (h *handler) backup(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	var in api.BackupRequest
	if !decode(w, r, &in) {
		return
	}
	if h.Backup == nil {
		refuse(w, http.StatusNotImplemented, api.ErrorDetail{
			Code: api.CodeUnavailable, Message: "this control plane has no online backup: back up its database with your own tooling",
		})
		return
	}
	size, err := h.Backup.Backup(r.Context(), in.Path)
	if errors.Is(err, ports.ErrBackupTarget) {
		invalid(w, "%v", err)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, api.BackupResult{Path: in.Path, Bytes: size})
}
