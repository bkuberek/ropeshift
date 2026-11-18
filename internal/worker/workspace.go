package worker

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// planningRepository is the ID of the repository a project plans in, from the desired state the journal holds, or
// nothing when the state names none, does not name the project, or cannot be read. An empty project is the plane's
// only project. Nothing waits on the answer: a lane that cannot learn it is placed as a project with no workspace
// found through its planning clone.
func (w *Worker) planningRepository(ctx context.Context, project string) string {
	state, err := w.desired(ctx)
	if err != nil {
		return ""
	}
	for _, served := range state.Projects {
		if served.ID == project || project == "" && len(state.Projects) == 1 {
			return served.PlanningRepository
		}
	}
	return ""
}

// FindWorkspace finds a project's workspace on this worker, as ports.FindWorkspace does for every place that asks
// where a worktree goes.
func FindWorkspace(ctx context.Context, kept ports.Checkouts, profile lanes.Profile, project, planningRepository string) (lanes.Workspace, bool, error) {
	return ports.NewWorkspace(kept).Find(ctx, profile, project, planningRepository)
}
