package lanes_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestMachineResources(t *testing.T) {
	total, available := 32*gib, 8*gib
	sample := lanes.MachineSample{
		Cores: 8, CPU: &lanes.CPUTicks{Busy: 1300, Total: 2000}, Load: &lanes.LoadAverage{One: 1, Five: 2, Fifteen: 3},
		MemoryTotalBytes: &total, MemoryAvailableBytes: &available, Pressure: &lanes.MemoryPressure{Some: 1.5, Full: 0.5},
		Volumes: []lanes.VolumeSample{
			{Volume: lanes.Volume{Role: lanes.VolumeWorktrees, Path: "/a"}, Device: 7, FreeBytes: 10, TotalBytes: 100},
			{Volume: lanes.Volume{Role: lanes.VolumeState, Path: "/b"}, Device: 7, FreeBytes: 10, TotalBytes: 100},
			{Volume: lanes.Volume{Role: lanes.VolumeWorktrees, Path: "/c"}, Device: 9, FreeBytes: 50, TotalBytes: 500},
			{Volume: lanes.Volume{Role: lanes.VolumeWorktrees, Path: "/d"}, Device: 9, FreeBytes: 50, TotalBytes: 500},
		},
	}
	got := lanes.MachineResources(sample, &lanes.CPUTicks{Busy: 1000, Total: 1000})
	if got.Cores != 8 || got.CPUPercent == nil || *got.CPUPercent != 30 {
		t.Errorf("cpu %+v", got.CPUPercent)
	}
	if *got.Load1 != 1 || *got.Load5 != 2 || *got.Load15 != 3 || *got.MemoryUsedBytes != 24*gib || *got.MemoryAvailableBytes != 8*gib || *got.MemoryPressureSome != 1.5 {
		t.Errorf("machine %+v", got)
	}
	want := []ledger.VolumeResources{
		{Roles: []string{lanes.VolumeState, lanes.VolumeWorktrees}, FreeBytes: 10, TotalBytes: 100},
		{Roles: []string{lanes.VolumeWorktrees}, FreeBytes: 50, TotalBytes: 500},
	}
	if !slices.EqualFunc(got.Volumes, want, func(a, b ledger.VolumeResources) bool {
		return slices.Equal(a.Roles, b.Roles) && a.FreeBytes == b.FreeBytes && a.TotalBytes == b.TotalBytes
	}) {
		t.Errorf("volumes %+v", got.Volumes)
	}

	first := lanes.MachineResources(lanes.MachineSample{Cores: 4, CPU: &lanes.CPUTicks{Busy: 1, Total: 2}}, nil)
	if first.CPUPercent != nil || first.Load1 != nil || first.MemoryTotalBytes != nil || first.MemoryUsedBytes != nil || first.MemoryPressureSome != nil || len(first.Volumes) != 0 {
		t.Errorf("a first read, or a measure not provided, was not absent: %+v", first)
	}
	if first.Volumes == nil {
		t.Error("no volumes is an empty list, so the report says none were readable")
	}
	still := lanes.MachineResources(lanes.MachineSample{Cores: 4, CPU: &lanes.CPUTicks{Busy: 5, Total: 10}}, &lanes.CPUTicks{Busy: 5, Total: 10})
	if still.CPUPercent != nil {
		t.Errorf("no time between two reads gave %v", *still.CPUPercent)
	}
}

func TestCPUMeterAveragesTheLastMinute(t *testing.T) {
	start := time.Unix(1000, 0)
	var meter lanes.CPUMeter
	if _, _, ok := meter.Add(start, 10); ok {
		t.Fatal("one read gave a rate")
	}
	steps := []struct {
		after   time.Duration
		seconds float64
		cores   float64
		minute  bool
	}{
		{15 * time.Second, 25, 1, false},
		{30 * time.Second, 40, 1, false},
		{60 * time.Second, 100, 1.5, true},
		// The read at 0s drops out: the rate is over the last minute, from 15s.
		{75 * time.Second, 175, 2.5, true},
	}
	for _, step := range steps {
		cores, minute, ok := meter.Add(start.Add(step.after), step.seconds)
		if !ok || cores != step.cores || minute != step.minute {
			t.Errorf("at %v: %v cores, minute %v, ok %v; want %v, %v", step.after, cores, minute, ok, step.cores, step.minute)
		}
	}
	// A new process tree starts its CPU time again, and so does the meter.
	if _, _, ok := meter.Add(start.Add(90*time.Second), 1); ok {
		t.Error("a tree whose CPU time went back gave a rate")
	}
	if cores, _, ok := meter.Add(start.Add(105*time.Second), 16); !ok || cores != 1 {
		t.Errorf("after a new tree: %v, %v", cores, ok)
	}
}

func TestLaneMeterKeepsItsPeak(t *testing.T) {
	start := time.Unix(1000, 0)
	var meter lanes.LaneMeter
	rss := int64(100)
	meter.Add(start, 0, rss)
	meter.Add(start.Add(30*time.Second), 60, 300)
	if peak := meter.Peak(); peak.CPU != 2 || peak.MemoryBytes != 300 {
		t.Errorf("a lane shorter than a minute: %+v", peak)
	}
	meter.Add(start.Add(60*time.Second), 90, 200)
	meter.Add(start.Add(90*time.Second), 105, 250)
	// Once a minute has passed, only averages over a whole minute count toward the peak.
	if peak := meter.Peak(); peak.CPU != 1.5 || peak.MemoryBytes != 300 {
		t.Errorf("a lane past a minute: %+v", peak)
	}
	var unread lanes.LaneMeter
	unread.Add(start, 0, 0)
	if peak := unread.Peak(); peak != (lanes.LanePeak{}) {
		t.Errorf("a lane never measured twice: %+v", peak)
	}
}

func TestKeepPeaks(t *testing.T) {
	var held []lanes.LanePeak
	for i := range 5 {
		held = lanes.KeepPeaks(held, lanes.LanePeak{CPU: float64(i)}, 3)
	}
	if len(held) != 3 || held[0].CPU != 2 || held[2].CPU != 4 {
		t.Errorf("held %+v", held)
	}
}
