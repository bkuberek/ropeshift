package lanes

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The documented defaults of the recommendation (FR-OBS-9) and of what a worker reports (FR-OBS-8,
// FR-UI-10). They are starting values to tune on real machines, not measurements.
const (
	DefaultLaneCPU             = 1.0
	DefaultLaneMemoryBytes     = 2 << 30
	DefaultReserveCPU          = 1.0
	DefaultReserveMemoryBytes  = 4 << 30
	DefaultObservedWindow      = 50
	DefaultObservedMin         = 10
	DefaultReportInterval      = 15 * time.Second
	DefaultDiskFreeWarnBytes   = 10 << 30
	DefaultMemoryAvailableWarn = 0.10
	DefaultMemoryPressureWarn  = 10.0
	DefaultCPULoadWarn         = 1.5
	// minReportInterval keeps a mistyped interval from turning the worker's measuring into a busy loop.
	minReportInterval = time.Second
	// ObservedFloor is the least share of an estimate an observed peak counts as. A peak measured low, by
	// lanes that did little or by a tree whose CPU the worker could not see, lifts the recommendation at
	// most to twice what the estimates give.
	ObservedFloor = 0.5
)

// RecommendSettings is the [admission] table's recommendation settings, read, with every default applied.
type RecommendSettings struct {
	LaneCPU            float64
	LaneMemoryBytes    int64
	ReserveCPU         float64
	ReserveMemoryBytes int64
	ObservedWindow     int64
	ObservedMin        int64
}

// Recommending reads the recommendation's settings. An estimate must be above zero, a reserve may be zero,
// and the lanes that must have run cannot be more than the window that holds them.
func (a Admission) Recommending() (RecommendSettings, error) {
	out := RecommendSettings{
		LaneCPU: DefaultLaneCPU, LaneMemoryBytes: DefaultLaneMemoryBytes, ReserveCPU: DefaultReserveCPU, ReserveMemoryBytes: DefaultReserveMemoryBytes,
		ObservedWindow: DefaultObservedWindow, ObservedMin: DefaultObservedMin,
	}
	bad := func(format string, args ...any) (RecommendSettings, error) {
		return RecommendSettings{}, fmt.Errorf("%w: "+format, append([]any{ErrBadProfile}, args...)...)
	}
	if a.LaneCPUEstimate < 0 {
		return bad("lane_cpu_estimate is cores above zero")
	}
	if a.LaneCPUEstimate > 0 {
		out.LaneCPU = a.LaneCPUEstimate
	}
	if a.LaneMemoryEstimate != "" {
		size, err := ParseBytes(a.LaneMemoryEstimate)
		if err != nil || size == 0 {
			return bad("lane_memory_estimate is a size above zero, such as \"2GiB\"")
		}
		out.LaneMemoryBytes = size
	}
	if a.ReserveCPU != nil {
		if *a.ReserveCPU < 0 {
			return bad("reserve_cpu is zero or more cores")
		}
		out.ReserveCPU = *a.ReserveCPU
	}
	if a.ReserveMemory != "" {
		size, err := ParseBytes(a.ReserveMemory)
		if err != nil {
			return bad("reserve_memory is a size, such as \"4GiB\"")
		}
		out.ReserveMemoryBytes = size
	}
	if a.ObservedWindow < 0 || a.ObservedMin < 0 {
		return bad("observed_window and observed_min count lanes")
	}
	if a.ObservedWindow > 0 {
		out.ObservedWindow = a.ObservedWindow
	}
	if a.ObservedMin > 0 {
		out.ObservedMin = a.ObservedMin
	}
	if out.ObservedMin > out.ObservedWindow {
		return bad("observed_min is at most observed_window")
	}
	return out, nil
}

// Thresholds are where a measure warns (FR-UI-10). Zero turns a warning off.
type Thresholds struct {
	// DiskFreeBytes warns on a watched volume with less free space.
	DiskFreeBytes int64
	// MemoryAvailable warns when available memory is a smaller share of the total.
	MemoryAvailable float64
	// MemoryPressure warns past this share of the last minute, in percent, in which some task waited on
	// memory.
	MemoryPressure float64
	// CPULoad warns past this five-minute load per core.
	CPULoad float64
}

// Reporting reads how often a worker measures and where a measure warns, with every default applied.
func (s ResourceSettings) Reporting() (time.Duration, Thresholds, error) {
	interval := DefaultReportInterval
	thresholds := Thresholds{
		DiskFreeBytes: DefaultDiskFreeWarnBytes, MemoryAvailable: DefaultMemoryAvailableWarn, MemoryPressure: DefaultMemoryPressureWarn, CPULoad: DefaultCPULoadWarn,
	}
	bad := func(format string) (time.Duration, Thresholds, error) {
		return 0, Thresholds{}, fmt.Errorf("%w: "+format, ErrBadProfile)
	}
	if s.ReportInterval != "" {
		parsed, err := time.ParseDuration(s.ReportInterval)
		if err != nil || parsed < minReportInterval {
			return bad("report_interval is a duration of a second or more, such as \"15s\"")
		}
		interval = parsed
	}
	if s.DiskFreeWarn != "" {
		size, err := ParseBytes(s.DiskFreeWarn)
		if err != nil {
			return bad("disk_free_warn is a size, such as \"10GiB\", or \"0\" for no warning")
		}
		thresholds.DiskFreeBytes = size
	}
	if s.MemoryAvailableWarn != nil {
		if *s.MemoryAvailableWarn < 0 || *s.MemoryAvailableWarn > 1 {
			return bad("memory_available_warn is a fraction of total memory, from 0 to 1")
		}
		thresholds.MemoryAvailable = *s.MemoryAvailableWarn
	}
	for _, setting := range []struct {
		value  *float64
		target *float64
		name   string
	}{{s.MemoryPressureWarn, &thresholds.MemoryPressure, "memory_pressure_warn"}, {s.CPULoadWarn, &thresholds.CPULoad, "cpu_load_warn"}} {
		if setting.value == nil {
			continue
		}
		if *setting.value < 0 {
			return bad(setting.name + " is zero or more")
		}
		*setting.target = *setting.value
	}
	return interval, thresholds, nil
}

// MeasuringSettings is what of [resources] belongs to the machine and not to a lane: how often the worker
// measures and where a measure warns. Only the user's layer may change it.
func (s ResourceSettings) MeasuringSettings() ResourceSettings {
	return ResourceSettings{
		ReportInterval: s.ReportInterval, DiskFreeWarn: s.DiskFreeWarn, MemoryAvailableWarn: s.MemoryAvailableWarn,
		MemoryPressureWarn: s.MemoryPressureWarn, CPULoadWarn: s.CPULoadWarn,
	}
}

// SameMeasuring says two layers measure the same way.
func SameMeasuring(a, b ResourceSettings) bool {
	aInterval, aThresholds, aErr := a.Reporting()
	bInterval, bThresholds, bErr := b.Reporting()
	return aErr == nil && bErr == nil && aInterval == bInterval && aThresholds == bThresholds
}

var byteUnits = map[string]int64{
	"": 1, "B": 1,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
	"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
}

// ParseBytes reads a size: a number of bytes, or a number with a binary unit (KiB, MiB, GiB, TiB) or a
// decimal one (KB, MB, GB, TB).
func ParseBytes(text string) (int64, error) {
	text = strings.TrimSpace(text)
	split := strings.IndexFunc(text, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	number, unit := text, ""
	if split >= 0 {
		number, unit = text[:split], strings.TrimSpace(text[split:])
	}
	scale, ok := byteUnits[unit]
	if !ok || number == "" {
		return 0, fmt.Errorf("%q is not a size", text)
	}
	// math.MaxInt64 is 2^63 as a float, which is one past what an int64 holds, so a size reaching it is
	// refused, and a conversion that still lands below zero is too. Zero is a size: no reserve, or no
	// warning.
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 || value*float64(scale) >= math.MaxInt64 {
		return 0, fmt.Errorf("%q is not a size", text)
	}
	size := int64(value * float64(scale))
	if size < 0 {
		return 0, fmt.Errorf("%q is not a size", text)
	}
	return size, nil
}

// BytesWords says a size as a person reads it, in binary units with one decimal.
func BytesWords(value int64) string {
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	if value < 1<<10 {
		return strconv.FormatInt(value, 10) + " B"
	}
	scaled, unit := float64(value)/(1<<10), units[0]
	for _, next := range units[1:] {
		if scaled < 1<<10 {
			break
		}
		scaled, unit = scaled/(1<<10), next
	}
	return strconv.FormatFloat(scaled, 'f', 1, 64) + " " + unit
}

// LanePeak is the most one lane used while it ran: the cores its process tree kept busy, and its resident
// memory. A measure the worker could not read is zero, and the recommendation keeps that estimate.
type LanePeak struct {
	CPU         float64 `json:"cpu"`
	MemoryBytes int64   `json:"memory_bytes"`
}

// RecommendInput is what a recommendation reads: the machine, the settings, and the peaks of the lanes this
// worker ran, oldest first.
type RecommendInput struct {
	Cores            int64
	MemoryTotalBytes *int64
	Settings         RecommendSettings
	Peaks            []LanePeak
}

// Recommend works out the lane cap a worker recommends (FR-OBS-9):
//
//	max(1, min(floor((cores - reserve_cpu) / lane_cpu), floor((memory - reserve_memory) / lane_memory)))
//
// One lane's share is the profile's estimates until observed_min lanes have run, and from then on the
// highest peak among the last observed_window lanes, each measure on its own, and never under
// ObservedFloor of its estimate. A measure no lane's peak holds keeps its estimate. Memory it cannot read
// leaves the cores alone to decide. A machine whose cores it could not count has no recommendation.
func Recommend(in RecommendInput) (ledger.LaneRecommendation, bool) {
	if in.Cores <= 0 {
		return ledger.LaneRecommendation{}, false
	}
	s := in.Settings
	out := ledger.LaneRecommendation{
		Basis: ledger.BasisEstimates, Cores: in.Cores, MemoryTotalBytes: in.MemoryTotalBytes, ReserveCPU: s.ReserveCPU, ReserveMemoryBytes: s.ReserveMemoryBytes,
		LaneCPU: s.LaneCPU, LaneMemoryBytes: s.LaneMemoryBytes,
	}
	window := in.Peaks[max(0, len(in.Peaks)-int(s.ObservedWindow)):]
	out.ObservedLanes = int64(len(window))
	if s.ObservedMin > 0 && out.ObservedLanes >= s.ObservedMin {
		out.Basis = ledger.BasisObserved
		var cpu float64
		var memory int64
		for _, peak := range window {
			cpu, memory = max(cpu, peak.CPU), max(memory, peak.MemoryBytes)
		}
		if cpu > 0 {
			out.LaneCPU = max(cpu, s.LaneCPU*ObservedFloor)
		}
		if memory > 0 {
			out.LaneMemoryBytes = max(memory, int64(float64(s.LaneMemoryBytes)*ObservedFloor))
		}
	}
	out.ByCPU = max(0, int64(math.Floor((float64(in.Cores)-out.ReserveCPU)/out.LaneCPU)))
	out.MaxLanes = out.ByCPU
	reason := fmt.Sprintf("%d cores less %s reserved, at %s per lane, fit %d lanes", in.Cores, number(out.ReserveCPU), number(out.LaneCPU), out.ByCPU)
	if in.MemoryTotalBytes != nil {
		byMemory := max(0, (*in.MemoryTotalBytes-out.ReserveMemoryBytes)/out.LaneMemoryBytes)
		out.ByMemory = &byMemory
		out.MaxLanes = min(out.MaxLanes, byMemory)
		reason += fmt.Sprintf("; %s of memory less %s reserved, at %s per lane, fit %d", BytesWords(*in.MemoryTotalBytes), BytesWords(out.ReserveMemoryBytes),
			BytesWords(out.LaneMemoryBytes), byMemory)
	} else {
		reason += "; this machine's memory could not be read"
	}
	out.MaxLanes = max(1, out.MaxLanes)
	basis := "from the estimates"
	if out.Basis == ledger.BasisObserved {
		basis = fmt.Sprintf("from the highest peaks of the last %d lanes, as observed", out.ObservedLanes)
	}
	if in.MemoryTotalBytes != nil {
		out.Reason = fmt.Sprintf("%s; the lower is %d, %s", reason, out.MaxLanes, basis)
	} else {
		out.Reason = fmt.Sprintf("%s; the recommendation is %d, %s", reason, out.MaxLanes, basis)
	}
	return out, true
}

// UnreadRecommendation is what a worker recommends for a machine whose cores it has not counted: one lane,
// so a read that fails never leaves the cap unset.
func UnreadRecommendation() ledger.LaneRecommendation {
	return ledger.LaneRecommendation{
		MaxLanes: 1, Basis: ledger.BasisUnread, Reason: "this machine could not be read, so the worker recommends one lane until it can",
	}
}

func number(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

// WorkerCap is the per-worker cap in force for one lane, from the user's own value and the value its
// layers resolved to: the user's value or a layer's lower one, and where the user set none, the
// recommendation, which a layer can lower and not raise. A recommended value of zero is none, and then
// an unset cap caps nothing. Zero caps nothing.
func WorkerCap(user, layered *int64, recommended int64) int64 {
	switch {
	case layered == nil:
		return recommended
	case user != nil:
		return *layered
	case recommended > 0 && *layered > 0:
		return min(*layered, recommended)
	case *layered > 0:
		return *layered
	}
	return recommended
}

// ResourceWarnings names each measure past its threshold (FR-UI-10). A measure the machine did not provide
// warns of nothing, and a threshold of zero is off. A warning stops nothing.
func ResourceWarnings(m ledger.MachineResources, t Thresholds) []ledger.ResourceWarning {
	var out []ledger.ResourceWarning
	if t.CPULoad > 0 && m.Load5 != nil && m.Cores > 0 {
		if load := *m.Load5 / float64(m.Cores); load > t.CPULoad {
			out = append(out, ledger.ResourceWarning{
				Measure: ledger.WarnCPULoad, Value: load, Threshold: t.CPULoad,
				Message: fmt.Sprintf("The five-minute load is %s per core, past %s.", strconv.FormatFloat(load, 'f', 2, 64), number(t.CPULoad)),
			})
		}
	}
	if t.MemoryAvailable > 0 && m.MemoryAvailableBytes != nil && m.MemoryTotalBytes != nil && *m.MemoryTotalBytes > 0 {
		if share := float64(*m.MemoryAvailableBytes) / float64(*m.MemoryTotalBytes); share < t.MemoryAvailable {
			out = append(out, ledger.ResourceWarning{
				Measure: ledger.WarnMemoryAvailable, Value: share, Threshold: t.MemoryAvailable,
				Message: fmt.Sprintf("%s of %s of memory is available, under %s%%.", BytesWords(*m.MemoryAvailableBytes), BytesWords(*m.MemoryTotalBytes), number(t.MemoryAvailable*100)),
			})
		}
	}
	if t.MemoryPressure > 0 && m.MemoryPressureSome != nil && *m.MemoryPressureSome > t.MemoryPressure {
		out = append(out, ledger.ResourceWarning{
			Measure: ledger.WarnMemoryPressure, Value: *m.MemoryPressureSome, Threshold: t.MemoryPressure,
			Message: fmt.Sprintf("Some task waited on memory for %s%% of the last minute, past %s%%.", number(*m.MemoryPressureSome), number(t.MemoryPressure)),
		})
	}
	if t.DiskFreeBytes > 0 {
		for _, volume := range m.Volumes {
			if volume.FreeBytes >= t.DiskFreeBytes {
				continue
			}
			roles := strings.Join(volume.Roles, " and ")
			out = append(out, ledger.ResourceWarning{
				Measure: ledger.WarnDiskFree, Volume: roles, Value: float64(volume.FreeBytes), Threshold: float64(t.DiskFreeBytes),
				Message: fmt.Sprintf("The %s volume has %s free, under %s.", roles, BytesWords(volume.FreeBytes), BytesWords(t.DiskFreeBytes)),
			})
		}
	}
	return out
}

// CapsReached names each lane cap a report's running lanes stand at, with its value (FR-OBS-14). The lanes
// stand at a cap when they reach it or pass it, and a cap of zero caps nothing. A report that does not count
// its lanes counts its lane sessions.
func CapsReached(report ledger.ResourceReport) map[string]int64 {
	var running int64
	if report.RunningLanes != nil {
		running = *report.RunningLanes
	} else {
		for _, session := range report.Sessions {
			if session.Kind == ledger.SessionLane {
				running++
			}
		}
	}
	out := map[string]int64{}
	for kind, value := range map[string]int64{ledger.CapMaxLanes: report.Caps.MaxLanes, ledger.CapMaxLanesPerSignIn: report.Caps.MaxLanesPerSignIn} {
		if value > 0 && running >= value {
			out[kind] = value
		}
	}
	return out
}

// CapacityStep is what one report does to a worker's capacity intervals: those it ends, those it still sees,
// and those it begins, each as it now stands.
type CapacityStep struct {
	Ended, Seen, Begun []ledger.CapacityInterval
}

// StepCapacity moves a worker's standing capacity intervals by one report of its session epoch, measured at
// at by the control plane's clock (FR-OBS-14). An interval the report still sees at the same value stands
// until at. One the report sees below its cap, or at another value, ends at at. One of another session, or
// whose last report is more than freshMs before this one, ends at its last report, because the worker
// stopped reporting in between. A cap reached with no interval standing begins one at at, and never before
// the end of the interval of that cap the report ended, so a worker's intervals of one cap never overlap. A
// report dated before an interval's last report moves nothing of it back.
func StepCapacity(open []ledger.CapacityInterval, worker string, epoch int64, report ledger.ResourceReport, at, freshMs int64) CapacityStep {
	reached := CapsReached(report)
	var step CapacityStep
	standing, from := map[string]bool{}, map[string]int64{}
	for _, interval := range open {
		when := max(at, interval.SeenAt)
		switch value, ok := reached[interval.Cap]; {
		case interval.Epoch != epoch || when-interval.SeenAt > freshMs:
			interval.EndedAt = interval.SeenAt
			step.Ended = append(step.Ended, interval)
			from[interval.Cap] = max(from[interval.Cap], interval.EndedAt)
		case ok && value == interval.Value:
			interval.SeenAt = when
			step.Seen = append(step.Seen, interval)
			standing[interval.Cap] = true
		default:
			interval.EndedAt = when
			step.Ended = append(step.Ended, interval)
			from[interval.Cap] = max(from[interval.Cap], interval.EndedAt)
		}
	}
	for _, kind := range []string{ledger.CapMaxLanes, ledger.CapMaxLanesPerSignIn} {
		value, ok := reached[kind]
		if !ok || standing[kind] {
			continue
		}
		start := max(at, from[kind])
		step.Begun = append(step.Begun, ledger.CapacityInterval{Worker: worker, Cap: kind, Value: value, Epoch: epoch, StartedAt: start, SeenAt: start})
	}
	return step
}

// LaneCapSettings is what a member sets of a machine's lane caps (FR-PROJ-7). MaxLanes nil follows the
// recommendation and zero caps nothing; MaxLanesPerSignIn zero caps nothing. Neither has an upper bound.
type LaneCapSettings struct {
	MaxLanes          *int64
	MaxLanesPerSignIn int64
}

// Check refuses a cap below zero.
func (s LaneCapSettings) Check() error {
	if (s.MaxLanes != nil && *s.MaxLanes < 0) || s.MaxLanesPerSignIn < 0 {
		return fmt.Errorf("%w: a lane cap is a positive number, or 0 for no cap", ErrBadProfile)
	}
	return nil
}
