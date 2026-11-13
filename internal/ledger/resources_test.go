package ledger_test

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
)

// TestAReportIsBounded holds what a worker sends to what the control plane keeps: a report is the
// worker's word, so its lists, its text, and its interval have limits, and a freshness window worked out
// from the interval cannot overflow.
func TestAReportIsBounded(t *testing.T) {
	long := strings.Repeat("é", 5000)
	report := ledger.ResourceReport{
		IntervalMs:     math.MaxInt64,
		Recommendation: &ledger.LaneRecommendation{MaxLanes: 3, Reason: long},
		Machine:        ledger.MachineResources{Volumes: make([]ledger.VolumeResources, 1000)},
		Sessions:       make([]ledger.SessionResources, 5000),
		Warnings:       make([]ledger.ResourceWarning, 1000),
	}
	report.Machine.Volumes[0].Roles = []string{long, "state", "a", "b", "c", "d", "e", "f", "g", "h"}
	report.Sessions[0] = ledger.SessionResources{Kind: long, Project: long, Lane: long}
	report.Warnings[0] = ledger.ResourceWarning{Measure: long, Volume: long, Message: long}
	got, err := ledger.BoundReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntervalMs != ledger.MaxReportIntervalMs || len(got.Sessions) != ledger.MaxReportSessions || len(got.Warnings) != ledger.MaxReportWarnings ||
		len(got.Machine.Volumes) != ledger.MaxReportVolumes {
		t.Errorf("interval %d, %d sessions, %d warnings, %d volumes", got.IntervalMs, len(got.Sessions), len(got.Warnings), len(got.Machine.Volumes))
	}
	for name, text := range map[string]string{
		"reason": got.Recommendation.Reason, "role": got.Machine.Volumes[0].Roles[0], "kind": got.Sessions[0].Kind, "lane": got.Sessions[0].Lane,
		"message": got.Warnings[0].Message, "volume": got.Warnings[0].Volume,
	} {
		if len(text) > ledger.MaxReportText || !utf8.ValidString(text) {
			t.Errorf("%s keeps %d bytes", name, len(text))
		}
	}
	if len(got.Machine.Volumes[0].Roles) != ledger.MaxReportRoles {
		t.Errorf("roles %d", len(got.Machine.Volumes[0].Roles))
	}
	if low, err := ledger.BoundReport(ledger.ResourceReport{IntervalMs: -5}); err != nil || low.IntervalMs != ledger.MinReportIntervalMs {
		t.Errorf("a negative interval keeps %d: %v", low.IntervalMs, err)
	}
	if report.Sessions[0].Kind != long {
		t.Error("bounding changed the report it was given")
	}
}
