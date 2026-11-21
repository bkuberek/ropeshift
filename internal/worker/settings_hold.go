package worker

import (
	"context"
	"encoding/json"
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
		return workorders.Report{}, false
	case lanes.SettingsBase:
		spec.SettingsAtBase = true
		r.setSettingsNote(lanes.SettingsRestoredNote(change.Files))
		return workorders.Report{}, false
	}
	held := *change
	held.Generation = r.currentGeneration()
	payload, err := json.Marshal(held)
	if err == nil {
		_, err = r.w.Record(ctx, r.w.event(ledger.UploadLaneSettingsHeld, r.record(), payload))
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: recording the settings change it holds on: %w", r.lease.Lane, err))
	}
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
