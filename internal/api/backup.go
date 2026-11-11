package api

import (
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
