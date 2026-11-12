package api

import (
	"bytes"
	"context"
	"net/http"
)

// BackupRequest names the file a copy goes to: an absolute path, on the daemon's machine, that does not
// exist yet.
type BackupRequest struct {
	Path string `json:"path"`
}

// BackupResult is the copy that was written.
type BackupResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// InstallationBackupPath copies the installation's own records.
const InstallationBackupPath = "/v1/installation/backup"

// The routes that answer a backup in the response, for a browser that saves it as a download: a
// project's, as it reads under /v1, and the installation's. Each answers BackupMediaType with the
// backup's bytes, and names a file in Content-Disposition.
const (
	BackupDownloadPath             = "/v1/backup/download"
	InstallationBackupDownloadPath = "/v1/installation/backup/download"
	BackupMediaType                = "application/vnd.sqlite3"
)

// ImportUploadPath registers a project from a backup sent in the request, which a browser uploads.
const ImportUploadPath = ProjectsPath + "/import/upload"

// ImportUploadMaxBytes bounds an uploaded backup, before its base64 encoding.
const ImportUploadMaxBytes = 128 << 20

// ImportUpload is a project's backup sent in the request, and the name to register it under, which
// defaults to the backup's own. Backup reads as base64 in JSON.
type ImportUpload struct {
	Backup []byte `json:"backup"`
	Name   string `json:"name,omitempty"`
}

// Backup copies this project's ledger to a new file while the daemon runs.
func (c *ProjectClient) Backup(ctx context.Context, in BackupRequest) (BackupResult, error) {
	var out BackupResult
	err := c.do(ctx, http.MethodPost, "/v1/backup", in, &out)
	return out, err
}

// BackupInstallation copies the installation's own records to a new file while the daemon runs: the
// registry of projects, the members and the passphrase's hash, and the workers.
func (c *Client) BackupInstallation(ctx context.Context, in BackupRequest) (BackupResult, error) {
	var out BackupResult
	err := c.Do(ctx, http.MethodPost, InstallationBackupPath, in, &out)
	return out, err
}

// DownloadBackup reads a copy of this project's ledger in the response, as a browser downloads it.
func (c *ProjectClient) DownloadBackup(ctx context.Context) ([]byte, error) {
	var out bytes.Buffer
	err := c.do(ctx, http.MethodPost, BackupDownloadPath, struct{}{}, &out)
	return out.Bytes(), err
}

// DownloadInstallationBackup reads a copy of the installation's own records in the response.
func (c *Client) DownloadInstallationBackup(ctx context.Context) ([]byte, error) {
	var out bytes.Buffer
	err := c.Do(ctx, http.MethodPost, InstallationBackupDownloadPath, struct{}{}, &out)
	return out.Bytes(), err
}

// ImportUploaded registers a project from a backup the request carries, keeping its ID.
func (c *Client) ImportUploaded(ctx context.Context, in ImportUpload) (Project, error) {
	var out Project
	err := c.Do(ctx, http.MethodPost, ImportUploadPath, in, &out)
	return out, err
}
