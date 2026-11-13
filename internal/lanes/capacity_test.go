package lanes_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

const gib = int64(1) << 30

func ptr[T any](value T) *T { return &value }

func TestParseBytes(t *testing.T) {
	tests := []struct {
		text string
		want int64
	}{
		{"2GiB", 2 * gib}, {"512MiB", 512 << 20}, {"1.5GiB", 3 * gib / 2}, {"10GB", 10_000_000_000}, {"64KiB", 64 << 10},
		{"1TiB", 1 << 40}, {"1024", 1024}, {"0", 0}, {" 4 GiB ", 4 * gib},
	}
	for _, tt := range tests {
		if got, err := lanes.ParseBytes(tt.text); err != nil || got != tt.want {
			t.Errorf("%q: %d, %v", tt.text, got, err)
		}
	}
	// A size past what an int64 holds is refused, never wrapped to a negative one: 2^63 bytes is one past.
	for _, text := range []string{"", "lots", "-1GiB", "2 parsecs", "8388608TiB", "9223372036854775807", "99999999TB"} {
		if got, err := lanes.ParseBytes(text); err == nil {
			t.Errorf("%q passed as %d", text, got)
		}
	}
}

func TestRecommendingSettings(t *testing.T) {
	defaults, err := lanes.Admission{}.Recommending()
	if err != nil {
		t.Fatal(err)
	}
	want := lanes.RecommendSettings{LaneCPU: 1, LaneMemoryBytes: 2 * gib, ReserveCPU: 1, ReserveMemoryBytes: 4 * gib, ObservedWindow: 50, ObservedMin: 10}
	if defaults != want {
		t.Errorf("defaults %+v", defaults)
	}
	set, err := lanes.Admission{LaneCPUEstimate: 0.5, LaneMemoryEstimate: "1GiB", ReserveCPU: ptr(0.0), ReserveMemory: "0", ObservedWindow: 20, ObservedMin: 5}.Recommending()
	want = lanes.RecommendSettings{LaneCPU: 0.5, LaneMemoryBytes: gib, ReserveCPU: 0, ReserveMemoryBytes: 0, ObservedWindow: 20, ObservedMin: 5}
	if err != nil || set != want {
		t.Errorf("set %+v: %v", set, err)
	}
	for name, bad := range map[string]lanes.Admission{
		"a negative estimate":       {LaneCPUEstimate: -1},
		"a size that is no size":    {LaneMemoryEstimate: "plenty"},
		"a zero memory estimate":    {LaneMemoryEstimate: "0"},
		"a negative reserve":        {ReserveCPU: ptr(-1.0)},
		"a negative window":         {ObservedWindow: -1},
		"a minimum past its window": {ObservedWindow: 5, ObservedMin: 6},
	} {
		if _, err := bad.Recommending(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func TestReportingSettings(t *testing.T) {
	interval, thresholds, err := lanes.ResourceSettings{}.Reporting()
	want := lanes.Thresholds{DiskFreeBytes: 10 * gib, MemoryAvailable: 0.10, MemoryPressure: 10, CPULoad: 1.5}
	if err != nil || interval != 15*time.Second || thresholds != want {
		t.Errorf("defaults %v %+v: %v", interval, thresholds, err)
	}
	interval, thresholds, err = lanes.ResourceSettings{ReportInterval: "1m", DiskFreeWarn: "0", MemoryAvailableWarn: ptr(0.0), MemoryPressureWarn: ptr(25.0), CPULoadWarn: ptr(2.0)}.Reporting()
	want = lanes.Thresholds{MemoryPressure: 25, CPULoad: 2}
	if err != nil || interval != time.Minute || thresholds != want {
		t.Errorf("set %v %+v: %v", interval, thresholds, err)
	}
	for name, bad := range map[string]lanes.ResourceSettings{
		"an interval under a second":  {ReportInterval: "500ms"},
		"an interval that is no time": {ReportInterval: "often"},
		"a fraction past one":         {MemoryAvailableWarn: ptr(1.5)},
		"a negative load":             {CPULoadWarn: ptr(-1.0)},
		"a disk threshold of no size": {DiskFreeWarn: "some"},
	} {
		if _, _, err := bad.Reporting(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func peaks(count int, cpu float64, memory int64) []lanes.LanePeak {
	out := make([]lanes.LanePeak, count)
	for i := range out {
		out[i] = lanes.LanePeak{CPU: cpu, MemoryBytes: memory}
	}
	return out
}

func TestRecommend(t *testing.T) {
	settings, err := lanes.Admission{}.Recommending()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		cores    int64
		memory   *int64
		peaks    []lanes.LanePeak
		want     int64
		basis    string
		byMemory *int64
	}{
		{"the cores hold it", 8, ptr(32 * gib), nil, 7, ledger.BasisEstimates, ptr(int64(14))},
		{"the memory holds it", 16, ptr(8 * gib), nil, 2, ledger.BasisEstimates, ptr(int64(2))},
		{"a machine smaller than its reserve still runs one", 1, ptr(2 * gib), nil, 1, ledger.BasisEstimates, ptr(int64(0))},
		{"memory it cannot read leaves the cores", 4, nil, nil, 3, ledger.BasisEstimates, nil},
		{"fewer lanes than the minimum keep the estimates", 8, ptr(32 * gib), peaks(9, 0.5, gib), 7, ledger.BasisEstimates, ptr(int64(14))},
		{"enough lanes give their peaks", 8, ptr(32 * gib), peaks(10, 0.5, gib), 14, ledger.BasisObserved, ptr(int64(28))},
		{"only the last window counts", 8, ptr(32 * gib), append(peaks(10, 4, 8*gib), peaks(50, 0.5, gib)...), 14, ledger.BasisObserved, ptr(int64(28))},
		{"a peak it could not measure keeps that estimate", 8, ptr(32 * gib), peaks(10, 0, 4*gib), 7, ledger.BasisObserved, ptr(int64(7))},
		// A peak far under the estimate counts as half the estimate, so a quiet run of lanes cannot lift the
		// cap past twice what the estimates give: 64 GiB less 4 at 1 GiB, not at 400 MiB.
		{"a peak is never under half the estimate", 32, ptr(64 * gib), peaks(10, 0.05, 400<<20), 60, ledger.BasisObserved, ptr(int64(60))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := lanes.Recommend(lanes.RecommendInput{Cores: tt.cores, MemoryTotalBytes: tt.memory, Settings: settings, Peaks: tt.peaks})
			if !ok {
				t.Fatal("no recommendation")
			}
			if got.MaxLanes != tt.want || got.Basis != tt.basis {
				t.Errorf("recommended %d from %s, want %d from %s", got.MaxLanes, got.Basis, tt.want, tt.basis)
			}
			if (got.ByMemory == nil) != (tt.byMemory == nil) || (got.ByMemory != nil && *got.ByMemory != *tt.byMemory) {
				t.Errorf("by memory %v, want %v", got.ByMemory, tt.byMemory)
			}
			if !strings.Contains(got.Reason, tt.basis) {
				t.Errorf("the reason does not say its basis: %q", got.Reason)
			}
		})
	}
	if _, ok := lanes.Recommend(lanes.RecommendInput{Settings: settings}); ok {
		t.Error("a machine with no cores it could count was given a recommendation")
	}
	got, _ := lanes.Recommend(lanes.RecommendInput{Cores: 8, MemoryTotalBytes: ptr(32 * gib), Settings: settings})
	if got.Reason != "8 cores less 1 reserved, at 1 per lane, fit 7 lanes; 32.0 GiB of memory less 4.0 GiB reserved, at 2.0 GiB per lane, fit 14; the lower is 7, from the estimates" {
		t.Errorf("reason %q", got.Reason)
	}
}

func TestWorkerCap(t *testing.T) {
	tests := []struct {
		name          string
		user, layered *int64
		recommended   int64
		want          int64
	}{
		{"unset follows the recommendation", nil, nil, 7, 7},
		{"unset with no recommendation caps nothing", nil, nil, 0, 0},
		{"zero caps nothing", ptr(int64(0)), ptr(int64(0)), 7, 0},
		{"a value under the recommendation", ptr(int64(5)), ptr(int64(5)), 7, 5},
		{"a value over the recommendation stands", ptr(int64(12)), ptr(int64(12)), 7, 12},
		{"a repository lowers the user's value", ptr(int64(12)), ptr(int64(3)), 7, 3},
		{"a repository lowers the recommendation", nil, ptr(int64(3)), 7, 3},
		{"a repository cannot raise the recommendation", nil, ptr(int64(10)), 7, 7},
		{"a repository's value with no recommendation", nil, ptr(int64(10)), 0, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.WorkerCap(tt.user, tt.layered, tt.recommended); got != tt.want {
				t.Errorf("cap %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResourceWarnings(t *testing.T) {
	thresholds := lanes.Thresholds{DiskFreeBytes: 10 * gib, MemoryAvailable: 0.10, MemoryPressure: 10, CPULoad: 1.5}
	strained := ledger.MachineResources{
		Cores: 8, Load5: ptr(16.0), MemoryTotalBytes: ptr(32 * gib), MemoryAvailableBytes: ptr(gib), MemoryPressureSome: ptr(20.0),
		Volumes: []ledger.VolumeResources{
			{Roles: []string{lanes.VolumeState}, FreeBytes: 5 * gib, TotalBytes: 100 * gib},
			{Roles: []string{lanes.VolumeWorktrees}, FreeBytes: 100 * gib, TotalBytes: 500 * gib},
		},
	}
	warnings := lanes.ResourceWarnings(strained, thresholds)
	got := map[string]ledger.ResourceWarning{}
	for _, warning := range warnings {
		got[warning.Measure+"/"+warning.Volume] = warning
	}
	if len(warnings) != 4 {
		t.Fatalf("warnings %+v", warnings)
	}
	for key, value := range map[string]float64{
		ledger.WarnCPULoad + "/": 2, ledger.WarnMemoryAvailable + "/": 1.0 / 32, ledger.WarnMemoryPressure + "/": 20, ledger.WarnDiskFree + "/" + lanes.VolumeState: float64(5 * gib),
	} {
		if warning, ok := got[key]; !ok || warning.Value != value || warning.Message == "" {
			t.Errorf("%s: %+v", key, warning)
		}
	}
	if warnings := lanes.ResourceWarnings(ledger.MachineResources{Cores: 8}, thresholds); len(warnings) != 0 {
		t.Errorf("a measure the machine cannot provide warned: %+v", warnings)
	}
	if warnings := lanes.ResourceWarnings(strained, lanes.Thresholds{}); len(warnings) != 0 {
		t.Errorf("a threshold that is off warned: %+v", warnings)
	}
}
