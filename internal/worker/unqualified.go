package worker

import (
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ErrCLINotQualified is why a worker that never qualifies the agent CLI starts no session.
var ErrCLINotQualified = errors.New("this daemon never qualifies the agent CLI, so it starts no session: a session runs only on a qualified version, " +
	"and the daemon `ropeshift init` starts for the length of its run qualifies none and takes no work")

// takesNoWork says this is the worker of the daemon `ropeshift init` starts, which takes no work at all. That daemon
// runs for the length of one command, on the installation's own journal and ledger, and may run while the
// installation's own daemon is stopped with work under way: claims not yet started, woken lanes, lanes whose session
// is gone, an orchestrator, open conversations, and sessions that outlived their daemon. It qualifies no agent CLI, so
// each of them it acted on would end refused, and a refused lane ends blocked, which only a member or the orchestrator
// undoes. So the worker registers and opens its epoch, which init waits for, and starts neither of its loops: Run
// makes no heartbeat, upload, or pass, so it claims, wakes, resumes, adopts, ends, and starts nothing, and ServeLanes
// answers no session. Everything stays in the journal and the ledger as it was, for the installation's own daemon.
func (w *Worker) takesNoWork() bool {
	return w.config.Lanes != nil && w.config.Lanes.NoProbe
}

// refuseSessions says why this worker starts no session, or nothing when it may. A lane, an orchestrator, an
// assistant, and a consultation each run a session of the agent CLI, and only on a version that was qualified
// (ADR-0040). A worker told not to probe is one that runs for the length of a command and is stopped within seconds,
// and it qualifies no version, so it treats the CLI as unqualified and starts none, whatever the shipped matrix holds
// for the installed version. Such a worker takes no work (takesNoWork), so this is a backstop for a path that reaches
// a session without its loops.
func (rt *LaneRuntime) refuseSessions() error {
	if rt.NoProbe {
		return ErrCLINotQualified
	}
	return nil
}

// refuseStart ends a lane whose session this worker does not start, and says why. The work did not run and nothing
// about it failed, so the outcome is blocked, as it is for a planning lane on a machine with no tmux, and no attempt
// is spent.
func (r *laneRun) refuseStart(err error) workorders.Report {
	r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonCLINotQualified, Detail: failureDetail(err)}
}

// advertised is the manifest this worker says of itself. A worker that never qualifies the agent CLI advertises each
// driver as not qualified, so the control plane and every page that shows the manifest read what the worker does.
func (rt *LaneRuntime) advertised(manifest ledger.Manifest) ledger.Manifest {
	if !rt.NoProbe {
		return manifest
	}
	manifest.Drivers = slices.Clone(manifest.Drivers)
	for i := range manifest.Drivers {
		manifest.Drivers[i].Qualified = false
	}
	return manifest
}
