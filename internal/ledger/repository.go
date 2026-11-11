package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// SubjectRepository is what a repository's events are about.
const SubjectRepository = "repository"

// The events of a repository's record.
const (
	// EventRepositoryDetected records what git said of a repository when a source was read: its remote
	// and its default branch. The first one makes the record.
	EventRepositoryDetected = "repository.detected"
	// EventRepositoryChanged records a member changing the settings a source read cannot.
	EventRepositoryChanged = "repository.changed"
)

// MergeMethod is how a pull request lands on its base branch.
type MergeMethod string

// The merge methods. A squash and a rebase write new commits, so the commits a lane built on are not
// the commits the base then holds.
const (
	MergeCommit MergeMethod = "merge"
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
)

// Rewrites reports a method that gives the base commits other than the ones the branch held.
func (m MergeMethod) Rewrites() bool { return m == MergeSquash || m == MergeRebase }

// StartFrom says what dependent work starts from.
type StartFrom string

// The starting points of work that depends on other work in the same repository.
const (
	// StartFromMerged waits until a dependency's accepted commit is in the base branch.
	StartFromMerged StartFrom = "merged"
	// StartFromAccepted starts from a dependency's accepted commit.
	StartFromAccepted StartFrom = "accepted"
)

// ReviewTarget is where a repository's accepted work is reviewed and lands (ADR-0020).
type ReviewTarget string

// The review targets. Only a member chooses one: a plan, a manifest, an orchestrator, and a lane cannot.
const (
	// ReviewOnBranch keeps accepted work on its branch, which ends the work order; the default.
	ReviewOnBranch ReviewTarget = "branch"
	// ReviewOnPullRequest opens a pull request through the repository's forge.
	ReviewOnPullRequest ReviewTarget = "pull_request"
)

// MergeAuthority is how much of the loop stays with the person: who accepts finished work, and later who
// merges it. Only a member chooses it, and nothing learned widens it.
type MergeAuthority string

// The merge authority levels, from the most the person keeps to the least.
const (
	// PersonMerges leaves every change to the person: the review agent judges it first, the person sees
	// each verdict, and the person decides.
	PersonMerges MergeAuthority = "person_merges"
	// PersonApproves has the person approve each change, and ropeshift lands it.
	PersonApproves MergeAuthority = "person_approves"
	// ReviewApproves lets the review agent's approval stand for the person's, once every criterion passed.
	ReviewApproves MergeAuthority = "review_approves"
)

// PersonDecides reports a level at which a member answers for each change after the review.
func (a MergeAuthority) PersonDecides() bool { return a != ReviewApproves }

// ErrInvalidRepository marks a record, or a change to one, that the ledger must not accept.
var ErrInvalidRepository = errors.New("invalid repository")

// ErrRewritesCommits means a record would start dependent work from accepted commits in a repository
// whose merge method rewrites them (FR-BASE-3).
var ErrRewritesCommits = fmt.Errorf("%w: accepted work cannot be built on when the merge method rewrites its commits", ErrInvalidRepository)

// Repository is the workspace's record of one repository, keyed by the repository's identity. Git gives
// the remote and the default branch, and a member gives the rest (FR-REPO-1).
type Repository struct {
	ID string `json:"id"`
	// Remote is the URL of the remote work lands on, with no credential in it. Empty means the
	// repository has none.
	Remote        string `json:"remote,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// BaseBranch is the branch lanes start from. Empty follows the default branch.
	BaseBranch string `json:"base_branch,omitempty"`
	// ForgeBinding names the forge account and repository pull requests open against. Empty is none yet.
	ForgeBinding string      `json:"forge_binding,omitempty"`
	MergeMethod  MergeMethod `json:"merge_method"`
	// MergeAuthority is how much of each change the person decides after the review (FR-MERGE-3). Empty
	// reads as PersonMerges, which a record made before the level existed holds.
	MergeAuthority MergeAuthority `json:"merge_authority,omitempty"`
	// ReviewTarget is where accepted work is reviewed and lands. Empty reads as ReviewOnBranch.
	ReviewTarget ReviewTarget `json:"review_target"`
	StartFrom    StartFrom    `json:"start_from"`
}

// NewRepository returns a record with the settings a person who chose nothing gets.
func NewRepository(id string) Repository {
	return Repository{ID: id, MergeMethod: MergeCommit, StartFrom: StartFromMerged, MergeAuthority: PersonMerges, ReviewTarget: ReviewOnBranch}
}

// Target is the review target in force: the one a member chose, or else a branch.
func (r Repository) Target() ReviewTarget {
	if r.ReviewTarget == "" {
		return ReviewOnBranch
	}
	return r.ReviewTarget
}

// Level is the merge authority in force: the one a member chose, or else the default.
func (r Repository) Level() MergeAuthority {
	if r.MergeAuthority == "" {
		return PersonMerges
	}
	return r.MergeAuthority
}

// Base is the branch lanes start from: the one a member named, or else the default branch.
func (r Repository) Base() string {
	if r.BaseBranch != "" {
		return r.BaseBranch
	}
	return r.DefaultBranch
}

// Validate refuses a record that holds a setting nobody offers, or settings that cannot hold together.
func (r Repository) Validate() error {
	switch r.MergeMethod {
	case MergeCommit, MergeSquash, MergeRebase:
	default:
		return fmt.Errorf("%w: merge method %q", ErrInvalidRepository, r.MergeMethod)
	}
	switch r.StartFrom {
	case StartFromMerged, StartFromAccepted:
	default:
		return fmt.Errorf("%w: start from %q", ErrInvalidRepository, r.StartFrom)
	}
	switch r.Level() {
	case PersonMerges, PersonApproves, ReviewApproves:
	default:
		return fmt.Errorf("%w: merge authority %q", ErrInvalidRepository, r.MergeAuthority)
	}
	switch r.Target() {
	case ReviewOnBranch, ReviewOnPullRequest:
	default:
		return fmt.Errorf("%w: review target %q", ErrInvalidRepository, r.ReviewTarget)
	}
	if r.BaseBranch != "" {
		if err := CheckBranch(r.BaseBranch); err != nil {
			return err
		}
	}
	if r.StartFrom == StartFromAccepted && r.MergeMethod.Rewrites() {
		return fmt.Errorf("%w (%s)", ErrRewritesCommits, r.MergeMethod)
	}
	return nil
}

// forbiddenInBranch lists the characters git refuses in a ref name, apart from control characters
// and spaces.
const forbiddenInBranch = "~^:?*[\\"

// CheckBranch refuses a name git would not take as a branch, because it goes to git as an argument.
func CheckBranch(name string) error {
	bad := func(why string) error { return fmt.Errorf("%w: branch %q %s", ErrInvalidRepository, name, why) }
	switch {
	case name == "":
		return bad("is empty")
	case name == "@":
		return bad("is not a name")
	case strings.HasPrefix(name, "-"):
		return bad("starts with a dash")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return bad("has an empty part")
	case strings.Contains(name, "..") || strings.Contains(name, "@{"):
		return bad("holds a sequence git refuses")
	case strings.HasSuffix(name, "."):
		return bad("ends with a dot")
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(forbiddenInBranch, r) {
			return bad("holds a character git refuses")
		}
	}
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return bad("has a part git refuses")
		}
	}
	return nil
}
