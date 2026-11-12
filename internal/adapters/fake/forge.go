package fake

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

type forgeKey struct {
	scope   ledger.WorkspaceID
	request ports.ForgeRequest
}

// forgePR is one pull request the fake forge holds.
type forgePR struct {
	binding  string
	pr       ledger.PullRequest
	comments []string
	keys     map[string]bool
}

// Forge is a forge held in memory, as the control plane reads its checks and as a worker runs it. A test
// sets what ran on a commit, pushes a branch's head, merges a pull request as a person would, and signs
// the CLI out. Fail stands for a forge that cannot be reached.
type Forge struct {
	mu     sync.Mutex
	checks map[forgeKey][]ports.ForgeCheck
	// heads is what the forge holds for each branch, by binding and branch.
	heads map[string]string
	prs   []*forgePR
	// health is the sign-in's state by host, and missing says no CLI runs at all.
	health  map[string]ledger.ForgeHealthReport
	missing bool
	calls   []string
	// Fail is what every read and operation returns while it is set.
	Fail error
}

var (
	_ ports.CheckSource = (*Forge)(nil)
	_ ports.Forge       = (*Forge)(nil)
)

// Set replaces what ran on a commit.
func (f *Forge) Set(scope ledger.WorkspaceID, request ports.ForgeRequest, checks ...ports.ForgeCheck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks == nil {
		f.checks = map[forgeKey][]ports.ForgeCheck{}
	}
	f.checks[forgeKey{scope, request}] = slices.Clone(checks)
}

// Checks lists what ran on a commit, by name.
func (f *Forge) Checks(_ context.Context, scope ledger.WorkspaceID, request ports.ForgeRequest) ([]ports.ForgeCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return nil, f.Fail
	}
	out := slices.Clone(f.checks[forgeKey{scope, request}])
	slices.SortFunc(out, func(a, b ports.ForgeCheck) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

func headKey(binding, branch string) string { return binding + "\x00" + branch }

// Push says the forge's repository now holds a branch at a commit, as a push from a checkout leaves it.
func (f *Forge) Push(binding ledger.ForgeBinding, branch, commit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.heads == nil {
		f.heads = map[string]string{}
	}
	f.heads[headKey(binding.String(), branch)] = commit
}

// Merge merges a pull request at its head, as a person does on the forge, with a merge commit.
func (f *Forge) Merge(number int64, mergeCommit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.pr.HeadCommit = f.heads[headKey(held.binding, held.pr.Branch)]
		held.pr.State, held.pr.MergeCommit, held.pr.MergedAt = ledger.PullRequestMerged, mergeCommit, "2026-01-02T03:04:05Z"
	}
}

// Close closes a pull request with no merge.
func (f *Forge) Close(number int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.pr.HeadCommit = f.heads[headKey(held.binding, held.pr.Branch)]
		held.pr.State = ledger.PullRequestClosed
	}
}

// SetChecks replaces the checks a pull request's head reports.
func (f *Forge) SetChecks(number int64, checks ...ledger.PullRequestCheck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.pr.Checks = slices.Clone(checks)
	}
}

// SignOut makes the sign-in for a host report a problem, and SignIn mends it.
func (f *Forge) SignOut(host, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.health == nil {
		f.health = map[string]ledger.ForgeHealthReport{}
	}
	f.health[host] = ledger.ForgeHealthReport{State: ledger.ForgeSignedOut, Detail: detail}
}

// SignIn mends a sign-in SignOut broke.
func (f *Forge) SignIn(host string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.health, host)
}

// Remove stands for a machine with no CLI, and Install for one that has it again.
func (f *Forge) Remove() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing = true
}

// Install undoes Remove.
func (f *Forge) Install() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing = false
}

// Comments lists what was posted on a pull request, in order.
func (f *Forge) Comments(number int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		return slices.Clone(held.comments)
	}
	return nil
}

// PullRequests lists every pull request the forge holds, by number.
func (f *Forge) PullRequests() []ledger.PullRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ledger.PullRequest, 0, len(f.prs))
	for _, held := range f.prs {
		out = append(out, f.current(held))
	}
	return out
}

// Calls lists each operation a worker ran, as its name and its pull request or branch.
func (f *Forge) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *Forge) find(number int64) *forgePR {
	for _, held := range f.prs {
		if held.pr.Number == number {
			return held
		}
	}
	return nil
}

