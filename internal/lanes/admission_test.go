package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func TestAdmit(t *testing.T) {
	limits := lanes.Admission{MaxLanes: 4, MaxLanesPerSignIn: 2, MinFreeMemoryMB: 512, MinFreeCPUPercent: 20}
	plenty := lanes.MachineReading{Known: true, FreeMemoryMB: 2048, FreeCPUPercent: 80}
	tests := []struct {
		name  string
		facts lanes.AdmissionFacts
		want  string
	}{
		{"nothing stands in the way", lanes.AdmissionFacts{Mode: lanes.FleetRun, Limits: limits, Machine: plenty}, ""},
		{"a paused fleet starts nothing", lanes.AdmissionFacts{Mode: lanes.FleetPause, Limits: limits, Machine: plenty}, lanes.HeldFleetPaused},
		{"a draining fleet starts nothing", lanes.AdmissionFacts{Mode: lanes.FleetDrain, Limits: limits, Machine: plenty}, lanes.HeldFleetDraining},
		{"the worker is full", lanes.AdmissionFacts{Mode: lanes.FleetRun, Running: 4, Limits: lanes.Admission{MaxLanes: 4}, Machine: plenty}, lanes.HeldWorkerFull},
		{"one below the worker's cap", lanes.AdmissionFacts{Mode: lanes.FleetRun, Running: 3, Limits: lanes.Admission{MaxLanes: 4}, Machine: plenty}, ""},
		{"the sign-in is full", lanes.AdmissionFacts{Mode: lanes.FleetRun, Running: 2, Limits: limits, Machine: plenty}, lanes.HeldSignInFull},
		{"memory under the floor", lanes.AdmissionFacts{Mode: lanes.FleetRun, Limits: limits, Machine: lanes.MachineReading{Known: true, FreeMemoryMB: 100, FreeCPUPercent: 80}}, lanes.HeldMemory},
		{"CPU under the floor", lanes.AdmissionFacts{Mode: lanes.FleetRun, Limits: limits, Machine: lanes.MachineReading{Known: true, FreeMemoryMB: 2048, FreeCPUPercent: 5}}, lanes.HeldCPU},
		{"a machine it cannot read does not stop a lane", lanes.AdmissionFacts{Mode: lanes.FleetRun, Limits: limits}, ""},
		{"zero is no limit", lanes.AdmissionFacts{Mode: lanes.FleetRun, Running: 99, Machine: lanes.MachineReading{Known: true}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.Admit(tt.facts); got != tt.want {
				t.Errorf("held %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdmitACrashRestart(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want bool
	}{
		{"run", lanes.FleetRun, true},
		{"pause lets a running lane come back", lanes.FleetPause, true},
		{"drain stops new generations", lanes.FleetDrain, false},
		{"no word yet reads as run", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.AdmitsRestart(tt.mode); got != tt.want {
				t.Errorf("admits %v", got)
			}
		})
	}
}
