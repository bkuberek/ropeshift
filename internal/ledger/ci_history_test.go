package ledger

import (
	"testing"
	"time"
)

func minutes(n int) int64 { return (time.Duration(n) * time.Minute).Milliseconds() }

// records builds passing CI records whose push-to-completed times are the given minutes, oldest first.
func records(result CIResult, durations ...int) []CIRecord {
	out := make([]CIRecord, 0, len(durations))
	for i, d := range durations {
		completed := int64(i+1) * minutes(100)
		out = append(out, CIRecord{
			Repository: "r", Head: "h", Attempt: 1, Result: result, PushedAt: completed - minutes(d),
			FirstStart: completed - minutes(d) + minutes(1), LastCompleted: completed, DurationMs: minutes(d), QueueMs: minutes(1),
		})
	}
	return out
}

// A repository's p50 and p85 are the nearest-rank percentiles of the push-to-completed times of its records.
func TestCIHistoryIsTheNearestRankOfPushToCompleted(t *testing.T) {
	stats := DefaultCISettings().Stats(records(CIPass, 7, 1, 9, 3, 5, 10, 2, 8, 4, 6))
	if !stats.Has || stats.Records != 10 {
		t.Fatalf("stats %+v", stats)
	}
	if stats.Duration.P50 != 5*time.Minute || stats.Duration.P85 != 9*time.Minute {
		t.Errorf("the p50 and p85 read %s and %s, and nearest rank of ten gives 5m and 9m", stats.Duration.P50, stats.Duration.P85)
	}
	if stats.Queue.P50 != time.Minute || stats.Queue.P85 != time.Minute {
		t.Errorf("the queue time reads %+v", stats.Queue)
	}
	if history := stats.History(); history == nil || history.P50 != 5*time.Minute || history.P85 != 9*time.Minute {
		t.Errorf("the history reads %+v", history)
	}
}

// A failed run is a test result too, and an infrastructure fault or a run that waits for a person is not.
func TestCIHistoryLeavesOutInfrastructureResults(t *testing.T) {
	settings := DefaultCISettings()
	mixed := append(records(CIPass, 5, 5, 5), records(CIFailure, 5, 5)...)
	if stats := settings.Stats(mixed); !stats.Has || stats.Records != 5 {
		t.Errorf("three passes and two failures read %+v", stats)
	}
	noise := append(records(CIPass, 5, 5, 5, 5), records(CIInfrastructure, 90, 90, 90, 90, 90, 90)...)
	noise = append(noise, records(CIActionRequired, 90, 90)...)
	stats := settings.Stats(noise)
	if stats.Has || stats.Records != 4 || stats.History() != nil {
		t.Errorf("four test results among infrastructure results read %+v", stats)
	}
}

// Fewer records than ci_history_min_records is no history, five by default, and the key is the repository's.
func TestCIHistoryNeedsItsMinimumRecords(t *testing.T) {
	settings := DefaultCISettings()
	if settings.Stats(records(CIPass, 4, 4, 4, 4)).Has {
		t.Error("four records are a history")
	}
	if !settings.Stats(records(CIPass, 4, 4, 4, 4, 4)).Has {
		t.Error("five records are no history")
	}
	three, err := settings.Set("ci_history_min_records", "3")
	if err != nil {
		t.Fatal(err)
	}
	if !three.Stats(records(CIPass, 4, 4, 4)).Has || three.Stats(records(CIPass, 4, 4)).Has {
		t.Error("ci_history_min_records = 3 does not give history after three records and not before")
	}
}

