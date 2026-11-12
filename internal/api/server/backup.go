package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

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

// downloadBackup answers a copy of this handler's project in the response, which a browser saves as a
// download. The copy is made the way a backup to a file is, into a directory of its own that is gone
// when the answer ends, so nothing of it stays on the daemon's machine.
func (h *handler) downloadBackup(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	project, err := h.self(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	h.answerCopy(w, project.Name, func(path string) (int64, error) { return h.Projects.Backup(r.Context(), project, path) })
}

// downloadInstallationBackup answers a copy of the installation's own records in the response.
func (h *handler) downloadInstallationBackup(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	h.answerCopy(w, "installation", func(path string) (int64, error) { return h.Projects.BackupInstallation(r.Context(), path) })
}

// answerCopy makes a copy with copy, into a file it names, and answers its bytes with a file name that
// says what it holds and when it was made.
func (h *handler) answerCopy(w http.ResponseWriter, name string, copy func(path string) (int64, error)) {
	dir, err := os.MkdirTemp("", "ropeshift-backup-")
	if err != nil {
		fail(w, err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, name+".db")
	if _, err := copy(path); err != nil {
		answerBackup(w, path, 0, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		fail(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fail(w, err)
		return
	}
	header := w.Header()
	header.Set("Content-Type", api.BackupMediaType)
	header.Set("Content-Length", fmt.Sprint(info.Size()))
	header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("ropeshift-%s-%s.db", name, time.Now().UTC().Format("20060102-150405"))))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}
