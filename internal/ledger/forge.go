package ledger

import (
	"fmt"
	"strings"
	"unicode"
)

// ForgeBinding names one repository on a forge as HOST/OWNER/REPO, the form `gh --repo` takes
// (ADR-0020). A repository whose review target is `pull_request` must have one.
type ForgeBinding struct {
	Host  string
	Owner string
	Repo  string
}

// String writes the binding as HOST/OWNER/REPO.
func (b ForgeBinding) String() string { return b.Host + "/" + b.Owner + "/" + b.Repo }

// ParseForgeBinding reads HOST/OWNER/REPO, and refuses anything else, because each part goes to the
// forge's CLI as an argument.
func ParseForgeBinding(text string) (ForgeBinding, error) {
	parts := strings.Split(text, "/")
	if len(parts) != 3 {
		return ForgeBinding{}, fmt.Errorf("%w: forge binding %q is not HOST/OWNER/REPO", ErrInvalidRepository, text)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, "-") || strings.IndexFunc(part, notBindingRune) >= 0 {
			return ForgeBinding{}, fmt.Errorf("%w: forge binding %q has a part a forge would refuse", ErrInvalidRepository, text)
		}
	}
	return ForgeBinding{Host: parts[0], Owner: parts[1], Repo: parts[2]}, nil
}

func notBindingRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`\:@?#`, r)
}

