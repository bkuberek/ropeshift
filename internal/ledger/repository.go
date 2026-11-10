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
	// Integration says whether ropeshift pushes accepted work and opens its pull request.
	Integration bool      `json:"integration"`
	StartFrom   StartFrom `json:"start_from"`
}

// NewRepository returns a record with the settings a person who chose nothing gets.
func NewRepository(id string) Repository {
	return Repository{ID: id, MergeMethod: MergeCommit, StartFrom: StartFromMerged}
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
