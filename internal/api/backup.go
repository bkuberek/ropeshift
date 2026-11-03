package api

import (
	"context"
	"net/http"
)

// BackupRequest names the file a copy of the ledger goes to: an absolute path, on the daemon's
// machine, that does not exist yet.
type BackupRequest struct {
	Path string `json:"path"`
}

// BackupResult is the copy that was written.
type BackupResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Backup copies the ledger to a new file while the daemon runs.
func (c *Client) Backup(ctx context.Context, in BackupRequest) (BackupResult, error) {
	var out BackupResult
	err := c.Do(ctx, http.MethodPost, "/v1/backup", in, &out)
	return out, err
}
