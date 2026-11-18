package ledger

import (
	"strings"
	"time"
	"unicode"
)

// The kinds of thing CI raises for a person (ADR-0037). Each is a Needs you item and a notice of the same kind.
const (
	// CIItemNotFinished is a head whose required checks passed the CI timeout.
	CIItemNotFinished = "ci_not_finished"
	// CIItemQueueStalled is a required run that stayed queued past the queue timeout. The fix is a runner, not a rerun.
	CIItemQueueStalled = "ci_queue_stalled"
	// CIItemInfrastructure is an infrastructure conclusion that remained after the automatic reruns.
	CIItemInfrastructure = "ci_infrastructure_failure"
	// CIItemBaseRed is a failed check that also fails on the base branch's tip.
	CIItemBaseRed = "ci_base_red"
	// CIItemBaseUnread is a failed check whose base branch's tip the forge refused to let a worker read, on CIBaseUnreadReads
	// reads of the pull request in a row, so nobody can tell whether the base shares the failure.
	CIItemBaseUnread = "ci_base_unread"
	// CIItemActionRequired is a required check that concluded `action_required`: a person on the forge acts.
	CIItemActionRequired = "ci_action_required"
)

// CIItemKinds lists the kinds in the order a surface shows them.
var CIItemKinds = []string{CIItemNotFinished, CIItemQueueStalled, CIItemInfrastructure, CIItemBaseRed, CIItemBaseUnread, CIItemActionRequired}

// CIWaiting is what members did about one head's wait: the time Wait longer added to its timeout, and the reruns
// asked for, which is every rerun of the head, a member's and the automatic ones, with when the last was asked for.
// A rerun starts the head's CI clock again from then. AutoReruns counts the automatic reruns among them, and only
// those are bound by `ci_auto_rerun` (ADR-0037 allows that many plus each member's explicit rerun).
type CIWaiting struct {
	ExtendedMs int64 `json:"extended_ms,omitempty"`
	Reruns     int64 `json:"reruns,omitempty"`
	AutoReruns int64 `json:"auto_reruns,omitempty"`
	RerunAt    int64 `json:"rerun_at,omitempty"`
}

// CIWatch is where one head's required checks stand against the repository's CI limits, which the work order's page
// shows and the Needs you view raises an item from (FR-SPEND-23). Times are database milliseconds, and the limits
// are the ones in force: the repository's keys, its history, and what members extended. A head with no required
// check, or one whose time cannot be told, is not watched.
type CIWatch struct {
	Head     string `json:"head"`
	Watching bool   `json:"watching"`
	// Final says every required check finished. Queued says a required run has not started.
	Final  bool `json:"final"`
	Queued bool `json:"queued"`
	// History says the expected time is the repository's own. With none the expected fields are empty, and the
	// limits are the no-history keys.
	History       bool  `json:"history"`
	ExpectedP50Ms int64 `json:"expected_p50_ms,omitempty"`
	ExpectedMs    int64 `json:"expected_ms,omitempty"`
	// ElapsedMs is the time from the push to now, or to the last completion once the checks finished. QueueMs is
	// the time a required run waited for a runner, or has waited so far.
	ElapsedMs int64 `json:"elapsed_ms"`
	QueueMs   int64 `json:"queue_ms"`
	// TimeoutMs is the CI timeout in force, with what Wait longer added, and TimeoutAt the time it is reached.
	TimeoutMs      int64 `json:"timeout_ms"`
	TimeoutAt      int64 `json:"timeout_at"`
	QueueTimeoutMs int64 `json:"queue_timeout_ms"`
	QueueTimeoutAt int64 `json:"queue_timeout_at"`
	ExtendedMs     int64 `json:"extended_ms,omitempty"`
	Reruns         int64 `json:"reruns,omitempty"`
	// Kind is the item the head raises now, and empty when it raises none. Since is when it came due.
	Kind  string `json:"kind,omitempty"`
	Since int64  `json:"since,omitempty"`
	// RunURL is where the forge shows the run that has not finished, when it gave an address a link can hold.
	RunURL string `json:"run_url,omitempty"`
}

// CITimeoutMax is the most one press of Wait longer may add: the greatest CI timeout a repository may hold.
const CITimeoutMax = ciTimeoutHigh

// WaitStep is what Wait longer adds when no amount is named: one more p85 of the repository's CI, or
// ci_no_history_p85 when it has no history, and never under a minute.
func (s CISettings) WaitStep(watch CIWatch) time.Duration {
	if watch.History {
		return max(time.Duration(watch.ExpectedMs)*time.Millisecond, time.Minute)
	}
	return max(s.NoHistory().P85, time.Minute)
}

