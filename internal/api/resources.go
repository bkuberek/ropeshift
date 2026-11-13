package api

import (
	"fmt"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// unavailable is what a measure the machine could not provide reads as. It is never zero.
const unavailable = "unavailable"

// ResourceWords says what a worker last measured of its machine the way a person reads it (FR-OBS-8,
// FR-UI-10): CPU, load, memory, free disk, the cap in force beside the recommendation, and the warnings. A
// measure the machine could not provide reads as unavailable, and no report says so.
func ResourceWords(report *ledger.ResourceReport) string {
	if report == nil {
		return "resources not reported"
	}
	m := report.Machine
	parts := []string{
		"cpu " + measureOr(m.CPUPercent, func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + "%" }),
		"load " + loadWords(m),
		"memory " + memoryWords(m),
		"disk " + diskWords(m.Volumes),
		"cap " + capWords(report),
	}
	if len(report.Warnings) > 0 {
		measures := make([]string, 0, len(report.Warnings))
		for _, warning := range report.Warnings {
			measure := warning.Measure
			if warning.Volume != "" {
				measure += " " + warning.Volume
			}
			measures = append(measures, measure)
		}
		parts = append(parts, "warnings "+strings.Join(measures, ", "))
	}
	return strings.Join(parts, "  ")
}

// SessionWords says a session's process tree as its worker last measured it.
func SessionWords(session ledger.SessionResources) string {
	cpu := measureOr(session.CPUCores, func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) + " cores" })
	memory := unavailable
	if session.RSSBytes != nil {
		memory = lanes.BytesWords(*session.RSSBytes)
	}
	return "cpu " + cpu + "  rss " + memory
}

func measureOr(value *float64, say func(float64) string) string {
	if value == nil {
		return unavailable
	}
	return say(*value)
}

func loadWords(m ledger.MachineResources) string {
	if m.Load1 == nil || m.Load5 == nil || m.Load15 == nil {
		return unavailable
	}
	return fmt.Sprintf("%.2f %.2f %.2f", *m.Load1, *m.Load5, *m.Load15)
}

func memoryWords(m ledger.MachineResources) string {
	if m.MemoryTotalBytes == nil {
		return unavailable
	}
	used, available := unavailable, unavailable
	if m.MemoryUsedBytes != nil {
		used = lanes.BytesWords(*m.MemoryUsedBytes)
	}
	if m.MemoryAvailableBytes != nil {
		available = lanes.BytesWords(*m.MemoryAvailableBytes)
	}
	pressure := unavailable
	if m.MemoryPressureSome != nil {
		pressure = strconv.FormatFloat(*m.MemoryPressureSome, 'f', 1, 64) + "%"
	}
	return fmt.Sprintf("%s used of %s, %s available, pressure %s", used, lanes.BytesWords(*m.MemoryTotalBytes), available, pressure)
}

func diskWords(volumes []ledger.VolumeResources) string {
	if len(volumes) == 0 {
		return unavailable
	}
	parts := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		parts = append(parts, strings.Join(volume.Roles, " and ")+" "+lanes.BytesWords(volume.FreeBytes)+" free")
	}
	return strings.Join(parts, ", ")
}

func capWords(report *ledger.ResourceReport) string {
	caps := report.Caps
	in := strconv.FormatInt(caps.MaxLanes, 10)
	if caps.MaxLanes == 0 {
		in = "none"
	}
	switch {
	case report.Recommendation == nil:
		return in + ", no recommendation"
	case caps.MaxLanesSet:
		return fmt.Sprintf("%s (recommended %d)", in, report.Recommendation.MaxLanes)
	}
	return in + ", the recommendation"
}

// AgeWords says how long ago something was, from milliseconds, in the largest whole unit.
func AgeWords(ms int64) string {
	seconds := max(ms, 0) / 1000
	switch {
	case seconds < 60:
		return strconv.FormatInt(seconds, 10) + "s"
	case seconds < 3600:
		return strconv.FormatInt(seconds/60, 10) + "m"
	case seconds < 48*3600:
		return strconv.FormatInt(seconds/3600, 10) + "h"
	}
	return strconv.FormatInt(seconds/86400, 10) + "d"
}
