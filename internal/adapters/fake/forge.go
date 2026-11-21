package fake

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

type forgeKey struct {
	scope   ledger.WorkspaceID
	request ports.ForgeRequest
}

// forgePR is one pull request the fake forge holds.
type forgePR struct {
	binding string
	pr      ledger.PullRequest
	// title, draft, and updatedAt are what a listing shows beside the pull request.
	title     string
	draft     bool
	crossRepo bool
	updatedAt string
	comments  []string
	keys      map[string]bool
	// refusal is what a merge of it comes to while it is set, and method is how it was merged.
	refusal *ledger.MergeResult
	method  ledger.MergeMethod
	// rev counts what changed of it, which a probe's tag carries, as a response's ETag changes with its body.
	rev int64
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
	// limit is the rate limit every read is refused for, while it is set.
	limit *ports.ForgeRate
	// poll is the poll interval a query or a probe reports.
	poll time.Duration
	// logs is what the log of a failing check holds, by the check's name.
	logs map[string]string
	// runs is the head each workflow run belongs to, by run.
	runs map[string]string
	// logsByKey is what the log of a check holds by its run and job, and failRuns the runs a rerun fails on.
	logsByKey map[string]string
	failRuns  map[string]int
	// baseErr is what a read of a branch's tip fails with while it is set.
	baseErr error
	// bases is what concluded `failure` on a branch's tip, by binding and branch.
	bases map[string]baseTip
	// reruns lists each rerun the forge took, and refuseRerun is why it refuses them while it is set.
	reruns      []string
	refuseRerun string
	// Fail is what every read and operation returns while it is set.
	Fail error
}

// baseTip is what a branch's tip holds of checks that failed.
type baseTip struct {
	commit string
	failed []string
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
	for _, held := range f.prs {
		if held.binding == binding.String() && held.pr.Branch == branch {
			held.rev++
		}
	}
}

// MergeByPerson merges a pull request at its head, as a person does on the forge, with a merge commit.
func (f *Forge) MergeByPerson(number int64, mergeCommit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.pr.HeadCommit = f.heads[headKey(held.binding, held.pr.Branch)]
		held.pr.State, held.pr.MergeCommit, held.pr.MergedAt = ledger.PullRequestMerged, mergeCommit, "2026-01-02T03:04:05Z"
		held.rev++
	}
}

// Refuse makes every merge of a pull request come to an outcome other than a merge, such as a conflict or
// branch protection, with what the forge says of it. A nil result lets it merge again.
func (f *Forge) Refuse(number int64, result *ledger.MergeResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.refusal = result
		held.rev++
		if result != nil && result.Outcome == ledger.MergeConflict {
			held.pr.Mergeable = "CONFLICTING"
		} else {
			held.pr.Mergeable = "MERGEABLE"
		}
	}
}

// MergedWith says how a pull request was merged, or nothing while it is not.
func (f *Forge) MergedWith(number int64) ledger.MergeMethod {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		return held.method
	}
	return ""
}

// Close closes a pull request with no merge.
func (f *Forge) Close(number int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.pr.HeadCommit = f.heads[headKey(held.binding, held.pr.Branch)]
		held.pr.State = ledger.PullRequestClosed
		held.rev++
	}
}

// SetChecks replaces the checks a pull request's head reports.
func (f *Forge) SetChecks(number int64, checks ...ledger.PullRequestCheck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.find(number); held != nil {
		held.pr.Checks = slices.Clone(checks)
		held.rev++
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
	held := &forgePR{binding: binding, title: spec.Title, keys: map[string]bool{}, pr: ledger.PullRequest{
		Number: number, URL: fmt.Sprintf("https://%s/pull/%d", binding, number), State: ledger.PullRequestOpen,
		Base: spec.Base, Branch: spec.Branch, Mergeable: "MERGEABLE",
	}}
	f.prs = append(f.prs, held)
	return f.current(held), nil
}

// HoldOpen says the forge holds open pull requests of a repository that nobody opened through it, as they
// stand in a listing.
func (f *Forge) HoldOpen(binding ledger.ForgeBinding, pulls ...ledger.PullRequestSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pull := range pulls {
		f.prs = append(f.prs, &forgePR{
			binding: binding.String(), title: pull.Title, draft: pull.Draft, crossRepo: pull.CrossRepository, updatedAt: pull.UpdatedAt, keys: map[string]bool{},
			pr: ledger.PullRequest{Number: pull.Number, URL: pull.URL, State: ledger.PullRequestOpen, Base: pull.Base, Branch: pull.Branch, Mergeable: "MERGEABLE"},
		})
	}
}

// OpenPullRequests lists the repository's open pull requests, newest first, at most MaxOpenPullRequests.
func (f *Forge) OpenPullRequests(_ context.Context, repo ports.ForgeRepo) ([]ledger.PullRequestSummary, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "list "+repo.Binding.String())
	if err := f.readable(repo.Binding.Host); err != nil {
		return nil, false, err
	}
	pulls := []ledger.PullRequestSummary{}
	for _, held := range f.prs {
		if held.binding == repo.Binding.String() && held.pr.State == ledger.PullRequestOpen {
			pulls = append(pulls, ledger.PullRequestSummary{
				Number: held.pr.Number, Title: held.title, Branch: held.pr.Branch, Base: held.pr.Base, URL: held.pr.URL, Draft: held.draft, CrossRepository: held.crossRepo, UpdatedAt: held.updatedAt,
			})
		}
	}
	slices.SortFunc(pulls, func(a, b ledger.PullRequestSummary) int { return cmp.Compare(b.Number, a.Number) })
	more := len(pulls) > ports.MaxOpenPullRequests
	if more {
		pulls = pulls[:ports.MaxOpenPullRequests]
	}
	return pulls, more, nil
}

