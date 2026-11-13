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

// limits reads this machine's caps for one lane: the user's own layer, which says whether the user set a
// per-worker cap, and the lane's layers resolved. A profile that cannot be read leaves the start to fail
// where a lane's other settings are read, and that failure names the lane.
func (w *Worker) limits(ctx context.Context, lease ledger.DesiredLease) (user, layered lanes.Admission) {
	rt := w.config.Lanes
	if own, err := rt.Provisioner.Profiles.Load(ctx, "", ""); err == nil {
		user = own.Admission
	}
	repository, err := rt.Repository(ctx, lease.Project, lease.Repository)
	if err != nil {
		return user, user
	}
	profile, err := rt.Provisioner.Profiles.Load(ctx, repository, lease.Overrides)
	if err != nil {
		return user, user
	}
	return user, profile.Admission
}

// recommend works out the lane cap this worker recommends, from the machine's cores and memory and the peaks
// of the lanes it ran (FR-OBS-9). The machine's size does not change while it runs, so it reads the
// machine only before any read has counted it. A read that fails keeps the last recommendation, or gives
// one lane, so it never leaves the cap unset. Only a worker with no machine to read has none.
func (w *Worker) recommend(ctx context.Context, settings lanes.RecommendSettings) (ledger.LaneRecommendation, bool) {
	machine := w.config.Lanes.Machine
	if machine == nil {
		return ledger.LaneRecommendation{}, false
	}
	if cores, _ := w.size(); cores <= 0 {
		sample, err := machine.Sample(ctx, nil)
		if err != nil {
			w.config.Report(fmt.Errorf("sampling the machine: %w", err))
		} else {
			w.resources.lock.Lock()
			w.steady(&sample)
			w.resources.lock.Unlock()
		}
	}
	w.resources.lock.Lock()
	defer w.resources.lock.Unlock()
	return w.recommendLocked(settings), true
}

// recommended is the recommendation's cap where the user set none, and zero where the user set one or the
// worker has no recommendation, because then it decides nothing.
func (w *Worker) recommended(ctx context.Context, user lanes.Admission) int64 {
	if user.MaxLanes != nil {
		return 0
	}
	settings, err := user.Recommending()
	if err != nil {
		return 0
	}
	recommendation, ok := w.recommend(ctx, settings)
	if !ok {
		return 0
	}
	return recommendation.MaxLanes
}

// workerCap is the per-worker cap in force on this machine with no repository's layer, which a claim is
// held to (FR-PROJ-7). It cannot say with no profile to read, or with no cap set and no recommendation.
func (w *Worker) workerCap(ctx context.Context) (int64, bool) {
	rt := w.config.Lanes
	if rt == nil || rt.Provisioner.Profiles == nil {
		return 0, false
	}
	profile, err := rt.Provisioner.Profiles.Load(ctx, "", "")
	if err != nil {
		return 0, false
	}
	user := profile.Admission
	if user.MaxLanes != nil {
		return *user.MaxLanes, true
	}
	recommended := w.recommended(ctx, user)
	return recommended, recommended > 0
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
	user, layered := w.limits(ctx, lease)
	facts.Mode, facts.Running, facts.Limits = w.fleetMode(ctx, lease.Project), running, layered
	// Only a floor reads what is free now, which on a Mac spawns its reads, so with none set it reads nothing.
	if layered.MinFreeMemoryMB > 0 || layered.MinFreeCPUPercent > 0 {
		facts.Machine = w.reading(ctx)
	}
	facts.WorkerCap = lanes.WorkerCap(user.MaxLanes, layered.MaxLanes, w.recommended(ctx, user))
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
