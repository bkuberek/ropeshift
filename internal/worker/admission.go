package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// fleetMode reads the mode desired state last gave a project's lanes, which holds through an
// outage of the control plane. A plane of one project gives one mode.
func (w *Worker) fleetMode(ctx context.Context, project string) string {
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(err)
		return ""
	}
	for _, served := range state.Projects {
		if served.ID == project {
			return string(served.FleetMode)
		}
	}
	return string(state.FleetMode)
}

func (w *Worker) admitsRestart(ctx context.Context, project string) bool {
	return lanes.AdmitsRestart(w.fleetMode(ctx, project))
}

// runningLanes counts what holds this worker, which is every lane with a run and every lane
// a restart has yet to pick up.
func (w *Worker) runningLanes(ctx context.Context) (int64, error) {
	records, err := w.journal.Lanes(ctx)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// An orchestrator holds no lane's place, or it could keep the work it claims from starting.
	var count int64
	for _, run := range w.running {
		if !run.orchestrating() {
			count++
		}
	}
	for _, record := range records {
		if _, held := w.running[record.ID]; !held && record.State == lanes.StateRunning && record.Owner != lanes.OwnerOrchestrator {
			count++
		}
	}
	return count, nil
}

// limits reads this machine's caps. A profile that cannot be read leaves the start to fail
// where a lane's other settings are read, and that failure names the lane.
func (w *Worker) limits(ctx context.Context, lease ledger.DesiredLease) lanes.Admission {
	rt := w.config.Lanes
	repository, err := rt.Repository(ctx, lease.Repository)
	if err != nil {
		return lanes.Admission{}
	}
	profile, err := rt.Provisioner.Profiles.Load(ctx, repository, lease.Overrides)
	if err != nil {
		return lanes.Admission{}
	}
	return profile.Admission
}

// reading reads the machine. A read that fails holds nothing back and is reported, because
// a broken reading must not stop every lane on a machine that may be fine.
func (w *Worker) reading(ctx context.Context) lanes.MachineReading {
	machine := w.config.Lanes.Machine
	if machine == nil {
		return lanes.MachineReading{}
	}
	reading, err := machine.Reading(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading the machine: %w", err))
		return lanes.MachineReading{}
	}
	return reading
}

// admission names what holds a lane back, for what only this machine can see. The control plane
// held the claim to what the ledger can see. Woken says the lane parked, and has work to finish.
func (w *Worker) admission(ctx context.Context, lease ledger.DesiredLease, woken bool) string {
	running, err := w.runningLanes(ctx)
	if err != nil {
		w.config.Report(err)
		return ""
	}
	facts := w.usageFacts(lease.Lane, woken)
	facts.Mode, facts.Running, facts.Limits, facts.Machine = w.fleetMode(ctx, lease.Project), running, w.limits(ctx, lease), w.reading(ctx)
	return lanes.Admit(facts)
}

// hold leaves a lane to a later pass, and tells a person once per reason. The lease stays
// renewed, so a held lane keeps its place.
func (w *Worker) hold(ctx context.Context, lease ledger.DesiredLease, reason string) error {
	w.mu.Lock()
	said := w.holding[lease.Lane] == reason
	w.holding[lease.Lane] = reason
	w.mu.Unlock()
	if said {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"lane": lease.Lane, "reason": reason})
	if err != nil {
		return err
	}
	event := ledger.UploadEvent{
		Project: lease.Project, Type: ledger.UploadAdmissionHeld, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence, Payload: payload,
	}
	_, err = w.Record(ctx, event)
	return err
}
