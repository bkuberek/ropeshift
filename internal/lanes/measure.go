package lanes

import (
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// MachineResources turns one read of a machine into what a worker reports of it (FR-OBS-8). The CPU share
// needs the read before, and is absent on the first, as is every measure the read did not provide. Two
// volumes on one filesystem are one, holding both roles.
func MachineResources(sample MachineSample, before *CPUTicks) ledger.MachineResources {
	out := ledger.MachineResources{Cores: sample.Cores, Volumes: mergeVolumes(sample.Volumes)}
	if sample.CPU != nil && before != nil && sample.CPU.Total > before.Total && sample.CPU.Busy >= before.Busy {
		share := float64(sample.CPU.Busy-before.Busy) / float64(sample.CPU.Total-before.Total) * 100
		out.CPUPercent = &share
	}
	if sample.Load != nil {
		out.Load1, out.Load5, out.Load15 = &sample.Load.One, &sample.Load.Five, &sample.Load.Fifteen
	}
	out.MemoryTotalBytes, out.MemoryAvailableBytes = sample.MemoryTotalBytes, sample.MemoryAvailableBytes
	if sample.MemoryTotalBytes != nil && sample.MemoryAvailableBytes != nil {
		used := *sample.MemoryTotalBytes - *sample.MemoryAvailableBytes
		out.MemoryUsedBytes = &used
	}
	if sample.Pressure != nil {
		out.MemoryPressureSome, out.MemoryPressureFull = &sample.Pressure.Some, &sample.Pressure.Full
	}
	return out
}

// mergeVolumes gives each filesystem one entry, in the order its first volume came, with its roles sorted.
func mergeVolumes(samples []VolumeSample) []ledger.VolumeResources {
	out := []ledger.VolumeResources{}
	index := map[uint64]int{}
	for _, sample := range samples {
		at, seen := index[sample.Device]
		if !seen {
			index[sample.Device] = len(out)
			out = append(out, ledger.VolumeResources{Roles: []string{sample.Role}, FreeBytes: sample.FreeBytes, TotalBytes: sample.TotalBytes})
			continue
		}
		if !slices.Contains(out[at].Roles, sample.Role) {
			out[at].Roles = append(out[at].Roles, sample.Role)
			slices.Sort(out[at].Roles)
		}
	}
	return out
}

// meterWindow is the span a session's CPU is averaged over (FR-OBS-9).
const meterWindow = time.Minute

type cpuPoint struct {
	at      time.Time
	seconds float64
}

// CPUMeter turns a process tree's CPU time, read now and then, into the cores it kept busy: averaged over
// the last minute, or over every read so far while there is less than a minute of them. A tree's CPU time
// can go back: a process that leaves the tree takes its time with it, and a new generation starts its own
// from zero. The meter then goes on from where it stood, so it keeps its window and loses only the time
// that left between two reads.
type CPUMeter struct {
	points []cpuPoint
	// carried is the CPU time that left the tree, added to every later read so the series never goes back.
	carried float64
	last    float64
}

// Add takes one read and returns the cores kept busy, and whether that average spans a whole minute. A
// first read, and a read with no time since the one before, give no rate.
func (m *CPUMeter) Add(at time.Time, seconds float64) (cores float64, minute, ok bool) {
	if len(m.points) > 0 && !at.After(m.points[len(m.points)-1].at) {
		return 0, false, false
	}
	if len(m.points) > 0 && seconds < m.last {
		m.carried += m.last - seconds
	}
	m.last = seconds
	m.points = append(m.points, cpuPoint{at: at, seconds: seconds + m.carried})
	base := 0
	for i, point := range m.points {
		if !point.at.After(at.Add(-meterWindow)) {
			base = i
		}
	}
	m.points = m.points[base:]
	if len(m.points) < 2 {
		return 0, false, false
	}
	newest, oldest := m.points[len(m.points)-1], m.points[0]
	span := at.Sub(oldest.at)
	return (newest.seconds - oldest.seconds) / span.Seconds(), span >= meterWindow, true
}

// LaneMeter follows one session: its CPU through a CPUMeter, and the most it used. A lane's CPU peak is
// the highest average over a whole minute, or its average over its whole life if it ran less than one.
type LaneMeter struct {
	cpu        CPUMeter
	minutePeak float64
	hasMinute  bool
	last       float64
	memory     int64
}

// Add takes one read of the session's process tree, and returns the cores it kept busy and whether there
// was a read before to tell.
func (m *LaneMeter) Add(at time.Time, cpuSeconds float64, rssBytes int64) (float64, bool) {
	m.memory = max(m.memory, rssBytes)
	cores, minute, ok := m.cpu.Add(at, cpuSeconds)
	if !ok {
		return 0, false
	}
	m.last = cores
	if minute {
		m.minutePeak, m.hasMinute = max(m.minutePeak, cores), true
	}
	return cores, true
}

// Peak is the most the session used, so far.
func (m *LaneMeter) Peak() LanePeak {
	peak := LanePeak{CPU: m.last, MemoryBytes: m.memory}
	if m.hasMinute {
		peak.CPU = m.minutePeak
	}
	return peak
}

// KeepPeaks adds one lane's peak and keeps the newest up to the window.
func KeepPeaks(held []LanePeak, peak LanePeak, window int64) []LanePeak {
	held = append(held, peak)
	if over := len(held) - int(max(window, 1)); over > 0 {
		held = slices.Clone(held[over:])
	}
	return held
}
