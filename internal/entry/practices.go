package entry

import (
	"context"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/ghcli"
	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/worker"
)

// soloPractices is what a worker runs the practices check with: this machine's git for the files and the secret scan,
// the person's `gh` sign-in for the settings of the repository's forge, the checkouts the worker keeps, and a state
// directory for the journal of each request.
func soloPractices(stateDir string, repository func(ctx context.Context, project, id string) (string, error)) *worker.PracticesRuntime {
	return &worker.PracticesRuntime{
		Sources: practicerun.Sources{
			Snapshots: practicegit.Snapshots{}, Secrets: practicegit.SecretScanner{}, Forge: ghcli.Forge{}, Clock: clock.System{},
		},
		Repository: repository, StateDir: stateDir,
	}
}
