package ledger

import (
	"testing"
	"time"
)

// watchPush is when the ledger first held the head the tests watch, in database milliseconds, and at is a moment
// after it.
const watchPush = int64(1_000_000_000)

func at(offset time.Duration) int64 { return watchPush + offset.Milliseconds() }

func openPR(head string, checks ...PullRequestCheck) WorkPullRequest {
	return WorkPullRequest{
		PullRequest: PullRequest{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: PullRequestOpen, HeadCommit: head, Checks: checks},
		WorkOrder:   "wo-1", Repository: "repo-1", HeadSince: watchPush,
	}
}

func queuedRun() PullRequestCheck {
	return PullRequestCheck{Name: "CI", State: CheckPending, Status: CIRunQueued, Required: true, DetailsURI: "https://github.com/acme/widgets/actions/runs/9"}
}

func runningRun(started int64) PullRequestCheck {
	return PullRequestCheck{
		Name: "CI", State: CheckPending, Status: CIRunInProgress, Required: true, StartedAt: time.UnixMilli(started).UTC().Format(time.RFC3339),
		DetailsURI: "https://github.com/acme/widgets/actions/runs/9",
	}
}

// A repository with no history times CI out at ci_timeout_no_history, 3 hours, and a run before it raises nothing.
func TestACIRunRaisesNothingBeforeItsTimeoutAndNotFinishedAtIt(t *testing.T) {
	var settings CISettings
	pr := openPR("h1", runningRun(at(time.Minute)))
	early := settings.Watch(pr, CIStats{}, CIWaiting{}, at(3*time.Hour-time.Second))
	if early.Kind != "" || early.History || early.TimeoutMs != (3*time.Hour).Milliseconds() || early.TimeoutAt != at(3*time.Hour) {
		t.Errorf("a run just under 3 hours reads %+v", early)
	}
	late := settings.Watch(pr, CIStats{}, CIWaiting{}, at(3*time.Hour))
	if late.Kind != CIItemNotFinished || late.RunURL != "https://github.com/acme/widgets/actions/runs/9" || late.ElapsedMs != (3*time.Hour).Milliseconds() {
		t.Errorf("a run at 3 hours reads %+v", late)
	}
}

// With history the limit is the multiplier times the repository's p85 of push-to-completed time, and editing the
// multiplier moves the time the item fires at.
func TestTheCITimeoutIsTheMultipleOfTheRepositorysP85AndEditingItMovesIt(t *testing.T) {
	stats := CIStats{Records: 20, Has: true, Duration: CIDurations{P50: 8 * time.Minute, P85: 12 * time.Minute}, Queue: CIDurations{P50: time.Minute, P85: 2 * time.Minute}}
	pr := openPR("h1", runningRun(at(0)))
	var settings CISettings
	watch := settings.Watch(pr, stats, CIWaiting{}, at(time.Minute))
	if !watch.History || watch.ExpectedMs != (12*time.Minute).Milliseconds() || watch.ExpectedP50Ms != (8*time.Minute).Milliseconds() || watch.TimeoutAt != at(36*time.Minute) {
		t.Errorf("3 times a p85 of 12 minutes reads %+v", watch)
	}
	edited, err := settings.Set("ci_timeout_multiplier", "5")
	if err != nil {
		t.Fatal(err)
	}
	if moved := edited.Watch(pr, stats, CIWaiting{}, at(time.Minute)); moved.TimeoutAt != at(time.Hour) {
		t.Errorf("a multiplier of 5 fires at %d, want %d", moved.TimeoutAt, at(time.Hour))
	}
	if limit := edited.Watch(pr, stats, CIWaiting{}, at(59*time.Minute)); limit.Kind != "" {
		t.Errorf("a run under the edited limit raises %q", limit.Kind)
	}
}

// A limit read from a very fast history never drops under the least ci_timeout_no_history allows, so a repository
// whose CI takes seconds is not alarmed by a slow minute.
func TestAHistoryOfFastCINeverTimesOutUnderTheKeysFloor(t *testing.T) {
	stats := CIStats{Records: 20, Has: true, Duration: CIDurations{P50: time.Second, P85: 2 * time.Second}, Queue: CIDurations{}}
	var settings CISettings
	watch := settings.Watch(openPR("h1", runningRun(at(0))), stats, CIWaiting{}, at(time.Second))
	if watch.TimeoutMs != (10*time.Minute).Milliseconds() || watch.QueueTimeoutMs != time.Minute.Milliseconds() {
		t.Errorf("the limits of a history of seconds read %+v", watch)
	}
}

