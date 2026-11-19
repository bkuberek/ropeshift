package worker

import (
	"errors"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// The forge's rate limits (FR-SPEND-20): a read the forge refuses for one holds the repository's reads for the wait
// the response names, and for longer each time it is refused again, from a minute, as GitHub's guidance asks
// (https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api, "Handle rate limit
// errors appropriately" and "Avoid polling").

const (
	// ciBackoffFirst is the wait after a first refusal, and it doubles with each refusal after.
	ciBackoffFirst = time.Minute
	// ciBackoffMost bounds the wait, so a repository is asked again within the hour whatever it was told.
	ciBackoffMost = time.Hour
)

// ciRateSeen is where one repository stands with the forge's rate limits.
type ciRateSeen struct {
	// until is when the forge's refusal, or a window with nothing left in it, says to read again. A hint does not
	// break it.
	until time.Time
	// floor is the least time an endpoint asked between two reads, from x-poll-interval. A hint reads through it.
	floor time.Time
	// refusals counts the refusals in a row, which a read the forge answers ends.
	refusals int
	// noticed says the notice of this stretch of refusals was given.
	noticed bool
}

// ciBackoff is the wait after the n-th refusal in a row: a minute, then twice as long each time.
func ciBackoff(n int) time.Duration {
	return doublingBackoff(n, ciBackoffFirst, ciBackoffMost)
}

// doublingBackoff is the wait after the n-th failure in a row: first, then twice as long each time, and never more
// than most.
func doublingBackoff(n int, first, most time.Duration) time.Duration {
	wait := first
	for range max(n, 1) - 1 {
		wait *= 2
		if wait >= most {
			return most
		}
	}
	return min(wait, most)
}

// rateLimited holds a repository's reads after the forge refused one for a rate limit, for the longest of the
// backoff, the retry-after the refusal asked, and the reset of an exhausted window. It says so once in a stretch
// of refusals, in a notice that holds the repository's ID and the time of the next read and nothing a forge or a
// person wrote.
func (w *Worker) rateLimited(project, repository string, err error) {
	var limited *ports.RateLimitedError
	rate := ports.ForgeRate{}
	if errors.As(err, &limited) {
		rate = limited.Rate
	}
	now := w.clock.Now()
	key := forgeKey(project, repository)
	w.forgeLock.Lock()
	if w.ciRate == nil {
		w.ciRate = map[string]ciRateSeen{}
	}
	seen := w.ciRate[key]
	seen.refusals++
	wait := max(ciBackoff(seen.refusals), rate.RetryAfter)
	until := now.Add(wait)
	if rate.Exhausted && rate.Reset.After(until) {
		until = rate.Reset
	}
	seen.until = until
	first := !seen.noticed
	seen.noticed = true
	w.ciRate[key] = seen
	w.forgeLock.Unlock()
	if first {
		w.config.Notice(fmt.Sprintf("repository %s: the forge is rate limiting this sign-in, so its pull requests are not read until %s", repository, until.UTC().Format(time.RFC3339)))
	}
}

// rateKept keeps what an answer said of the rate limits: it ends a stretch of refusals, holds the repository until
// the reset when the window has nothing left, and sets the floor an x-poll-interval asks.
func (w *Worker) rateKept(project, repository string, rate ports.ForgeRate) {
	now := w.clock.Now()
	key := forgeKey(project, repository)
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	if w.ciRate == nil {
		w.ciRate = map[string]ciRateSeen{}
	}
	seen := w.ciRate[key]
	seen.refusals, seen.noticed = 0, false
	if rate.Exhausted && rate.Reset.After(now) {
		seen.until = rate.Reset
	}
	if rate.PollInterval > 0 {
		seen.floor = now.Add(rate.PollInterval)
	}
	w.ciRate[key] = seen
}

// rateHeld says the repository's reads are held by a refusal or by an exhausted window.
func (w *Worker) rateHeld(project, repository string) bool {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	return w.clock.Now().Before(w.ciRate[forgeKey(project, repository)].until)
}
