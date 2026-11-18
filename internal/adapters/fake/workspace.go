package fake

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// Workspace answers where a project's workspace is from the clones a test placed on a fake machine, as a worker
// answers it from its own.
type Workspace struct {
	// Machine is the fake machine the answer is read from.
	Machine *Checkouts
}

// Find reads the machine as a worker does.
func (w Workspace) Find(ctx context.Context, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error) {
	return ports.NewWorkspace(w.Machine).Find(ctx, profile, project, planningRepository)
}

// Settings reads the machine as a worker does.
func (w Workspace) Settings(ctx context.Context, settings lanes.WorktreeSettings, workspace lanes.Workspace, repository string) (lanes.WorktreeSettings, string, error) {
	return ports.NewWorkspace(w.Machine).Settings(ctx, settings, workspace, repository)
}

var _ ports.Workspace = Workspace{}