// A limit read from a history of very slow CI never rises past the most ci_timeout_no_history allows, 72 hours, and a
// queue limit read from a very slow queue never passes the most ci_queue_timeout_no_history allows, 24 hours, so a
// history of days cannot silence the item for weeks. What Wait longer adds goes on top of the cap.
func TestAHistoryOfSlowCINeverTimesOutPastTheKeysCeiling(t *testing.T) {
	stats := CIStats{Records: 20, Has: true, Duration: CIDurations{P50: 40 * time.Hour, P85: 50 * time.Hour}, Queue: CIDurations{P50: 8 * time.Hour, P85: 12 * time.Hour}}
	var settings CISettings
	watch := settings.Watch(openPR("h1", runningRun(at(0))), stats, CIWaiting{}, at(time.Hour))
	if watch.TimeoutMs != (72*time.Hour).Milliseconds() || watch.QueueTimeoutMs != (24*time.Hour).Milliseconds() {
		t.Errorf("three times a p85 of 50 hours and of 12 hours read %+v", watch)
	}
	waited := settings.Watch(openPR("h1", runningRun(at(0))), stats, CIWaiting{ExtendedMs: time.Hour.Milliseconds()}, at(time.Hour))
	if waited.TimeoutMs != (73 * time.Hour).Milliseconds() {
		t.Errorf("a wait of an hour past the cap reads %d ms", waited.TimeoutMs)
	}
	edited, err := settings.Set("ci_timeout_multiplier", "20")
	if err != nil {
		t.Fatal(err)
	}
	if capped := edited.Watch(openPR("h1", queuedRun()), stats, CIWaiting{}, at(0)); capped.TimeoutMs != (72*time.Hour).Milliseconds() || capped.QueueTimeoutAt != at(24*time.Hour) {
		t.Errorf("the largest multiplier reads %+v", capped)
	}
}

// Wait longer adds the amount it was pressed with to the limit, once for each press.
func TestWaitingLongerExtendsTheTimeout(t *testing.T) {
	var settings CISettings
	pr := openPR("h1", runningRun(at(0)))
	waited := CIWaiting{ExtendedMs: (90 * time.Minute).Milliseconds()}
	if watch := settings.Watch(pr, CIStats{}, waited, at(3*time.Hour)); watch.Kind != "" || watch.TimeoutAt != at(4*time.Hour+30*time.Minute) || watch.ExtendedMs != waited.ExtendedMs {
		t.Errorf("a run at 3 hours after a wait of 90 minutes reads %+v", watch)
	}
	if watch := settings.Watch(pr, CIStats{}, waited, at(4*time.Hour+30*time.Minute)); watch.Kind != CIItemNotFinished {
		t.Errorf("a run at the extended limit reads %+v", watch)
	}
}

// The run's address is the forge's word, so only an http or https address with a host and nothing a page could be
// tricked by is offered as a link.
func TestOnlyASafeAddressOfARunIsALink(t *testing.T) {
	for address, want := range map[string]string{
		"https://github.com/acme/widgets/actions/runs/9": "https://github.com/acme/widgets/actions/runs/9",
		"http://forge.example/runs/9":                    "http://forge.example/runs/9",
		"javascript:alert(1)":                            "",
		"ftp://forge.example/runs/9":                     "",
		"https://":                                       "",
		"https://user@forge.example/runs/9":              "",
		"https://forge.example/runs/9 now":               "",
		"https://forge.example\\@evil.example":           "",
		"":                                               "",
	} {
		if got := linkable(address); got != want {
			t.Errorf("%q is linked as %q, want %q", address, got, want)
		}
	}
}

// A rerun starts the head's clocks again from the rerun, so the timeout is counted from it and not from the push.
func TestARerunStartsTheCIClockAgain(t *testing.T) {
	var settings CISettings
	pr := openPR("h1", runningRun(at(0)))
	rerun := CIWaiting{Reruns: 1, RerunAt: at(3 * time.Hour)}
	watch := settings.Watch(pr, CIStats{}, rerun, at(3*time.Hour+time.Minute))
	if watch.Kind != "" || watch.ElapsedMs != time.Minute.Milliseconds() || watch.TimeoutAt != at(6*time.Hour) || watch.Reruns != 1 {
		t.Errorf("a rerun a minute ago reads %+v", watch)
	}
	if again := settings.Watch(pr, CIStats{}, rerun, at(6*time.Hour)); again.Kind != CIItemNotFinished {
		t.Errorf("a rerun that ran 3 hours reads %+v", again)
	}
}