// current is a pull request as a read sees it: an open one's head is what the forge holds for its branch.
func (f *Forge) current(held *forgePR) ledger.PullRequest {
	pr := held.pr
	pr.Checks = slices.Clone(pr.Checks)
	slices.SortFunc(pr.Checks, func(a, b ledger.PullRequestCheck) int { return cmp.Compare(a.Name, b.Name) })
	if pr.State == ledger.PullRequestOpen {
		pr.HeadCommit = f.heads[headKey(held.binding, pr.Branch)]
	}
	return pr
}

func (f *Forge) usable(host string) error {
	if f.Fail != nil {
		return f.Fail
	}
	if f.missing {
		return fmt.Errorf("%w: no gh on this machine", ports.ErrForgeUnavailable)
	}
	if report, ok := f.health[host]; ok {
		return fmt.Errorf("%w: %s", ports.ErrForgeUnavailable, report.Detail)
	}
	return nil
}

// OpenPullRequest opens the branch's pull request, or returns the one that is open. A branch the forge
// holds no commit for is refused, as a forge refuses a pull request of a branch nobody pushed.
func (f *Forge) OpenPullRequest(_ context.Context, spec ports.PullRequestSpec) (ledger.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "open "+spec.Branch)
	if err := f.usable(spec.Repo.Binding.Host); err != nil {
		return ledger.PullRequest{}, err
	}
	binding := spec.Repo.Binding.String()
	var number int64
	for _, held := range f.prs {
		if held.binding == binding && held.pr.Branch == spec.Branch && held.pr.State == ledger.PullRequestOpen {
			return f.current(held), nil
		}
		number = max(number, held.pr.Number)
	}
	if f.heads[headKey(binding, spec.Branch)] == "" {
		return ledger.PullRequest{}, fmt.Errorf("the forge holds no branch %s to open a pull request from", spec.Branch)
	}
	number++
	held := &forgePR{binding: binding, keys: map[string]bool{}, pr: ledger.PullRequest{
		Number: number, URL: fmt.Sprintf("https://%s/pull/%d", binding, number), State: ledger.PullRequestOpen,
		Base: spec.Base, Branch: spec.Branch, Mergeable: "MERGEABLE",
	}}
	f.prs = append(f.prs, held)
	return f.current(held), nil
}

// PullRequest reads one pull request. A number the forge does not hold is ErrNotFound.
func (f *Forge) PullRequest(_ context.Context, ref ports.PullRequestRef) (ledger.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("read %d", ref.Number))
	if err := f.usable(ref.Repo.Binding.Host); err != nil {
		return ledger.PullRequest{}, err
	}
	held := f.find(ref.Number)
	if held == nil || held.binding != ref.Repo.Binding.String() {
		return ledger.PullRequest{}, fmt.Errorf("pull request %d: %w", ref.Number, ports.ErrNotFound)
	}
	return f.current(held), nil
}

// Comment posts once per key.
func (f *Forge) Comment(_ context.Context, ref ports.PullRequestRef, key, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("comment %d", ref.Number))
	if err := f.usable(ref.Repo.Binding.Host); err != nil {
		return err
	}
	held := f.find(ref.Number)
	if held == nil || held.binding != ref.Repo.Binding.String() {
		return fmt.Errorf("pull request %d: %w", ref.Number, ports.ErrNotFound)
	}
	if held.keys[key] {
		return nil
	}
	held.keys[key] = true
	held.comments = append(held.comments, strings.TrimRight(body, "\n"))
	return nil
}

// Health reports the sign-in for the binding's host.
func (f *Forge) Health(_ context.Context, repo ports.ForgeRepo) (ledger.ForgeHealthReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "health "+repo.Binding.Host)
	if f.Fail != nil {
		return ledger.ForgeHealthReport{}, f.Fail
	}
	if f.missing {
		return ledger.ForgeHealthReport{State: ledger.ForgeMissing, Detail: "gh is not on PATH"}, nil
	}
	if report, ok := f.health[repo.Binding.Host]; ok {
		return report, nil
	}
	return ledger.ForgeHealthReport{State: ledger.ForgeHealthy}, nil
}
