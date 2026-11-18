// Package ghstate is what the fake `gh` keeps beside itself: the forge it stands for, and every call it
// took. The double and the tests that drive it share these types, and every write takes one lock and
// lands by a rename, so a reader sees a whole file or the one before it.
package ghstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// The files the double keeps beside itself.
const (
	StateFile = "gh.json"
	LockFile  = "gh.lock"
	CallsDir  = "gh-calls"
)

// Check is one check on a pull request's head, in the shape `gh pr view --json statusCheckRollup` gives:
// a check run has a name, a status, and a conclusion, and a commit status has a context and a state.
type Check struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name,omitempty"`
	Status     string `json:"status,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	DetailsURL string `json:"detailsUrl,omitempty"`
	Context    string `json:"context,omitempty"`
	State      string `json:"state,omitempty"`
	TargetURL  string `json:"targetUrl,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	// CompletedAt is when a finished run completed.
	CompletedAt string `json:"completedAt,omitempty"`
}

// PullRequest is one pull request on the fake forge. An open one's head is whatever the remote holds for
// its branch, and Head keeps the commit it had when it stopped being open. Its mergeability is what a merge
// of its head into its base on the remote would give, unless Mergeable names one, and its merge state
// follows from that and its required checks, unless MergeState names one, such as BLOCKED for a base whose
// protection refuses the merge. Required names the checks the base's protection requires.
type PullRequest struct {
	Number      int64    `json:"number"`
	Repo        string   `json:"repo"`
	Branch      string   `json:"branch"`
	Base        string   `json:"base"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	State       string   `json:"state"`
	Head        string   `json:"head,omitempty"`
	MergeCommit string   `json:"merge_commit,omitempty"`
	MergedAt    string   `json:"merged_at,omitempty"`
	Comments    []string `json:"comments,omitempty"`
	Checks      []Check  `json:"checks,omitempty"`
	Mergeable   string   `json:"mergeable,omitempty"`
	MergeState  string   `json:"merge_state,omitempty"`
	Required    []string `json:"required,omitempty"`
	// Draft is what `isDraft` says, and UpdatedAt what `updatedAt` says.
	Draft     bool   `json:"draft,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// MergeMethod is the flag the merge that merged it named.
	MergeMethod string `json:"merge_method,omitempty"`
}

// Run is one workflow run on the fake forge: what its head's checks stand for when a worker reruns the failed jobs of
// a run or reads the log of what failed in it. Status is as the REST API words it, in lower case, and Reruns counts
// the reruns of failed jobs the fake took.
type Run struct {
	ID         int64  `json:"id"`
	Repo       string `json:"repo"`
	Head       string `json:"head"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	// Log is what `gh run view --log-failed` prints for the run.
	Log    string `json:"log,omitempty"`
	Reruns int64  `json:"reruns,omitempty"`
}

// BaseCheck is one check run on the tip of a branch, which says whether a base is red.
type BaseCheck struct {
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// RateLimit makes the fake refuse every read that goes through the API or the pull request commands, as GitHub does
// over a limit: a primary limit with no request left until Reset, or a secondary one that says how many seconds to
// wait in Retry-After.
type RateLimit struct {
	Secondary  bool  `json:"secondary,omitempty"`
	RetryAfter int64 `json:"retry_after,omitempty"`
	Exhausted  bool  `json:"exhausted,omitempty"`
	Reset      int64 `json:"reset,omitempty"`
}

// State is the whole fake forge.
type State struct {
	// RateLimit refuses reads while it is set.
	RateLimit *RateLimit `json:"rate_limit,omitempty"`
	// PollInterval is what an API response's X-Poll-Interval header says, in seconds, and zero sends none.
	PollInterval int64 `json:"poll_interval,omitempty"`
	// Remaining is what an API response's X-RateLimit-Remaining header says, and Reset the X-RateLimit-Reset. Zero
	// Remaining with a set Reset is a window with nothing left in it that has not refused anything yet.
	Remaining int64 `json:"remaining,omitempty"`
	Reset     int64 `json:"reset,omitempty"`
	// SignedOut makes `gh auth status` fail with AuthError.
	SignedOut bool   `json:"signed_out,omitempty"`
	AuthError string `json:"auth_error,omitempty"`
	// Remote is the remote of the checkout `gh` runs in that holds each open pull request's branch.
	Remote       string        `json:"remote,omitempty"`
	Calls        int64         `json:"calls"`
	PullRequests []PullRequest `json:"pull_requests,omitempty"`
	Runs         []Run         `json:"runs,omitempty"`
	Bases        []BaseCheck   `json:"bases,omitempty"`
	// FailRerun is a run whose next rerun of failed jobs the forge answers with a 500, once.
	FailRerun int64 `json:"fail_rerun,omitempty"`
}

// FindRun returns the run with an ID, or nil.
func (s *State) FindRun(id int64) *Run {
	for i := range s.Runs {
		if s.Runs[i].ID == id {
			return &s.Runs[i]
		}
	}
	return nil
}

// Find returns the pull request with a number, or nil.
func (s *State) Find(number int64) *PullRequest {
	for i := range s.PullRequests {
		if s.PullRequests[i].Number == number {
			return &s.PullRequests[i]
		}
	}
	return nil
}

// Call is one run of the double: its arguments, the directory it ran in, and the variables of its
// environment that decide what `gh` may reach.
type Call struct {
	Seq  int64             `json:"seq"`
	Args []string          `json:"args"`
	Dir  string            `json:"dir"`
	Env  map[string]string `json:"env"`
}

// Watched are the variables a call records, set or not.
var Watched = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_CONFIG_DIR", "GH_PROMPT_DISABLED", "GH_NO_UPDATE_NOTIFIER"}

// Edit changes the state under the lock, and writes it whole.
func Edit(dir string, change func(*State) error) error {
	lock, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	state, err := Read(dir)
	if err != nil {
		return err
	}
	if err := change(&state); err != nil {
		return err
	}
	return writeWhole(filepath.Join(dir, StateFile), state)
}

// Read reads the state, which is empty before the first write.
func Read(dir string) (State, error) {
	var state State
	raw, err := os.ReadFile(filepath.Join(dir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return state, err
}

// Record keeps one call under the next sequence number.
func Record(dir string, call Call) error {
	if err := os.MkdirAll(filepath.Join(dir, CallsDir), 0o700); err != nil {
		return err
	}
	return writeWhole(filepath.Join(dir, CallsDir, fmt.Sprintf("%06d.json", call.Seq)), call)
}

// Calls reads every call, in the order they were made.
func Calls(dir string) ([]Call, error) {
	entries, err := os.ReadDir(filepath.Join(dir, CallsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Call
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, CallsDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var call Call
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		out = append(out, call)
	}
	slices.SortFunc(out, func(a, b Call) int { return int(a.Seq - b.Seq) })
	return out, nil
}

func writeWhole(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	next := path + ".next"
	if err := os.WriteFile(next, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(next, path)
}