// BindingFromRemote reads a binding from a remote's URL, in its https, ssh, or scp-like form: the host,
// and a path of exactly an owner and a repository. A local path, and a path of any other shape, name no
// forge repository, and the answer is empty.
func BindingFromRemote(remote string) string {
	host, path := splitRemote(strings.TrimSpace(remote))
	if host == "" {
		return ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, repo, found := strings.Cut(path, "/")
	if !found {
		return ""
	}
	binding := ForgeBinding{Host: host, Owner: owner, Repo: repo}
	if _, err := ParseForgeBinding(binding.String()); err != nil {
		return ""
	}
	return binding.String()
}

// ForgeKind names what the control plane asks a worker to do on a forge for one work order. Each is an
// effect of the worker process, never of an agent (FR-MERGE-12).
type ForgeKind string

// The kinds of forge request.
const (
	// ForgePublish pushes a lane's branch to the repository's remote, then opens a pull request for it
	// against the work order's base, or finds the one that is open.
	ForgePublish ForgeKind = "publish"
	// ForgeComment posts a comment, such as a review's verdicts, on a pull request, once.
	ForgeComment ForgeKind = "comment"
	// ForgeMerge merges accepted work at its reviewed commit, with the repository's merge method: the pull
	// request through the forge, only while its head is that commit, or, with review target `branch`, the
	// lane's branch into the base with git, only while the branch is still at that commit.
	ForgeMerge ForgeKind = "merge"
)

// ForgeRequestState says where a forge request stands. A failed attempt stays requested, with the
// reason, and the next attempt counts up.
type ForgeRequestState string

// The states of a forge request.
const (
	ForgeRequested ForgeRequestState = "requested"
	ForgeDone      ForgeRequestState = "done"
)

// ForgeRequest is one forge request as the ledger keeps it. ID is derived from the work order and the
// commit, or the review, so a request made twice is made once.
type ForgeRequest struct {
	ID         string    `json:"id"`
	Kind       ForgeKind `json:"kind"`
	WorkOrder  string    `json:"work_order"`
	Repository string    `json:"repository"`
	// Worker is the worker that runs it: the one that ran the lane for a publish, and the one that opened
	// the pull request for the rest, because it holds the checkout and the sign-in.
	Worker  string `json:"worker"`
	Binding string `json:"binding"`
	Branch  string `json:"branch,omitempty"`
	Base    string `json:"base,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Number  int64  `json:"number,omitempty"`
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
	// Method is how a merge lands on its base.
	Method MergeMethod `json:"method,omitempty"`
	// Attempt counts the attempts the control plane has offered, from 1, and names the worker's effect.
	Attempt     int64             `json:"attempt"`
	State       ForgeRequestState `json:"state"`
	Detail      string            `json:"detail,omitempty"`
	RequestedAt int64             `json:"requested_at"`
	ReportedAt  int64             `json:"reported_at,omitempty"`
}

// DesiredForge is a forge request a worker has not reported on. The control plane offers it until one
// report lands.
type DesiredForge struct {
	Project    string      `json:"project,omitempty"`
	ID         string      `json:"id"`
	Kind       ForgeKind   `json:"kind"`
	WorkOrder  string      `json:"work_order"`
	Repository string      `json:"repository"`
	Binding    string      `json:"binding"`
	Branch     string      `json:"branch,omitempty"`
	Base       string      `json:"base,omitempty"`
	Commit     string      `json:"commit,omitempty"`
	Number     int64       `json:"number,omitempty"`
	Title      string      `json:"title,omitempty"`
	Body       string      `json:"body,omitempty"`
	Method     MergeMethod `json:"method,omitempty"`
	Attempt    int64       `json:"attempt"`
}

// DesiredPullRequest is a pull request a worker watches for the ledger, as the ledger last heard of it. The
// worker reads it now and then, and reports it only when what it reads differs.
type DesiredPullRequest struct {
	Project     string           `json:"project,omitempty"`
	WorkOrder   string           `json:"work_order"`
	Repository  string           `json:"repository"`
	Binding     string           `json:"binding"`
	Number      int64            `json:"number"`
	State       PullRequestState `json:"state"`
	HeadCommit  string           `json:"head_commit"`
	MergeCommit string           `json:"merge_commit,omitempty"`
}

// DesiredForgeRepository is a repository whose review target needs the forge. A worker that keeps a
// checkout of it reports its forge sign-in's health there.
type DesiredForgeRepository struct {
	Project    string `json:"project,omitempty"`
	Repository string `json:"repository"`
	Binding    string `json:"binding"`
}

// PullRequestState is a pull request's state on its forge.
type PullRequestState string

// The states of a pull request.
const (
	PullRequestOpen   PullRequestState = "open"
	PullRequestClosed PullRequestState = "closed"
	PullRequestMerged PullRequestState = "merged"
)

// ForgeCheckState is where one check a forge runs on a commit stands.
type ForgeCheckState string

// The states of a forge's check.
const (
	CheckPending ForgeCheckState = "pending"
	CheckPassed  ForgeCheckState = "passed"
	CheckFailed  ForgeCheckState = "failed"
)

// PullRequestCheck is one check a forge reports on a pull request's head commit. Required says the base
// branch's protection requires it, which makes it acceptance evidence and a gate on the merge (FR-MERGE-2).
type PullRequestCheck struct {
	Name       string          `json:"name"`
	State      ForgeCheckState `json:"state"`
	DetailsURI string          `json:"details_uri,omitempty"`
	Required   bool            `json:"required,omitempty"`
}

// PullRequest is what a forge says of one pull request. Checks are those on HeadCommit, and count for no
// other commit.
type PullRequest struct {
	Number      int64              `json:"number"`
	URL         string             `json:"url"`
	State       PullRequestState   `json:"state"`
	Base        string             `json:"base"`
	Branch      string             `json:"branch"`
	HeadCommit  string             `json:"head_commit"`
	Mergeable   string             `json:"mergeable,omitempty"`
	MergeCommit string             `json:"merge_commit,omitempty"`
	MergedAt    string             `json:"merged_at,omitempty"`
	Checks      []PullRequestCheck `json:"checks,omitempty"`
}

// WorkPullRequest is the pull request a work order lands through, as the ledger last heard of it.
type WorkPullRequest struct {
	PullRequest
	WorkOrder  string `json:"work_order"`
	Repository string `json:"repository"`
	// Worker opened it, and reads and comments on it.
	Worker string `json:"worker"`
	// ReadAt is when a worker last reported it, in database time.
	ReadAt int64 `json:"read_at"`
}

// ForgeHealthState says whether a worker can reach a repository's forge with the person's sign-in.
type ForgeHealthState string

// The states of a worker's forge sign-in for one repository.
const (
	// ForgeHealthy says `gh auth status` succeeded for the binding's host.
	ForgeHealthy ForgeHealthState = "ok"
	// ForgeSignedOut says `gh auth status` reported a problem with the sign-in.
	ForgeSignedOut ForgeHealthState = "signed_out"
	// ForgeMissing says the worker has no `gh` to run.
	ForgeMissing ForgeHealthState = "missing"
)

// ForgeHealth is what a worker said of its forge sign-in for one repository, and when the ledger heard it.
type ForgeHealth struct {
	Worker     string           `json:"worker"`
	WorkerName string           `json:"worker_name,omitempty"`
	Repository string           `json:"repository"`
	State      ForgeHealthState `json:"state"`
	Detail     string           `json:"detail,omitempty"`
	CheckedAt  int64            `json:"checked_at"`
}

// Healthy reports a sign-in the worker can run forge operations with.
func (h ForgeHealth) Healthy() bool { return h.State == ForgeHealthy }

// The uploads a worker makes of the forge. Neither passes a fence: what a worker did on a forge, and
// what its sign-in says, stay true whoever holds the work now.
const (
	// UploadForgeReported carries a ForgeReport.
	UploadForgeReported = "forge_reported"
	// UploadForgeHealth carries a ForgeHealthReport.
	UploadForgeHealth = "forge_health"
	// UploadPullRequestRead carries a PullRequestReport.
	UploadPullRequestRead = "pull_request_read"
)

// PullRequestReport is what a worker read of a pull request it watches, which differs from what the ledger
// last heard.
type PullRequestReport struct {
	WorkOrder   string      `json:"work_order"`
	PullRequest PullRequest `json:"pull_request"`
}

// ForgeReport is how one attempt at a forge request came out. A failed attempt carries why, a publish or a
// read that worked carries the pull request, and a merge that ran carries what became of it.
type ForgeReport struct {
	Request     string       `json:"request"`
	Attempt     int64        `json:"attempt"`
	Failed      bool         `json:"failed,omitempty"`
	Detail      string       `json:"detail,omitempty"`
	PullRequest *PullRequest `json:"pull_request,omitempty"`
	Merge       *MergeResult `json:"merge,omitempty"`
}

// MergeOutcome is what a merge came to. Only MergeMerged landed anything; each other outcome is the
// forge's or git's answer, which a later attempt of the same request would give again, so the control
// plane decides what follows it rather than asking again.
type MergeOutcome string

// The outcomes of a merge.
const (
	// MergeMerged landed the reviewed commit on its base, now or by an earlier attempt.
	MergeMerged MergeOutcome = "merged"
	// MergeConflict means the branch does not merge cleanly into its base.
	MergeConflict MergeOutcome = "conflict"
	// MergeBehind means the base requires an up-to-date branch, and the branch is behind it.
	MergeBehind MergeOutcome = "behind"
	// MergeStaleHead means the pull request's head, or the branch's tip, is no longer the reviewed commit.
	MergeStaleHead MergeOutcome = "stale_head"
	// MergeRefused means the forge or the repository refused the merge for any other reason: branch
	// protection, a closed pull request, or a base the person has checked out.
	MergeRefused MergeOutcome = "refused"
)

// Rework reports an outcome the lane mends on the same branch, by merging the current base into it.
func (o MergeOutcome) Rework() bool { return o == MergeConflict || o == MergeBehind }

// MergeResult is what one merge came to. A merge that landed names the commit it made on the base, and one
// that did not says why, with the files that conflict when git can name them.
type MergeResult struct {
	Outcome     MergeOutcome `json:"outcome"`
	MergeCommit string       `json:"merge_commit,omitempty"`
	MergedAt    string       `json:"merged_at,omitempty"`
	Detail      string       `json:"detail,omitempty"`
	Files       []string     `json:"files,omitempty"`
}

// ForgeHealthReport is what a worker found of its forge sign-in for one repository.
type ForgeHealthReport struct {
	Repository string           `json:"repository"`
	State      ForgeHealthState `json:"state"`
	Detail     string           `json:"detail,omitempty"`
}

// The events of the forge.
const (
	// SubjectPullRequest is what a pull request's events are about: the work order it lands.
	SubjectPullRequest = "pull_request"
	// EventForgeRequested records a request the control plane made of a worker.
	EventForgeRequested = "forge.requested"
	// EventForgeFailed records an attempt that failed, with why.
	EventForgeFailed = "forge.failed"
	// EventForgeHealth records a change in a worker's forge sign-in for a repository.
	EventForgeHealth = "forge.health"
	// EventPullRequestOpened records a pull request a worker opened or found open, at its head commit.
	EventPullRequestOpened = "pull_request.opened"
	// EventPullRequestUpdated records a pull request a worker read with a new head, mergeability, or checks.
	EventPullRequestUpdated = "pull_request.updated"
	// EventPullRequestCommented records a comment a worker posted.
	EventPullRequestCommented = "pull_request.commented"
	// EventPullRequestMerged records a merge a worker read, whoever made it.
	EventPullRequestMerged = "pull_request.merged"
	// EventPullRequestClosed records a pull request a worker read as closed with no merge.
	EventPullRequestClosed = "pull_request.closed"
)
