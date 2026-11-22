package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// holdOnSettings holds a generation whose worktree changed the project settings its agent CLI reads in a key that runs
// code or widens access, which its next session would run outside every rule ropeshift gives it (ADR-0040). A change a
// member decided runs as they chose: with the repository's settings, which the driver restores before the session
// starts and the generation is told of, or with the lane's own. Any other change parks the generation before its
// session starts, with no process and no model call, for a member's choice, and the lane records the change for Needs
// you. A read of the decisions that fails holds as a change nobody decided does. An orchestrator's and an assistant's
// sessions have no base to compare, so they hold nothing here.
func (r *laneRun) holdOnSettings(ctx context.Context, spec *ports.SessionSpec, change *lanes.SettingsChange) (workorders.Report, bool) {
	if change == nil || r.orchestrating() || r.assisting() {
		return workorders.Report{}, false
	}
	switch r.settingsDecided(ctx, change.Digest) {
	case lanes.SettingsLane:
		r.handleLock.Lock()
		r.settingsRan = change.Digest
		r.handleLock.Unlock()
		return workorders.Report{}, false
	case lanes.SettingsBase:
		spec.SettingsAtBase = true
		r.setSettingsNote(lanes.SettingsRestoredNote(change.Files))
		return workorders.Report{}, false
	}
	held := *change
	held.Generation = r.currentGeneration()
	r.recordSettingsHeld(ctx, held)
	r.sessionOver.Store(true)
	request := lanes.ParkRequest{Trigger: lanes.ParkSettings, Expr: lanes.HumanWake(), SkipNarrative: true}
	if err := r.beginParking(ctx, request); err != nil {
		r.sessionOver.Store(false)
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("holding on a settings change: %w", err)), true
	}
	return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonSettingsChanged}, true
}

// settingsDecided is the newest choice a member made for the settings change a digest names, or nothing.
func (r *laneRun) settingsDecided(ctx context.Context, digest string) string {
	facts, err := r.laneFacts(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading what members chose of its settings: %w", r.lease.Lane, err))
		return ""
	}
	choice := ""
	for _, decision := range facts.Settings {
		if decision.Digest == digest {
			choice = decision.Choice
		}
	}
	return choice
}

// setSettingsNote keeps what this generation is told first of the settings a member restored under it.
func (r *laneRun) setSettingsNote(note string) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.settingsNote = note
}

// withSettingsNote is a generation's first prompt, after what it is told of the settings a member restored under it.
func (r *laneRun) withSettingsNote(prompt string) string {
	r.handleLock.Lock()
	note := r.settingsNote
	r.handleLock.Unlock()
	if note == "" {
		return prompt
	}
	return note + "\n\n" + prompt
}

// recordSettingsHeld records on the lane the settings change a generation is held on, for Needs you.
func (r *laneRun) recordSettingsHeld(ctx context.Context, held lanes.SettingsChange) {
	payload, err := json.Marshal(held)
	if err == nil {
		_, err = r.w.Record(ctx, r.w.event(ledger.UploadLaneSettingsHeld, r.record(), payload))
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: recording the settings change it holds on: %w", r.lease.Lane, err))
	}
}

// startsWithSettings keeps the spec a generation's session starts with, which a reload of its settings is checked with.
func (r *laneRun) startsWithSettings(spec ports.SessionSpec) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.reloadSpec, r.reloadKnown = spec, true
}

// reloadAnswer is the worker's answer to a session's hook that asks about a reload.
func reloadAnswer(allowed bool) lanes.Reply {
	if allowed {
		return lanes.Reply{OK: true, Reload: lanes.ReloadAllow}
	}
	return lanes.Reply{OK: true, Reload: lanes.ReloadBlock}
}

// takeReload answers a session's `ConfigChange` hook, which asks whether its agent CLI may apply a settings file or a
// skill that changed during the session. A lane no run of this worker holds has no session it can compare, so its
// reload is blocked.
func (w *Worker) takeReload(ctx context.Context, lane lanes.Record) lanes.Reply {
	run := w.runOf(lane.ID)
	if run == nil {
		return reloadAnswer(false)
	}
	return reloadAnswer(run.reloadAllowed(ctx))
}

// reloadAllowed says whether a running session's agent CLI may apply a settings file or a skill that changed under it
// (ADR-0040). The CLI reloads a watched settings file when it changes, hooks and permissions included, so a change the
// generation's start did not hold would otherwise run at once. A reload applies when nothing that runs code or widens
// access differs from the repository's at the commit the lane's work began from, or when what differs is the change the
// session started with, which a member chose. Any other reload is blocked, so the session keeps what it has, and the
// lane is held for a member as a generation that finds the change at its start is (holdReloaded). A session whose spec
// cannot be read again, such as one a restarted worker adopted from a clone it cannot find, is blocked and held at its
// next start. An orchestrator's and an assistant's sessions have no base to compare, so their reloads apply.
func (r *laneRun) reloadAllowed(ctx context.Context) bool {
	if r.orchestrating() || r.assisting() {
		return true
	}
	spec, ran, ok := r.reloadSpecOf(ctx)
	if !ok {
		return false
	}
	change := r.rt.Driver.SettingsChange(ctx, spec)
	if change == nil || change.Digest == ran {
		return true
	}
	held, generation := *change, r.currentGeneration()
	r.w.goTracked(trackedSettingsHold, func() { r.holdReloaded(ctx, held, generation) })
	return false
}

// reloadSpecOf is the spec a reload is checked with, and the change the session started with: the spec this process
// started the session with, or for a session a restarted worker adopted, one read again from the lane's worktree, its
// clone, and its claim, which started with the repository's settings as far as this process knows. It says whether it
// could name the clone.
func (r *laneRun) reloadSpecOf(ctx context.Context) (ports.SessionSpec, string, bool) {
	r.handleLock.Lock()
	spec, ran, known := r.reloadSpec, r.settingsRan, r.reloadKnown
	r.handleLock.Unlock()
	if known {
		return spec, ran, true
	}
	repository, err := r.repository(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: finding its clone to check a reload of its settings: %w", r.lease.Lane, err))
		return ports.SessionSpec{}, "", false
	}
	spec = r.w.sessionSpec(ctx, r.record())
	spec.Dir, spec.Repository, spec.Base, spec.SettingsBase = r.worktreePath(), repository, r.begins(), r.settingsBase()
	spec.SettingsUnbased = r.settingsUnbased()
	return spec, "", true
}

// holdReloaded holds a lane whose session's reload was blocked, once per generation, unless a member already chose for
// that change, whose next generation runs as they chose. The lane parks in the middle of its turn on a member's choice:
// its next tool calls are denied until it hands off, and the change is recorded for Needs you as a held start records
// it. A lane that cannot park now, such as one already handing off, keeps the settings it has, and its next generation's
// start finds the same change and holds it there.
func (r *laneRun) holdReloaded(ctx context.Context, change lanes.SettingsChange, generation int64) {
	if r.settingsDecided(ctx, change.Digest) != "" || !r.reloadHeld.CompareAndSwap(false, true) {
		return
	}
	request := lanes.ParkRequest{Trigger: lanes.ParkSettings, Expr: lanes.HumanWake(), MidTurn: true}
	if err := r.beginParking(ctx, request); err != nil {
		r.reloadHeld.Store(false)
		if !errors.Is(err, ErrNoParking) && ctx.Err() == nil {
			r.w.config.Report(fmt.Errorf("lane %s: holding on a settings change its session made: %w", r.lease.Lane, err))
		}
		return
	}
	change.Generation = generation
	r.recordSettingsHeld(ctx, change)
}
