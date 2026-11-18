package ledger

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The defaults are the design's values (ADR-0037), and a repository that edited nothing reads them.
func TestTheCIDefaultsAreTheDesignsValues(t *testing.T) {
	got := CISettings{}.Effective()
	want := CISettings{
		Custom: true, HistoryWindow: 50, HistoryMinRecords: 5, NoHistoryP50: "10m", NoHistoryP85: "20m", MinInterval: "30s", SteadyInterval: "5m",
		TimeoutMultiplier: 3, TimeoutNoHistory: "3h", QueueTimeoutMultiplier: 3, QueueTimeoutNoHistory: "30m", AutoRerun: 1,
		LogExcerptLines: 200, LogExcerptBytes: 8192,
	}
	if got != want {
		t.Errorf("the defaults read %+v", got)
	}
	if err := got.Check(); err != nil {
		t.Errorf("the defaults do not check: %v", err)
	}
	if (CISettings{}).Custom {
		t.Error("an unedited repository reads as custom")
	}
}

// Each key is set by its name and its text, takes effect, and a value outside its range is refused with the key and
// the range named.
func TestACIKeyIsSetByNameAndRefusedOutsideItsRange(t *testing.T) {
	tests := []struct {
		key, value string
		ok         bool
		check      func(CISettings) bool
	}{
		{"ci_history_window", "100", true, func(s CISettings) bool { return s.HistoryWindow == 100 }},
		{"ci_history_window", "4", false, nil},
		{"ci_history_min_records", "10", true, func(s CISettings) bool { return s.HistoryMinRecords == 10 }},
		{"ci_history_min_records", "0", false, nil},
		{"ci_no_history_p50", "15m", true, func(s CISettings) bool { return s.NoHistoryP50 == "15m" }},
		{"ci_no_history_p50", "5s", false, nil},
		{"ci_no_history_p50", "ten minutes", false, nil},
		{"ci_no_history_p85", "30m", true, func(s CISettings) bool { return s.NoHistoryP85 == "30m" }},
		{"ci_no_history_p85", "5m", false, nil},
		{"ci_min_interval", "1m", true, func(s CISettings) bool { return s.MinInterval == "1m" }},
		{"ci_min_interval", "1s", false, nil},
		{"ci_steady_interval", "2m", true, func(s CISettings) bool { return s.SteadyInterval == "2m" }},
		{"ci_steady_interval", "10s", false, nil},
		{"ci_steady_interval", "2h", false, nil},
		{"ci_timeout_multiplier", "5", true, func(s CISettings) bool { return s.TimeoutMultiplier == 5 }},
		{"ci_timeout_multiplier", "1", false, nil},
		{"ci_timeout_no_history", "6h", true, func(s CISettings) bool { return s.TimeoutNoHistory == "6h" }},
		{"ci_timeout_no_history", "1m", false, nil},
		{"ci_queue_timeout_multiplier", "2.5", true, func(s CISettings) bool { return s.QueueTimeoutMultiplier == 2.5 }},
		{"ci_queue_timeout_multiplier", "100", false, nil},
		{"ci_queue_timeout_no_history", "1h", true, func(s CISettings) bool { return s.QueueTimeoutNoHistory == "1h" }},
		{"ci_queue_timeout_no_history", "1s", false, nil},
		{"ci_auto_rerun", "0", true, func(s CISettings) bool { return s.AutoRerun == 0 && s.Custom }},
		{"ci_auto_rerun", "3", true, func(s CISettings) bool { return s.AutoRerun == 3 }},
		{"ci_auto_rerun", "4", false, nil},
		{"ci_auto_rerun", "-1", false, nil},
		{"ci_log_excerpt_lines", "50", true, func(s CISettings) bool { return s.LogExcerptLines == 50 }},
		{"ci_log_excerpt_lines", "5", false, nil},
		{"ci_log_excerpt_bytes", "4096", true, func(s CISettings) bool { return s.LogExcerptBytes == 4096 }},
		{"ci_log_excerpt_bytes", "10", false, nil},
		{"ci_made_up", "1", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			got, err := CISettings{}.Set(tt.key, tt.value)
			if tt.ok {
				if err != nil || tt.check == nil || !tt.check(got) {
					t.Fatalf("set %s=%s: %+v, %v", tt.key, tt.value, got, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidRepository) || !strings.Contains(err.Error(), tt.key) {
				t.Errorf("set %s=%s: %v", tt.key, tt.value, err)
			}
		})
	}
}

// Keys that depend on each other are held to each other, so no edit leaves a schedule that runs backwards.
func TestCIKeysAreHeldToEachOther(t *testing.T) {
	if _, err := (CISettings{}).Set("ci_no_history_p50", "30m"); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a p50 past the default p85 is accepted: %v", err)
	}
	if _, err := (CISettings{}).Set("ci_min_interval", "10m"); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a minimum gap past the steady interval is accepted: %v", err)
	}
	if _, err := (CISettings{}).Set("ci_history_window", "5"); err != nil {
		t.Errorf("a window as small as the minimum record count is refused: %v", err)
	}
	if _, err := (CISettings{}).Set("ci_history_min_records", "60"); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a minimum record count past the window is accepted: %v", err)
	}
	both, err := CISettings{}.Set("ci_no_history_p85", "40m")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := both.Set("ci_no_history_p50", "30m"); err != nil || got.NoHistoryP50 != "30m" || got.NoHistoryP85 != "40m" {
		t.Errorf("an edit keeps the keys it does not name: %+v, %v", got, err)
	}
}

// The durations of a custom setting read back as durations.
func TestCIDurationsReadBack(t *testing.T) {
	s, err := CISettings{}.Set("ci_steady_interval", "2m")
	if err != nil {
		t.Fatal(err)
	}
	if s.Steady() != 2*time.Minute || s.Min() != 30*time.Second || s.NoHistory().P50 != 10*time.Minute || s.NoHistory().P85 != 20*time.Minute {
		t.Errorf("durations %s %s %+v", s.Steady(), s.Min(), s.NoHistory())
	}
}
