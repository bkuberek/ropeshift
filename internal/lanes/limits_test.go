package lanes_test

import (
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/parking"
)

func TestAReadingNearTheLimit(t *testing.T) {
	const now = int64(1_000_000)
	for name, tt := range map[string]struct {
		reading lanes.UsageReading
		want    bool
	}{
		"nothing read":           {lanes.UsageReading{}, false},
		"allowed with no figure": {lanes.UsageReading{Status: lanes.UsageAllowed}, false},
		"a vendor's warning":     {lanes.UsageReading{Status: lanes.UsageWarning}, true},
		"under the mark":         {lanes.UsageReading{UsedPercent: 89.9, UsedKnown: true}, false},
		"at the mark":            {lanes.UsageReading{UsedPercent: lanes.NearLimitPercent, UsedKnown: true}, true},
		"a warning past reset":   {lanes.UsageReading{Status: lanes.UsageWarning, ResetsAtMs: now - 1}, false},
		"a figure past reset":    {lanes.UsageReading{UsedPercent: 99, UsedKnown: true, ResetsAtMs: now}, false},
		"a warning before it":    {lanes.UsageReading{Status: lanes.UsageWarning, ResetsAtMs: now + 1}, true},
	} {
		if got := tt.reading.Near(now); got != tt.want {
			t.Errorf("%s: near %v", name, got)
		}
	}
}

func TestAResetIsOnlyEverAhead(t *testing.T) {
	const now = int64(1_000_000)
	for name, tt := range map[string]struct {
		at   int64
		want int64
	}{
		"none":   {0, 0},
		"behind": {now - 1, 0},
		"now":    {now, 0},
		"ahead":  {now + 1, now + 1},
	} {
		if got := (lanes.UsageReading{ResetsAtMs: tt.at}).Reset(now); got != tt.want {
			t.Errorf("%s: %d", name, got)
		}
	}
}

// TestUsageBackoffIsCapped is the wait with no reset time: it grows, it stops growing, and a
// figure too large to double cannot wrap to nothing.
func TestUsageBackoffIsCapped(t *testing.T) {
	base := 5 * time.Minute
	for tries, want := range map[int64]time.Duration{
		0: base, 1: 2 * base, 2: 4 * base, 3: 8 * base, 4: lanes.MaxUsageBackoff, 200: lanes.MaxUsageBackoff,
	} {
		if got := lanes.UsageBackoff(base, tries); got != want {
			t.Errorf("after %d tries: %s, want %s", tries, got, want)
		}
	}
	if got := lanes.UsageBackoff(0, 0); got != lanes.DefaultUsageBackoff {
		t.Errorf("no base: %s", got)
	}
}

