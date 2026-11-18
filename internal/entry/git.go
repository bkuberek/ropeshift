package entry

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// checkGit reads this machine's git and holds it to the minimum ropeshift runs on. It is the report the doctor
// prints and the refusal a daemon starts with, so the two say the same words.
func checkGit(ctx context.Context, probe ports.GitProbe) lanes.GitReport {
	found, err := probe.Version(ctx)
	return lanes.CheckGit(found, err)
}

// requireGit is the refusal a daemon starts with. A worker runs git for every lane, merge, and registration, and the
// first of them to need a feature an old git lacks fails half way, so a daemon that finds a git below the minimum, or
// none, says so and does not start. It returns nil when git is usable.
func requireGit(ctx context.Context, probe ports.GitProbe) error {
	return checkGit(ctx, probe).Err()
}
