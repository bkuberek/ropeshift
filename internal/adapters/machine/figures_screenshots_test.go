//go:build screenshots

package machine_test

import (
	"context"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/machine"
	"ropeshift.com/ropeshift/internal/lanes"
)

// The screenshots build reports the fixed machine whatever this one has, and keeps the volumes it read.
func TestScreenshotsBuildReportsTheFixedMachine(t *testing.T) {
	dir := t.TempDir()
	volumes := []lanes.Volume{{Role: lanes.VolumeState, Path: dir}, {Role: lanes.VolumeWorktrees, Path: dir}}
	reader := machine.Reader{GOOS: "linux"}
	sample, err := reader.Sample(context.Background(), volumes)
	if err != nil {
		t.Fatal(err)
	}
	got := lanes.MachineResources(sample, nil)
	const gib = int64(1) << 30
	if got.Cores != 10 || got.MemoryTotalBytes == nil || *got.MemoryTotalBytes != 32*gib {
		t.Errorf("cores %d and memory %v, want 10 cores and 32 GiB", got.Cores, got.MemoryTotalBytes)
	}
	if got.MemoryUsedBytes == nil || lanes.BytesWords(*got.MemoryUsedBytes) != "9.6 GiB" {
		t.Errorf("memory used %v, want 9.6 GiB", got.MemoryUsedBytes)
	}
	if got.Load5 == nil || *got.Load5 != 1.62 || got.CPUPercent != nil || got.MemoryPressureSome != nil {
		t.Errorf("load %v, CPU %v, pressure %v: want a five-minute load of 1.62 and no CPU or pressure", got.Load5, got.CPUPercent, got.MemoryPressureSome)
	}
	if len(got.Volumes) != 1 || len(got.Volumes[0].Roles) != 2 || got.Volumes[0].FreeBytes != 412*gib {
		t.Errorf("volumes %+v, want one filesystem holding both roles with 412 GiB free", got.Volumes)
	}
	reading, err := reader.Reading(context.Background())
	if err != nil || !reading.Known || reading.FreeMemoryMB != 22937 || reading.FreeCPUPercent != 81 {
		t.Errorf("reading %+v, %v: want 22937 MB and 81%% free", reading, err)
	}
}