// Only the ci_history_window most recent records count, so a repository whose CI changed reads its new speed.
func TestCIHistoryReadsOnlyTheWindowOfRecentRecords(t *testing.T) {
	old := records(CIPass, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	recent := records(CIPass, 20, 20, 20, 20, 20)
	for i := range recent {
		recent[i].LastCompleted += minutes(10_000)
	}
	settings, err := DefaultCISettings().Set("ci_history_window", "5")
	if err != nil {
		t.Fatal(err)
	}
	stats := settings.Stats(append(old, recent...))
	if stats.Records != 5 || stats.Duration.P50 != 20*time.Minute {
		t.Errorf("a window of five reads %+v", stats)
	}
	if wide := DefaultCISettings().Stats(append(old, recent...)); wide.Records != 15 || wide.Duration.P50 != 1*time.Minute {
		t.Errorf("a window of fifty reads %+v", wide)
	}
}

// The schedule reads the no-history keys until the fifth record, and the repository's own times after it.
func TestTheScheduleMovesToTheRepositorysOwnTimesAfterTheFifthRecord(t *testing.T) {
	settings := DefaultCISettings()
	now := time.Unix(1_000_000, 0)
	running := []CIRun{{Required: true, Status: CIRunInProgress, StartedAt: now}}
	before := settings.Stats(records(CIPass, 2, 2, 2, 2))
	if got := settings.Expectation(before.History()); got.History || got.P50 != 10*time.Minute {
		t.Errorf("four records expect %+v", got)
	}
	if got := settings.NextRead(settings.Expectation(before.History()), running, now).Sub(now); got != 10*time.Minute {
		t.Errorf("four records read at %s, the no-history p50", got)
	}
	after := settings.Stats(records(CIPass, 2, 2, 2, 2, 2))
	if got := settings.Expectation(after.History()); !got.History || got.P50 != 2*time.Minute || got.P85 != 2*time.Minute {
		t.Errorf("five records expect %+v", got)
	}
	if got := settings.NextRead(settings.Expectation(after.History()), running, now).Sub(now); got != 2*time.Minute {
		t.Errorf("five records read at %s, the repository's own p50", got)
	}
}

// Each conclusion a forge gives has one class, and a class that is no test result stays out of the history.
func TestEachConclusionHasOneClass(t *testing.T) {
	for conclusion, want := range map[string]CIResult{
		"success": CIPass, "neutral": CIPass, "skipped": CIPass, "failure": CIFailure,
		"cancelled": CIInfrastructure, "timed_out": CIInfrastructure, "startup_failure": CIInfrastructure, "stale": CIInfrastructure,
		"action_required": CIActionRequired, "": CIFailure, "something_new": CIInfrastructure,
	} {
		if got := ClassifyConclusion(conclusion); got != want {
			t.Errorf("%q is %q, want %q", conclusion, got, want)
		}
	}
	if !CIPass.TestResult() || !CIFailure.TestResult() || CIInfrastructure.TestResult() || CIActionRequired.TestResult() {
		t.Error("only a pass and a failure are test results")
	}
}

func check(name, conclusion string, state ForgeCheckState, started, completed string) PullRequestCheck {
	return PullRequestCheck{Name: name, Required: true, State: state, Status: CIRunCompleted, Conclusion: conclusion, StartedAt: started, CompletedAt: completed}
}

// A record is made when every required check is final: its push, first start, last completion, queue time, and result.
func TestACIRecordIsMadeWhenEveryRequiredCheckIsFinal(t *testing.T) {
	push := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	at := func(m int) string { return push.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	checks := []PullRequestCheck{
		check("build", "success", CheckPassed, at(3), at(11)), check("unit", "success", CheckPassed, at(2), at(14)),
		{Name: "optional", State: CheckFailed, Status: CIRunCompleted, Conclusion: "failure", StartedAt: at(1), CompletedAt: at(30)},
	}
	record, ok := CIRecordOf("r", "abc", push.UnixMilli(), checks, push.Add(time.Hour).UnixMilli())
	if !ok {
		t.Fatal("no record from final required checks")
	}
	if record.Result != CIPass || record.QueueMs != minutes(2) || record.DurationMs != minutes(14) ||
		record.FirstStart != push.Add(2*time.Minute).UnixMilli() || record.LastCompleted != push.Add(14*time.Minute).UnixMilli() {
		t.Errorf("record %+v", record)
	}
	pending := append([]PullRequestCheck{}, checks...)
	pending[0] = PullRequestCheck{Name: "build", Required: true, State: CheckPending, Status: CIRunInProgress, StartedAt: at(3)}
	if _, ok := CIRecordOf("r", "abc", push.UnixMilli(), pending, 0); ok {
		t.Error("a record was made while a required check ran")
	}
	if _, ok := CIRecordOf("r", "abc", push.UnixMilli(), []PullRequestCheck{{Name: "optional", State: CheckPassed}}, 0); ok {
		t.Error("a record was made for a head with no required check")
	}
}

// A failure beside runs the forge cancelled because of it is a test failure, and a clock that disagrees with the
// forge's makes no negative time.
func TestACIRecordClassifiesByTheWorstTestResultAndNeverGoesNegative(t *testing.T) {
	push := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	at := func(m int) string { return push.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	checks := []PullRequestCheck{check("unit", "failure", CheckFailed, at(1), at(4)), check("lint", "cancelled", CheckFailed, at(1), at(4))}
	record, ok := CIRecordOf("r", "abc", push.UnixMilli(), checks, 0)
	if !ok || record.Result != CIFailure {
		t.Errorf("a failure beside a cancelled run reads %+v, %v", record, ok)
	}
	infra, _ := CIRecordOf("r", "abc", push.UnixMilli(), []PullRequestCheck{check("unit", "timed_out", CheckFailed, at(1), at(40))}, 0)
	if infra.Result != CIInfrastructure {
		t.Errorf("a timed out run reads %+v", infra)
	}
	late, _ := CIRecordOf("r", "abc", push.Add(time.Hour).UnixMilli(), []PullRequestCheck{check("unit", "success", CheckPassed, at(1), at(4))}, 0)
	if late.QueueMs != 0 || late.DurationMs != minutes(3) || late.PushedAt != push.Add(time.Minute).UnixMilli() {
		t.Errorf("a head seen after its checks started reads %+v", late)
	}
	unclocked, _ := CIRecordOf("r", "abc", push.UnixMilli(), []PullRequestCheck{{Name: "unit", Required: true, State: CheckPassed}}, push.Add(5*time.Minute).UnixMilli())
	if unclocked.LastCompleted != push.Add(5*time.Minute).UnixMilli() || unclocked.DurationMs != minutes(5) {
		t.Errorf("a check with no times reads %+v", unclocked)
	}
}