// TestAUsageWakeNamesItsSignIn reads a wake the way the ledger does, so one signal or one time
// ends it, and a lane with no time waits on the signal alone.
func TestAUsageWakeNamesItsSignIn(t *testing.T) {
	const key = "worker-1:claude-code"
	with, err := parking.Parse(lanes.UsageWake(key, 5_000), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	fact := parking.Fact{Kind: parking.UsageReset, Key: key}
	if _, timed := with.FirstTime(); !with.Names(parking.UsageReset) || !timed {
		t.Errorf("the wake names %+v", with.Facts())
	}
	if !with.Holds(parking.Facts{fact: true}, 0) {
		t.Error("a signal did not end the wake")
	}
	if !with.Holds(parking.Facts{}, 5_000) {
		t.Error("the time did not end the wake")
	}
	if with.Holds(parking.Facts{}, 4_999) {
		t.Error("a wake ended before its time")
	}
	without, err := parking.Parse(lanes.UsageWake(key, 0), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if at, timed := without.FirstTime(); timed {
		t.Errorf("a wake with no time names %d", at)
	}
}

func TestASignInWakeWaitsOnOneFact(t *testing.T) {
	wake, err := parking.Parse(lanes.SignInWake("worker-1:claude-code"), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if !wake.Holds(parking.Facts{{Kind: parking.SigninRestored, Key: "worker-1:claude-code"}: true}, 0) {
		t.Errorf("the wake reads %+v", wake.Facts())
	}
}

// TestAStatusLineNamesItsFullestWindow assumes no window: whichever the build reports, the fullest
// is the one that stops a lane, and a line with none says nothing of a limit.
func TestAStatusLineNamesItsFullestWindow(t *testing.T) {
	for name, tt := range map[string]struct {
		input string
		want  *lanes.UsageReading
	}{
		"no limits at all":        {`{}`, nil},
		"a window with no figure": {`{"rate_limits":{"seven_day":{"resets_at":100}}}`, nil},
		"one window": {`{"rate_limits":{"seven_day":{"used_percentage":12,"resets_at":100}}}`,
			&lanes.UsageReading{Window: "seven_day", UsedPercent: 12, UsedKnown: true, ResetsAtMs: 100_000}},
		"the fuller of two": {`{"rate_limits":{"five_hour":{"used_percentage":91,"resets_at":50},"seven_day":{"used_percentage":12,"resets_at":100}}}`,
			&lanes.UsageReading{Window: "five_hour", UsedPercent: 91, UsedKnown: true, ResetsAtMs: 50_000}},
		"a tie reads in name order": {`{"rate_limits":{"seven_day":{"used_percentage":40,"resets_at":100},"five_hour":{"used_percentage":40,"resets_at":50}}}`,
			&lanes.UsageReading{Window: "five_hour", UsedPercent: 40, UsedKnown: true, ResetsAtMs: 50_000}},
		"zero is a figure": {`{"rate_limits":{"seven_day":{"used_percentage":0,"resets_at":100}}}`,
			&lanes.UsageReading{Window: "seven_day", UsedKnown: true, ResetsAtMs: 100_000}},
	} {
		got := lanes.NewStatusEvent([]byte(tt.input)).Status.Usage
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("%s: got %+v, want %+v", name, got, tt.want)
		}
	}
}

func TestASignInKeyIsItsWorkers(t *testing.T) {
	key := parking.SignInKey("worker-1", "claude-code")
	for name, tt := range map[string]struct {
		worker, key string
		want        bool
	}{
		"its own":                 {"worker-1", key, true},
		"another's":               {"worker-2", key, false},
		"a worker named like it":  {"worker", key, false},
		"a worker with no driver": {"worker-1", "worker-1:", false},
		"a key with no worker":    {"worker-1", "claude-code", false},
	} {
		if got := parking.SignInKeyOf(tt.worker, tt.key); got != tt.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestAdmissionAtALimit(t *testing.T) {
	for name, tt := range map[string]struct {
		facts lanes.AdmissionFacts
		want  string
	}{
		"a new lane, signed out":        {lanes.AdmissionFacts{SignedOut: true}, lanes.HeldSignedOut},
		"a woken lane, signed out":      {lanes.AdmissionFacts{SignedOut: true, Woken: true}, lanes.HeldSignedOut},
		"a new lane at a limit":         {lanes.AdmissionFacts{Limited: true}, lanes.HeldUsageLimit},
		"a woken lane at a limit":       {lanes.AdmissionFacts{Limited: true, Woken: true}, lanes.HeldUsageLimit},
		"the probe at a limit":          {lanes.AdmissionFacts{Limited: true, Woken: true, Probe: true}, ""},
		"a new lane near the limit":     {lanes.AdmissionFacts{NearLimit: true}, lanes.HeldNearLimit},
		"a woken lane near the limit":   {lanes.AdmissionFacts{NearLimit: true, Woken: true}, ""},
		"a pause before all of it":      {lanes.AdmissionFacts{Mode: lanes.FleetPause, SignedOut: true}, lanes.HeldFleetPaused},
		"caps after all of it":          {lanes.AdmissionFacts{Limited: true, Running: 1, Limits: lanes.Admission{MaxLanes: 1}}, lanes.HeldUsageLimit},
		"a woken lane still takes caps": {lanes.AdmissionFacts{Woken: true, Running: 2, Limits: lanes.Admission{MaxLanesPerSignIn: 2}}, lanes.HeldSignInFull},
		"the probe still takes caps":    {lanes.AdmissionFacts{Limited: true, Woken: true, Probe: true, Running: 1, Limits: lanes.Admission{MaxLanes: 1}}, lanes.HeldWorkerFull},
	} {
		if got := lanes.Admit(tt.facts); got != tt.want {
			t.Errorf("%s: %q, want %q", name, got, tt.want)
		}
	}
}