// PullRequest reads one pull request. A number the forge does not hold is ErrNotFound.
func (f *Forge) PullRequest(_ context.Context, ref ports.PullRequestRef) (ledger.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("read %d", ref.Number))
	if err := f.readable(ref.Repo.Binding.Host); err != nil {
		return ledger.PullRequest{}, err
	}
	held := f.find(ref.Number)
	if held == nil || held.binding != ref.Repo.Binding.String() {
		return ledger.PullRequest{}, fmt.Errorf("pull request %d: %w", ref.Number, ports.ErrNotFound)
	}
	return f.current(held), nil
}

// Limit makes every read refuse for a rate limit, with the wait the rate names, until it is called with a zero
// rate.
func (f *Forge) Limit(rate ports.ForgeRate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limit = nil
	if rate != (ports.ForgeRate{}) {
		f.limit = &rate
	}
}

// PollEvery makes every query and probe say the endpoint asks for this much time between two reads, as an
// x-poll-interval header does. Zero says nothing.
func (f *Forge) PollEvery(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.poll = d
}

// readable is what a read needs before it answers: a forge that can be reached and signed in to, and a rate limit
// that has not refused it.
func (f *Forge) readable(host string) error {
	if err := f.usable(host); err != nil {
		return err
	}
	if f.limit != nil {
		return &ports.RateLimitedError{Rate: *f.limit, Detail: "the fake forge is over its limit"}
	}
	return nil
}

// ReadPullRequests reads each pull request of the repository the forge holds, in one call, and leaves out a number
// it does not hold.
func (f *Forge) ReadPullRequests(_ context.Context, repo ports.ForgeRepo, numbers []int64) (ports.PullRequestsRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("batch %s %v", repo.Binding, numbers))
	if err := f.readable(repo.Binding.Host); err != nil {
		return ports.PullRequestsRead{}, err
	}
	read := ports.PullRequestsRead{Pulls: map[int64]ledger.PullRequest{}, Rate: ports.ForgeRate{PollInterval: f.poll}}
	for _, number := range numbers {
		if held := f.find(number); held != nil && held.binding == repo.Binding.String() {
			read.Pulls[number] = f.current(held)
		}
	}
	return read, nil
}

// ProbeChecks says whether a pull request changed since the probe that gave the tag. The tag is what the pull
// request's revision and head were, so a push, a check, a merge, or a close each change it.
func (f *Forge) ProbeChecks(_ context.Context, ref ports.PullRequestRef, _, tag string) (ports.ChecksProbe, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("probe %d", ref.Number))
	if err := f.readable(ref.Repo.Binding.Host); err != nil {
		return ports.ChecksProbe{}, err
	}
	held := f.find(ref.Number)
	if held == nil || held.binding != ref.Repo.Binding.String() {
		return ports.ChecksProbe{}, fmt.Errorf("pull request %d: %w", ref.Number, ports.ErrNotFound)
	}
	now := fmt.Sprintf("%d:%s", held.rev, f.current(held).HeadCommit)
	return ports.ChecksProbe{Unchanged: tag == now, Tag: now, Rate: ports.ForgeRate{PollInterval: f.poll}}, nil
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

// Merge merges a pull request with a method while its head is the given commit, as the forge does for a
// worker. A pull request merged at that head answers merged again, and what Refuse set comes back as the
// refusal it names.
func (f *Forge) Merge(_ context.Context, ref ports.PullRequestRef, head string, method ledger.MergeMethod, _ string) (ledger.MergeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("merge %d %s", ref.Number, method))
	if err := f.usable(ref.Repo.Binding.Host); err != nil {
		return ledger.MergeResult{}, err
	}
	if method != ledger.MergeCommit && method != ledger.MergeSquash && method != ledger.MergeRebase {
		return ledger.MergeResult{}, fmt.Errorf("no forge merges with the method %q", method)
	}
	held := f.find(ref.Number)
	if held == nil || held.binding != ref.Repo.Binding.String() {
		return ledger.MergeResult{}, fmt.Errorf("pull request %d: %w", ref.Number, ports.ErrNotFound)
	}
	pr := f.current(held)
	switch {
	case pr.State == ledger.PullRequestMerged && pr.HeadCommit == head:
		return ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: pr.MergeCommit, MergedAt: pr.MergedAt}, nil
	case pr.State != ledger.PullRequestOpen:
		return ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: fmt.Sprintf("the pull request is %s", pr.State)}, nil
	case pr.HeadCommit != head:
		return ledger.MergeResult{Outcome: ledger.MergeStaleHead, Head: pr.HeadCommit, Detail: fmt.Sprintf("the pull request's head is %s, not the reviewed commit %s", pr.HeadCommit, head)}, nil
	case held.refusal != nil:
		return *held.refusal, nil
	}
	merges := 0
	for _, other := range f.prs {
		if other.pr.State == ledger.PullRequestMerged {
			merges++
		}
	}
	held.pr.HeadCommit, held.method = head, method
	held.pr.State, held.pr.MergeCommit, held.pr.MergedAt = ledger.PullRequestMerged, fmt.Sprintf("%040x", 0xfeed0000+merges+1), "2026-01-02T03:04:05Z"
	held.rev++
	return ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: held.pr.MergeCommit, MergedAt: held.pr.MergedAt}, nil
}
