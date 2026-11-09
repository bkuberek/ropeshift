package contract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

// documentedRows are the qualification matrix's rows these fixtures stand for. None of them was
// observed at a limit, so each fixture stays documented only until a real run gives evidence.
var documentedRows = []string{
	"`StopFailure` with `rate_limit`",
	"`terminal_reason` `blocking_limit`",
	"`rate_limit_event` `status` `allowed_warning` and `rejected`",
	"`system/api_retry` with `retry_delay_ms`",
	"`rate_limits.five_hour`",
	"`unifiedWindows.<window>.utilization`",
}

// TestEveryLimitFixtureNamesItsMatrixRow keeps the shapes honest: each says which row it stands for
// and how much is known of it, so a real run can correct it.
func TestEveryLimitFixtureNamesItsMatrixRow(t *testing.T) {
	seen := 0
	for name, raw := range doubles.Fixtures(t) {
		if !strings.HasPrefix(name, "limit-") && !strings.HasPrefix(name, "signin-") {
			continue
		}
		seen++
		var fixture struct {
			Source string `json:"source"`
			Matrix string `json:"matrix"`
		}
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(documentedRows, fixture.Matrix) {
			t.Errorf("%s names no row of the matrix: %q", name, fixture.Matrix)
		}
		if fixture.Source == "" {
			t.Errorf("%s says nothing of where it came from", name)
		}
	}
	if seen == 0 {
		t.Error("no fixture read")
	}
}

func (s *driverSession) untilLimit() ports.DriverEvent {
	s.t.Helper()
	event := s.until(ports.EventLimit)
	if event.Usage == nil {
		s.t.Fatalf("a limit with no reading: %+v", event)
	}
	return event
}

func aheadOfNow(t *testing.T, at int64) {
	t.Helper()
	now := time.Now().UnixMilli()
	if at < now+30*60*1000 || at > now+2*60*60*1000 {
		t.Errorf("a reset read as %d, %d ms from now", at, at-now)
	}
}

func TestARejectedEventNamesItsReset(t *testing.T) {
	r := newPeerRig(t, "limit-rejected")
	session := r.start()
	got := session.untilLimit().Usage
	if got.Status != lanes.UsageRejected || got.Window != "five_hour" || got.UsedKnown {
		t.Errorf("reading %+v", got)
	}
	aheadOfNow(t, got.ResetsAtMs)
}

func TestAWarningCarriesItsFigure(t *testing.T) {
	r := newPeerRig(t, "limit-warning")
	session := r.start()
	got := session.untilLimit().Usage
	if got.Status != lanes.UsageWarning || !got.UsedKnown || got.UsedPercent != 93 {
		t.Errorf("reading %+v", got)
	}
}

// TestUnifiedWindowsCountOnlyOnAQualifiedVersion holds the highest window on a version that passed
// the check, and nothing on one that did not.
func TestUnifiedWindowsCountOnlyOnAQualifiedVersion(t *testing.T) {
	qualified := newPeerRig(t, "limit-windows").start().untilLimit().Usage
	if !qualified.UsedKnown || qualified.UsedPercent != 91 {
		t.Errorf("a qualified version reads %+v", qualified)
	}
	unqualified := newPeerRig(t, "limit-windows-unqualified").start().untilLimit().Usage
	if unqualified.UsedKnown {
		t.Errorf("an unqualified version reads %+v", unqualified)
	}
	aheadOfNow(t, unqualified.ResetsAtMs)
}

func TestAnAPIRetryNamesItsError(t *testing.T) {
	r := newPeerRig(t, "limit-retry")
	session := r.start()
	event := session.until(ports.EventRetry)
	if event.Detail != lanes.FailureUsageLimit || event.Attempt != 3 {
		t.Errorf("event %+v", event)
	}
}

func TestAnExpiredSignInNamesItself(t *testing.T) {
	r := newPeerRig(t, "signin-expired")
	session := r.start()
	if event := session.until(ports.EventRetry); event.Detail != lanes.FailureSignIn {
		t.Errorf("event %+v", event)
	}
}

func TestABlockingLimitEndsATurnOnTheLimit(t *testing.T) {
	r := newPeerRig(t, "limit-blocking")
	session := r.start()
	turn := session.until(ports.EventTurnEnded).Turn
	if turn.Status != ports.TurnFailed || turn.Reason != lanes.FailureUsageLimit {
		t.Errorf("turn %+v", turn)
	}
}

func TestAPaneReadsTheWindowItIsGiven(t *testing.T) {
	r := newPaneRig(t, "limit-status-line")
	session := r.start()
	got := session.untilLimit().Usage
	if !got.UsedKnown || got.UsedPercent != 91 || got.Window != "five_hour" {
		t.Errorf("reading %+v", got)
	}
	aheadOfNow(t, got.ResetsAtMs)
}

func TestAPaneTurnAtALimitFails(t *testing.T) {
	r := newPaneRig(t, "limit-stop-failure")
	session := r.start()
	turn := session.until(ports.EventTurnEnded).Turn
	if turn.Status != ports.TurnFailed || turn.Reason != lanes.FailureUsageLimit {
		t.Errorf("turn %+v", turn)
	}
}

func TestGeneratedSettingsLeaveTheWaitToTheWorker(t *testing.T) {
	r := newPeerRig(t, "lane-replies")
	session := r.start()
	session.until(ports.EventSessionBound)
	settings, _ := filepath.Glob(filepath.Join(r.spec.ConfigDir, "*settings*.json"))
	if len(settings) == 0 {
		t.Fatal("no generated settings")
	}
	raw, err := os.ReadFile(settings[0])
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]any
	if err := json.Unmarshal(raw, &generated); err != nil {
		t.Fatal(err)
	}
	if generated["autoContinueAtUsageLimit"] != false {
		t.Errorf("generated %v", generated["autoContinueAtUsageLimit"])
	}
}

// TestASessionHasNoRetryWatchdog is the other half of a worker owning the wait: nothing sets it,
// and a value in the worker's own environment does not reach a session.
func TestASessionHasNoRetryWatchdog(t *testing.T) {
	t.Setenv("CLAUDE_CODE_RETRY_WATCHDOG", "1")
	r := newPeerRig(t, "lane-replies")
	session := r.start()
	session.until(ports.EventSessionBound)
	if value := doubles.SeenEnv(t, r.path, "CLAUDE_CODE_RETRY_WATCHDOG"); value != "" {
		t.Errorf("a session saw %q", value)
	}
}

func TestUsageIsReadWithNoSession(t *testing.T) {
	r := newPeerRig(t, "limit-usage")
	got, err := r.driver.ReadUsage(t.Context(), r.spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Current session") {
		t.Errorf("read %q", got)
	}
}

func TestAPaneCannotReadUsage(t *testing.T) {
	r := newPaneRig(t, "limit-status-line")
	_, err := r.driver.ReadUsage(t.Context(), r.spec)
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Errorf("a pane read: %v", err)
	}
}
