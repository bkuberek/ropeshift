package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// What holds a run comes from its worker's word that it awaits a wake, and the worker's standing says which wait
// that is: an attended worker with no run window open judges no new work until a member opens one.
func TestWhatHoldsAnOrchestratorsRun(t *testing.T) {
	t.Parallel()
	awaiting := []string{string(lanes.AwaitingWake)}
	closed := authority.AutonomyStanding{Level: authority.LevelAttended}
	open := authority.AutonomyStanding{Level: authority.LevelAttended, WindowOpen: true}
	tests := []struct {
		name     string
		row      ledger.Orchestrator
		standing authority.AutonomyStanding
		want     OrchestratorWait
	}{
		{
			name: "a wait on an attended worker with no window open", standing: closed, want: WaitsForRunWindow,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorRunning, Actual: string(lanes.Provisioning), Conditions: awaiting},
		},
		{
			name: "a later generation's wait on an attended worker with no window open", standing: closed, want: WaitsForRunWindow,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorRunning, Actual: string(lanes.Starting), Conditions: awaiting},
		},
		{
			name: "a wait on an attended worker with a window open", standing: open, want: WaitsForWork,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorRunning, Actual: string(lanes.Provisioning), Conditions: awaiting},
		},
		{
			name: "a wait on a manual worker", standing: authority.AutonomyStanding{Level: authority.LevelManual}, want: WaitsForWork,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorRunning, Actual: string(lanes.Provisioning), Conditions: awaiting},
		},
		{
			name: "a wait on an unattended worker", standing: authority.AutonomyStanding{Level: authority.LevelUnattended}, want: WaitsForWork,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorRunning, Actual: string(lanes.Provisioning), Conditions: awaiting},
		},
		{
			name: "a run a wake reached, which prepares its session", standing: closed, want: WaitsForNothing,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorRunning, Actual: string(lanes.Provisioning)},
		},
		{
			name: "a run a member stopped", standing: closed, want: WaitsForNothing,
			row: ledger.Orchestrator{Desired: ledger.OrchestratorStopped, Actual: string(lanes.Provisioning), Conditions: awaiting},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := waitOf(tt.row, tt.standing); got != tt.want {
				t.Errorf("waits for %v, want %v", got, tt.want)
			}
		})
	}
}