// A queued required run raises only the stalled queue, at its own limit, and never "CI has not finished", even
// when the CI timeout is the shorter of the two.
func TestAQueuedRunRaisesOnlyAStalledQueue(t *testing.T) {
	var settings CISettings
	settings, err := settings.Set("ci_queue_timeout_no_history", "6h")
	if err != nil {
		t.Fatal(err)
	}
	settings, err = settings.Set("ci_timeout_no_history", "10m")
	if err != nil {
		t.Fatal(err)
	}
	pr := openPR("h1", queuedRun())
	for _, offset := range []time.Duration{time.Hour, 5*time.Hour + 59*time.Minute} {
		if watch := settings.Watch(pr, CIStats{}, CIWaiting{}, at(offset)); watch.Kind != "" || !watch.Queued {
			t.Errorf("a run queued for %s reads %+v", offset, watch)
		}
	}
	watch := settings.Watch(pr, CIStats{}, CIWaiting{}, at(6*time.Hour))
	if watch.Kind != CIItemQueueStalled || watch.QueueMs != (6*time.Hour).Milliseconds() || watch.QueueTimeoutAt != at(6*time.Hour) {
		t.Errorf("a run queued for 6 hours reads %+v", watch)
	}
}

// With history the queue limit is the multiplier times the p85 of queue time, and editing ci_queue_timeout_no_history
// moves it only for a repository with none.
func TestTheQueueLimitReadsHistoryAndTheNoHistoryKey(t *testing.T) {
	var settings CISettings
	pr := openPR("h1", queuedRun())
	history := CIStats{Records: 9, Has: true, Duration: CIDurations{P85: 10 * time.Minute}, Queue: CIDurations{P50: time.Minute, P85: 4 * time.Minute}}
	if watch := settings.Watch(pr, history, CIWaiting{}, at(0)); watch.QueueTimeoutAt != at(12*time.Minute) {
		t.Errorf("3 times a queue p85 of 4 minutes fires at %d, want %d", watch.QueueTimeoutAt, at(12*time.Minute))
	}
	if watch := settings.Watch(pr, CIStats{}, CIWaiting{}, at(0)); watch.QueueTimeoutAt != at(30*time.Minute) {
		t.Errorf("no history fires at %d, want %d", watch.QueueTimeoutAt, at(30*time.Minute))
	}
	edited, err := settings.Set("ci_queue_timeout_no_history", "45m")
	if err != nil {
		t.Fatal(err)
	}
	if watch := edited.Watch(pr, CIStats{}, CIWaiting{}, at(0)); watch.QueueTimeoutAt != at(45*time.Minute) {
		t.Errorf("an edited no-history queue limit fires at %d, want %d", watch.QueueTimeoutAt, at(45*time.Minute))
	}
	if watch := edited.Watch(pr, history, CIWaiting{}, at(0)); watch.QueueTimeoutAt != at(12*time.Minute) {
		t.Errorf("a repository with history ignores the no-history key: fires at %d", watch.QueueTimeoutAt)
	}
}

// Finished checks, a pull request that is not open, and a head with no required check raise nothing, and a finished
// head reads the time it took.
func TestFinishedClosedAndUnrequiredHeadsRaiseNothing(t *testing.T) {
	var settings CISettings
	done := PullRequestCheck{
		Name: "CI", State: CheckPassed, Status: CIRunCompleted, Required: true, StartedAt: time.UnixMilli(at(time.Minute)).UTC().Format(time.RFC3339),
		CompletedAt: time.UnixMilli(at(9 * time.Minute)).UTC().Format(time.RFC3339),
	}
	finished := settings.Watch(openPR("h1", done), CIStats{}, CIWaiting{}, at(10*time.Hour))
	if finished.Kind != "" || !finished.Final || finished.ElapsedMs != (9*time.Minute).Milliseconds() || finished.QueueMs != time.Minute.Milliseconds() {
		t.Errorf("a finished head reads %+v", finished)
	}
	closed := openPR("h1", runningRun(at(0)))
	closed.State = PullRequestClosed
	if watch := settings.Watch(closed, CIStats{}, CIWaiting{}, at(10*time.Hour)); watch.Kind != "" {
		t.Errorf("a closed pull request reads %+v", watch)
	}
	optional := runningRun(at(0))
	optional.Required = false
	if watch := settings.Watch(openPR("h1", optional), CIStats{}, CIWaiting{}, at(10*time.Hour)); watch.Kind != "" || watch.Watching {
		t.Errorf("a head with no required check reads %+v", watch)
	}
}
