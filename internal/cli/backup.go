package cli

import (
	"fmt"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/api"
)

// backup asks the daemon for a copy of one project's ledger at a new file, or with --installation of the
// installation's own records. A relative path is read from where the command runs, because the daemon
// runs somewhere else.
func (c *invocation) backup(args []string) int {
	set := c.flags()
	installation := set.Bool("installation", false, "copy the installation's records: projects, members, and workers")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("name one file for the copy")
	}
	if *installation && c.project != "" {
		return c.usageError("--installation copies no project, so it takes no --project")
	}
	path, err := filepath.Abs(positional[0])
	if err != nil {
		return c.usageError("%v", err)
	}
	request := api.BackupRequest{Path: path}
	if *installation {
		client := c.client()
		defer client.Close()
		result, err := client.BackupInstallation(c.ctx, request)
		return c.finish(err, result, func() string {
			return fmt.Sprintf("backed up the installation's records to %s, %d bytes\n", result.Path, result.Bytes)
		})
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.Backup(c.ctx, request)
	return c.finish(err, result, func() string {
		return fmt.Sprintf("backed up project %s to %s, %d bytes\n", client.Name(), result.Path, result.Bytes)
	})
}
