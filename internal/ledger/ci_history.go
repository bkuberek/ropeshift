package ledger

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// CIResult is the class of a head's required checks when they all finished (ADR-0037). The class decides whether
// an agent spends at all, so it is fixed and no setting moves it.
type CIResult string

// The classes of a finished head.
const (
	// CIPass says every required check passed, as the forge counts it.
	CIPass CIResult = "pass"
	// CIFailure says a required check failed, which a rework lane can fix.
	CIFailure CIResult = "failure"
	// CIInfrastructure says the only failures were a runner or a timeout, which no agent can fix.
	CIInfrastructure CIResult = "infrastructure"
	// CIActionRequired says a person on the forge has to act before a check runs.
	CIActionRequired CIResult = "action_required"
)

// TestResult says the head's checks ran and gave an answer about the code, pass or fail. Only those say how long
// CI takes, and a fault in a runner says nothing about it.
func (r CIResult) TestResult() bool { return r == CIPass || r == CIFailure }

// ClassifyConclusion is the class of one finished check's conclusion, as the forge words it in lower case. A
// failure the forge gives no conclusion for, a commit status's, is a failure. A conclusion this build does not
// know is infrastructure, which never starts an agent.
func ClassifyConclusion(conclusion string) CIResult {
	switch conclusion {
	case "success", "neutral", "skipped":
		return CIPass
	case "failure", "":
		return CIFailure
	case "action_required":
		return CIActionRequired
	}
	return CIInfrastructure
}

// CIRecord is what one head's required checks took, kept when they all finished (FR-SPEND-21). Times are database
// milliseconds. PushedAt is when ropeshift first held the head, or the first start when that was earlier, which
// is the best push time a control plane can know. QueueMs is the wait from the push to the first required run's
// start, and DurationMs the time from the push to the last completion.
type CIRecord struct {
	Repository    string   `json:"repository"`
	Head          string   `json:"head"`
	Attempt       int64    `json:"attempt"`
	PushedAt      int64    `json:"pushed_at"`
	FirstStart    int64    `json:"first_start"`
	LastCompleted int64    `json:"last_completed"`
	QueueMs       int64    `json:"queue_ms"`
	DurationMs    int64    `json:"duration_ms"`
	Result        CIResult `json:"result"`
}

func millis(text string) (int64, bool) {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return 0, false
	}
	return at.UnixMilli(), true
}

// CIRecordOf is the record of a head whose required checks all finished, and false while any is still to finish
// or when no check is required, because with no required check there is no CI to time. pushedAt is when the
// ledger first held the head, and observedAt when the read that found the checks final was made, which stands in
// for a completion time the forge did not give. The caller names the attempt.
func CIRecordOf(repository, head string, pushedAt int64, checks []PullRequestCheck, observedAt int64) (CIRecord, bool) {
	var required []PullRequestCheck
	for _, check := range checks {
		if !check.Required {
			continue
		}
		if check.State == CheckPending {
			return CIRecord{}, false
		}
		required = append(required, check)
	}
	if len(required) == 0 {
		return CIRecord{}, false
	}
	var first, last int64
	result := CIPass
	for _, check := range required {
		if at, ok := millis(check.StartedAt); ok && (first == 0 || at < first) {
			first = at
		}
		if at, ok := millis(check.CompletedAt); ok && at > last {
			last = at
		}
		if check.State != CheckFailed {
			continue
		}
		switch class := ClassifyConclusion(check.Conclusion); {
		case class == CIFailure || result == CIFailure:
			result = CIFailure
		case class == CIInfrastructure || result == CIInfrastructure:
			result = CIInfrastructure
		default:
			result = class
		}
	}
	if first != 0 && first < pushedAt {
		pushedAt = first
	}
	if first == 0 {
		first = pushedAt
	}
	if last == 0 {
		last = observedAt
	}
	return CIRecord{
		Repository: repository, Head: head, PushedAt: pushedAt, FirstStart: first, LastCompleted: last,
		QueueMs: max(first-pushedAt, 0), DurationMs: max(last-pushedAt, 0), Result: result,
	}, true
}

// CIStats is what a repository's recent CI records say: how many of them had a test result, their p50 and p85 of
// push-to-completed time and of queue time by nearest rank, and whether there are enough of them to be a history.
type CIStats struct {
	Records  int
	Has      bool
	Duration CIDurations
	Queue    CIDurations
}

// History is the expected duration the schedule reads, or nil when the repository has too few records to have one.
func (s CIStats) History() *CIHistory {
	if !s.Has {
		return nil
	}
	history := CIHistory(s.Duration)
	return &history
}

// Stats summarizes records by the repository's keys: the `ci_history_window` most recent with a test result,
// where fewer than `ci_history_min_records` is no history.
func (s CISettings) Stats(records []CIRecord) CIStats {
	e := s.Effective()
	usable := make([]CIRecord, 0, len(records))
	for _, record := range records {
		if record.Result.TestResult() {
			usable = append(usable, record)
		}
	}
	slices.SortStableFunc(usable, func(a, b CIRecord) int { return cmp.Compare(b.LastCompleted, a.LastCompleted) })
	if int64(len(usable)) > e.HistoryWindow {
		usable = usable[:e.HistoryWindow]
	}
	stats := CIStats{Records: len(usable), Has: int64(len(usable)) >= e.HistoryMinRecords && len(usable) > 0}
	if len(usable) == 0 {
		return stats
	}
	stats.Duration = durationsOf(usable, func(r CIRecord) int64 { return r.DurationMs })
	stats.Queue = durationsOf(usable, func(r CIRecord) int64 { return r.QueueMs })
	return stats
}

func durationsOf(records []CIRecord, of func(CIRecord) int64) CIDurations {
	values := make([]int64, 0, len(records))
	for _, r := range records {
		values = append(values, of(r))
	}
	slices.Sort(values)
	return CIDurations{
		P50: time.Duration(values[nearestRank(len(values), 0.5)]) * time.Millisecond,
		P85: time.Duration(values[nearestRank(len(values), 0.85)]) * time.Millisecond,
	}
}

// nearestRank is the index of a percentile by nearest rank among n sorted values, as the delivery records read it.
func nearestRank(n int, p float64) int { return max(0, min(int(math.Ceil(p*float64(n)))-1, n-1)) }
