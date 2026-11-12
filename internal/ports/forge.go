package ports

import (
	"context"
	"errors"

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

// Forge is a forge as a worker runs it (ADR-0020): the control plane requests each operation as a
// recorded effect, and the worker runs it here, in the repository's checkout, under the person's own
// sign-in. Nothing here signs in, reads a token, or stores one. Merging lands in a later step.
type Forge interface {
	// OpenPullRequest opens the pull request for a branch, or returns the one that is open, so a repeat
	// after a crash opens nothing twice.
	OpenPullRequest(ctx context.Context, spec PullRequestSpec) (ledger.PullRequest, error)
	// PullRequest reads a pull request's state, head commit, mergeability, and the checks on its head.
	PullRequest(ctx context.Context, ref PullRequestRef) (ledger.PullRequest, error)
	// Comment posts a comment on a pull request once per key: a comment that already carries the key is
	// not posted again, so a repeat after a crash posts nothing twice.
	Comment(ctx context.Context, ref PullRequestRef, key, body string) error
	// Health reports whether the sign-in for the binding's host can run the operations above. A CLI that
	// is missing, or a sign-in with a problem, is a health state and not an error.
	Health(ctx context.Context, repo ForgeRepo) (ledger.ForgeHealthReport, error)
}