// linkable keeps an address a page may link: a forge's word, so only an https address with a host and no user.
func linkable(address string) string {
	rest, secure := strings.CutPrefix(address, "https://")
	if !secure {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	if host == "" || strings.ContainsFunc(address, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) || strings.Contains(host, "@") {
		return ""
	}
	return address
}

// timeoutOf is the CI timeout before what members extended: the multiplier times the repository's own p85 when it
// has history, and ci_timeout_no_history when it has none. It is never under the least that key allows, so a
// repository whose CI takes seconds is not raised by a slow minute.
func (s CISettings) timeoutOf(stats CIStats) time.Duration {
	e := s.Effective()
	if !stats.Has {
		return s.duration(e.TimeoutNoHistory)
	}
	return min(max(time.Duration(e.TimeoutMultiplier*float64(stats.Duration.P85)), ciTimeoutLow), ciTimeoutHigh)
}

// queueTimeoutOf is the same for a queued run: the multiplier times the p85 of the repository's queue time, or
// ci_queue_timeout_no_history, and never under the least that key allows.
func (s CISettings) queueTimeoutOf(stats CIStats) time.Duration {
	e := s.Effective()
	if !stats.Has {
		return s.duration(e.QueueTimeoutNoHistory)
	}
	return min(max(time.Duration(e.QueueTimeoutMultiplier*float64(stats.Queue.P85)), ciQueueTimeoutLow), ciQueueTimeoutHi)
}

// pushedAt is when the head was pushed as far as the ledger can tell: when it first held it, or the first start of
// a required run when that came first. Zero says it cannot be told.
func pushedAt(pr WorkPullRequest, required []PullRequestCheck) int64 {
	push := pr.HeadSince
	for _, check := range required {
		if started, ok := millis(check.StartedAt); ok && (push == 0 || started < push) {
			push = started
		}
	}
	return push
}

// Watch reads one pull request's head against the repository's CI limits at now. A queued required run can raise
// only the stalled queue, whichever limit is shorter, because the wait for a runner is not CI running long; any
// other unfinished required check can raise only "CI has not finished".
func (s CISettings) Watch(pr WorkPullRequest, stats CIStats, waiting CIWaiting, now int64) CIWatch {
	var required []PullRequestCheck
	for _, check := range pr.Checks {
		if check.Required {
			required = append(required, check)
		}
	}
	watch := CIWatch{Head: pr.HeadCommit, ExtendedMs: waiting.ExtendedMs, Reruns: waiting.Reruns}
	push := pushedAt(pr, required)
	if len(required) == 0 || push == 0 {
		return watch
	}
	// A rerun starts the head's runs again, so its clocks run from the later of the push and the rerun.
	push = max(push, waiting.RerunAt)
	watch.Watching, watch.Final = true, true
	if history := stats.History(); history != nil {
		watch.History, watch.ExpectedP50Ms, watch.ExpectedMs = true, history.P50.Milliseconds(), history.P85.Milliseconds()
	}
	watch.TimeoutMs = s.timeoutOf(stats).Milliseconds() + waiting.ExtendedMs
	watch.TimeoutAt = push + watch.TimeoutMs
	watch.QueueTimeoutMs = s.queueTimeoutOf(stats).Milliseconds()
	watch.QueueTimeoutAt = push + watch.QueueTimeoutMs
	var firstStart, lastCompleted int64
	var unfinished, queuedRun *PullRequestCheck
	for i := range required {
		check := &required[i]
		if started, ok := millis(check.StartedAt); ok && (firstStart == 0 || started < firstStart) {
			firstStart = started
		}
		if done, ok := millis(check.CompletedAt); ok && done > lastCompleted {
			lastCompleted = done
		}
		if check.State != CheckPending {
			continue
		}
		watch.Final = false
		if unfinished == nil {
			unfinished = check
		}
		if check.Status == CIRunQueued && queuedRun == nil {
			queuedRun = check
		}
	}
	switch {
	case watch.Final:
		watch.ElapsedMs = max(lastCompleted-push, 0)
	default:
		watch.ElapsedMs = max(now-push, 0)
	}
	watch.Queued = queuedRun != nil
	switch {
	case watch.Queued:
		watch.QueueMs = max(now-push, 0)
	case firstStart != 0:
		watch.QueueMs = max(firstStart-push, 0)
	}
	if pr.State != PullRequestOpen || watch.Final {
		return watch
	}
	switch {
	case watch.Queued && now >= watch.QueueTimeoutAt:
		watch.Kind, watch.Since, watch.RunURL = CIItemQueueStalled, watch.QueueTimeoutAt, linkable(queuedRun.DetailsURI)
	case !watch.Queued && now >= watch.TimeoutAt:
		watch.Kind, watch.Since, watch.RunURL = CIItemNotFinished, watch.TimeoutAt, linkable(unfinished.DetailsURI)
	}
	return watch
}
