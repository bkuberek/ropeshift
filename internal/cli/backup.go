package cli

import (
	"fmt"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/api"
)

// backup asks the daemon for a copy of its ledger at a new file. A relative path is read from where
// the command runs, because the daemon runs somewhere else.
func (c *invocation) backup(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("name one file for the copy")
	}
	path, err := filepath.Abs(positional[0])
	if err != nil {
		return c.usageError("%v", err)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.Backup(c.ctx, api.BackupRequest{Path: path})
	return c.finish(err, result, func() string {
		return fmt.Sprintf("backed up the ledger to %s, %d bytes\n", result.Path, result.Bytes)
	})
}
