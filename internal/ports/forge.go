package ports

import (
	"context"
	"errors"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ErrForgeUnavailable means the forge's CLI could not run: it is missing, or its sign-in has a problem.
// A caller holds the work that needs the forge and asks again later.
var ErrForgeUnavailable = errors.New("the forge is unavailable")

// ForgeRepo is one repository as a worker reaches its forge: the checkout the CLI runs in, and the binding
// that names the repository there.
type ForgeRepo struct {
	Dir     string
	Binding ledger.ForgeBinding
}

// PullRequestSpec asks for the pull request of one branch against a base.
type PullRequestSpec struct {
	Repo   ForgeRepo
	Base   string
	Branch string
	Title  string
	Body   string
}

// PullRequestRef names one pull request.
type PullRequestRef struct {
	Repo   ForgeRepo
	Number int64
}

// MaxOpenPullRequests is the most pull requests OpenPullRequests lists.
const MaxOpenPullRequests = 200

// ErrRunNotOfHead means a workflow run a check's address names is not a run of the head under review, or is no run of
// the repository at all. Nothing is rerun or read of it.
var ErrRunNotOfHead = errors.New("the run is not a run of the head under review")

// ErrForgeRateLimited means the forge refused a read because the sign-in is over a rate limit. The error that
// wraps it is a RateLimitedError, which says how long the forge asked a caller to wait.
var ErrForgeRateLimited = errors.New("the forge is rate limiting this sign-in")

// ForgeRate is what a forge's response said of its rate limits (FR-SPEND-20). A field the response did not carry is
// its zero value.
type ForgeRate struct {
	// Exhausted says no request is left in the window, and Reset is when the window ends.
	Exhausted bool
	Reset     time.Time
	// RetryAfter is how long a refusal asked a caller to wait, which a secondary limit sends.
	RetryAfter time.Duration
	// PollInterval is the least time the endpoint asks between two reads of it.
	PollInterval time.Duration
	// Secondary says the refusal was the forge's secondary limit and not the primary one.
	Secondary bool
}

// RateLimitedError is a read the forge refused for a rate limit. A caller holds its reads of that repository
// until the wait the rate names, and then longer each time it is refused again.
type RateLimitedError struct {
	Rate   ForgeRate
	Detail string
}

func (e *RateLimitedError) Error() string {
	if e.Detail == "" {
		return ErrForgeRateLimited.Error()
	}
	return ErrForgeRateLimited.Error() + ": " + e.Detail
}

// Is makes errors.Is(err, ErrForgeRateLimited) true for the error.
func (e *RateLimitedError) Is(target error) bool { return target == ErrForgeRateLimited }

// PullRequestsRead is what one query read of several pull requests of a repository. A number the query could not
// answer completely is not in Pulls, and the caller reads it on its own.
type PullRequestsRead struct {
	Pulls map[int64]ledger.PullRequest
	Rate  ForgeRate
}

// ChecksProbe is what a conditional read said of a pull request's checks. Unchanged says nothing a read would
// report has changed since the read that gave Tag, and a conditional answer of that kind does not count against the
// primary rate limit. Tag is what the next probe passes, and it is opaque to the caller.
type ChecksProbe struct {
	Unchanged bool
	Tag       string
	Rate      ForgeRate
}

// Forge is a forge as a worker runs it (ADR-0020): the control plane requests each operation as a
// recorded effect, and the worker runs it here, in the repository's checkout, under the person's own
// sign-in. Nothing here signs in, reads a token, or stores one.
type Forge interface {
	// OpenPullRequest opens the pull request for a branch, or returns the one that is open, so a repeat
	// after a crash opens nothing twice.
	OpenPullRequest(ctx context.Context, spec PullRequestSpec) (ledger.PullRequest, error)
	// PullRequest reads a pull request's state, head commit, mergeability, and the checks on its head, each
	// marked when the base branch's protection requires it.
	PullRequest(ctx context.Context, ref PullRequestRef) (ledger.PullRequest, error)
	// ReadPullRequests reads several pull requests of one repository, with the checks on each head, in one query,
	// which costs one call where one read each costs two apiece (FR-SPEND-20). A pull request the query cannot
	// answer completely is left out of the answer.
	ReadPullRequests(ctx context.Context, repo ForgeRepo, numbers []int64) (PullRequestsRead, error)
	// OpenPullRequests lists the repository's open pull requests, newest first, at most MaxOpenPullRequests of
	// them. The boolean says the forge holds more than the list.
	OpenPullRequests(ctx context.Context, repo ForgeRepo) ([]ledger.PullRequestSummary, bool, error)
	// ProbeChecks asks whether the pull request or the checks on its head changed since the probe that gave tag,
	// with conditional requests that carry each response's ETag. An empty tag is a probe that has nothing to
	// compare, which answers changed and gives the tag to use next.
	ProbeChecks(ctx context.Context, ref PullRequestRef, head, tag string) (ChecksProbe, error)
	// Merge merges a pull request with a merge method only while its head is the given commit, and never
	// past branch protection. It reads the pull request first, so a pull request an earlier attempt merged
	// at that head answers merged and merges nothing twice. A refusal is a result, with the outcome that says
	// why; an error is a forge that could not answer, which a later attempt asks again.
	Merge(ctx context.Context, ref PullRequestRef, head string, method ledger.MergeMethod) (ledger.MergeResult, error)
	// Comment posts a comment on a pull request once per key: a comment that already carries the key is
	// not posted again, so a repeat after a crash posts nothing twice.
	Comment(ctx context.Context, ref PullRequestRef, key, body string) error
	// Health reports whether the sign-in for the binding's host can run the operations above. A CLI that
	// is missing, or a sign-in with a problem, is a health state and not an error.
	Health(ctx context.Context, repo ForgeRepo) (ledger.ForgeHealthReport, error)
	// RerunChecks starts again the failed jobs of runs of a head's checks (ADR-0037). The runs it is given are
	// rerun, each if it is a run of the head and finished without passing, and a run of any other commit is refused
	// in the result, because the address that named it was a stranger's word. With none given it reruns every run of
	// the head that did not pass. If the forge fails in the middle, the result holds the runs it started again and the
	// ones it did not reach as Pending, beside the error that stopped it, so a caller records the first and asks only
	// for the rest. A run the
	// forge cannot rerun, because it is still going or no longer exists, is a refusal in the result and not an error,
	// and a rerun of a run that already went is a refusal too, so a repeat after a crash starts nothing twice.
	RerunChecks(ctx context.Context, ref PullRequestRef, head string, runs []string) (ledger.CIRerun, error)
	// FailedLog reads the log of what failed in one check, at most its last maxBytes, but only when the run the check's
	// address names is a run of the head: a check's address is whatever the app that made the check wrote, so a run
	// of any other commit is refused with ErrRunNotOfHead and nothing of it is read. A check that has no log on the
	// forge's own runs answers an empty log and no error.
	FailedLog(ctx context.Context, repo ForgeRepo, head string, check ledger.CICheckRef, maxBytes int) (string, error)
	// BaseChecks reads the checks that concluded `failure` on a branch's tip, which says whether a failure of a pull
	// request is the base's too.
	BaseChecks(ctx context.Context, repo ForgeRepo, branch string) (BaseChecksRead, error)
}

// BaseChecksRead is what a read of a branch's tip found: the commit it read, and the names of the checks that
// concluded `failure` on it.
type BaseChecksRead struct {
	Commit string
	Failed []string
	Rate   ForgeRate
}
