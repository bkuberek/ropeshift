package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// defaultForgeEvery is how often a worker reads its forge sign-in for each repository, and how long a forge
// request that failed waits before its next attempt.
const defaultForgeEvery = time.Minute

// The bound on how long a publish waits for the forge to show the pushed commit as its pull request's
// head, which a forge updates a moment after a push.
const (
	headReads = 5
	headWait  = 500 * time.Millisecond
)

// errForgeBusy says another goroutine of this process holds the effect.
var errForgeBusy = errors.New("another run holds this forge effect")

// forgeHealthSeen is what a worker last found of its sign-in for one repository, and what it last told the
// ledger.
type forgeHealthSeen struct {
	checkedAt time.Time
	found     ledger.ForgeHealthReport
	told      bool
	toldAs    ledger.ForgeHealthReport
}

func (rt *LaneRuntime) forgeEvery() time.Duration {
	if rt.ForgeEvery > 0 {
		return rt.ForgeEvery
	}
	return defaultForgeEvery
}

// forgeKey names one project's repository or request.
func forgeKey(project, id string) string { return project + "\x00" + id }

// tendForge runs what the control plane asks of this worker on a forge (ADR-0020): it reads its sign-in
// for each repository that reviews through pull requests and that it keeps a checkout of, and runs each
// forge request it has not reported, among them a branch merge, which needs no forge. Each runs on a
// goroutine of its own, so a slow forge holds no lane.
func (w *Worker) tendForge(ctx context.Context) {
	rt := w.config.Lanes
	if rt == nil {
		return
	}
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading what the forge needs: %w", err))
		return
	}
	for _, repository := range state.ForgeRepositories {
		if rt.Forge != nil && w.healthDue(repository) {
			w.goForge(ctx, "health/"+forgeKey(repository.Project, repository.Repository), func(ctx context.Context) {
				w.checkForgeHealth(ctx, repository)
			})
		}
	}
	for _, request := range state.Forge {
		if (rt.Forge == nil && request.Binding != "") || !w.forgeDue(request) {
			continue
		}
		w.goForge(ctx, "request/"+forgeKey(request.Project, request.ID), func(ctx context.Context) {
			w.runForge(ctx, request)
		})
	}
	for _, watched := range state.PullRequests {
		if rt.Forge == nil || !w.readDue(watched) {
			continue
		}
		w.goForge(ctx, "read/"+forgeKey(watched.Project, watched.WorkOrder), func(ctx context.Context) {
			w.readPullRequest(ctx, watched)
		})
	}
}

// readDue paces the reads of one watched pull request, and holds them while the repository's sign-in this
// worker found cannot run.
func (w *Worker) readDue(watched ledger.DesiredPullRequest) bool {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	seen, ok := w.forgeHealth[forgeKey(watched.Project, watched.Repository)]
	if !ok || seen.found.State != ledger.ForgeHealthy {
		return false
	}
	last, read := w.forgeRead[forgeKey(watched.Project, watched.WorkOrder)]
	return !read || w.clock.Now().Sub(last.at) >= w.config.Lanes.forgeEvery()
}

// forgeReadSeen is when a worker last read a watched pull request, and what it last told the ledger of it.
type forgeReadSeen struct {
	at   time.Time
	told *ledger.PullRequest
}

// readPullRequest reads one pull request this worker watches for the ledger, and reports it when it differs
// from what the ledger last heard: a merge, a close, a head the forge now shows, or new checks.
func (w *Worker) readPullRequest(ctx context.Context, watched ledger.DesiredPullRequest) {
	rt := w.config.Lanes
	key := forgeKey(watched.Project, watched.WorkOrder)
	w.forgeLock.Lock()
	if w.forgeRead == nil {
		w.forgeRead = map[string]forgeReadSeen{}
	}
	seen := w.forgeRead[key]
	seen.at = w.clock.Now()
	w.forgeRead[key] = seen
	w.forgeLock.Unlock()
	dir, err := rt.Repository(ctx, watched.Project, watched.Repository)
	if err != nil {
		return
	}
	binding, err := ledger.ParseForgeBinding(watched.Binding)
	if err != nil {
		return
	}
	read, err := rt.Forge.PullRequest(ctx, ports.PullRequestRef{Repo: ports.ForgeRepo{Dir: dir, Binding: binding}, Number: watched.Number})
	if err != nil {
		w.config.Report(fmt.Errorf("pull request %d of %s: %w", watched.Number, watched.WorkOrder, err))
		w.recheckHealth(watched.Project, watched.Repository)
		return
	}
	if seen.told != nil && sameRead(*seen.told, read) {
		return
	}
	if seen.told == nil && read.State == watched.State && read.HeadCommit == watched.HeadCommit && read.MergeCommit == watched.MergeCommit &&
		read.Mergeable == watched.Mergeable && slices.Equal(read.Checks, watched.Checks) {
		w.forgeLock.Lock()
		seen.told = &read
		w.forgeRead[key] = seen
		w.forgeLock.Unlock()
		return
	}
	payload, err := json.Marshal(ledger.PullRequestReport{WorkOrder: watched.WorkOrder, PullRequest: read})
	if err == nil {
		_, err = w.Record(ctx, w.event(ledger.UploadPullRequestRead, lanes.Record{Project: watched.Project, WorkOrder: watched.WorkOrder}, payload))
	}
	if err != nil {
		w.config.Report(fmt.Errorf("pull request %d of %s: %w", watched.Number, watched.WorkOrder, err))
		return
	}
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	seen.told = &read
	w.forgeRead[key] = seen
}

// sameRead compares what a read of a pull request can change.
func sameRead(a, b ledger.PullRequest) bool {
	return a.State == b.State && a.HeadCommit == b.HeadCommit && a.Mergeable == b.Mergeable && a.MergeCommit == b.MergeCommit &&
		a.MergedAt == b.MergedAt && a.Base == b.Base && slices.Equal(a.Checks, b.Checks)
}

// goForge runs one forge step on a goroutine the lanes' wait covers, and none while another runs the same.
func (w *Worker) goForge(ctx context.Context, key string, step func(context.Context)) {
	if !w.takeEffect("run/" + key) {
		return
	}
	w.laneGroup.Add(1)
	go func() {
		defer w.laneGroup.Done()
		defer w.leaveEffect("run/" + key)
		step(ctx)
	}()
}

func (w *Worker) healthDue(repository ledger.DesiredForgeRepository) bool {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	seen, ok := w.forgeHealth[forgeKey(repository.Project, repository.Repository)]
	return !ok || w.clock.Now().Sub(seen.checkedAt) >= w.config.Lanes.forgeEvery()
}

// forgeDue holds back a request whose report is on its way, a request whose last attempt failed until it has
// waited, and one in a repository where this worker has not yet found a sign-in that runs.
func (w *Worker) forgeDue(request ledger.DesiredForge) bool {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	if w.forgeReported[forgeAttemptKey(request)] {
		return false
	}
	if failed, ok := w.forgeFailed[forgeKey(request.Project, request.ID)]; ok && w.clock.Now().Sub(failed) < w.config.Lanes.forgeEvery() {
		return false
	}
	// A branch merge names no binding, because it runs git and no forge.
	if request.Binding == "" {
		return true
	}
	seen, ok := w.forgeHealth[forgeKey(request.Project, request.Repository)]
	return ok && seen.found.State == ledger.ForgeHealthy
}

func forgeAttemptKey(request ledger.DesiredForge) string {
	return fmt.Sprintf("forge/%s/%s/%d", request.Project, request.ID, request.Attempt)
}

// checkForgeHealth reads the sign-in for one repository this worker keeps a checkout of, and tells the ledger
// when what it found differs from what it last told. A repository with no checkout here is another
// worker's to read.
func (w *Worker) checkForgeHealth(ctx context.Context, repository ledger.DesiredForgeRepository) {
	rt := w.config.Lanes
	dir, err := rt.Repository(ctx, repository.Project, repository.Repository)
	if err != nil {
		w.noteHealth(repository, nil)
		return
	}
	binding, err := ledger.ParseForgeBinding(repository.Binding)
	if err != nil {
		w.config.Report(fmt.Errorf("repository %s: %w", repository.Repository, err))
		w.noteHealth(repository, nil)
		return
	}
	found, err := rt.Forge.Health(ctx, ports.ForgeRepo{Dir: dir, Binding: binding})
	if err != nil {
		w.config.Report(fmt.Errorf("repository %s: reading the forge sign-in: %w", repository.Repository, err))
		w.noteHealth(repository, nil)
		return
	}
	found.Repository = repository.Repository
	if !w.noteHealth(repository, &found) {
		return
	}
	payload, err := json.Marshal(found)
	if err == nil {
		_, err = w.Record(ctx, w.event(ledger.UploadForgeHealth, lanes.Record{Project: repository.Project}, payload))
	}
	if err != nil {
		w.config.Report(fmt.Errorf("repository %s: %w", repository.Repository, err))
		return
	}
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	seen := w.forgeHealth[forgeKey(repository.Project, repository.Repository)]
	seen.told, seen.toldAs = true, found
	w.forgeHealth[forgeKey(repository.Project, repository.Repository)] = seen
}

// noteHealth keeps when a repository was read and what was found, and reports whether that is news to the
// ledger.
func (w *Worker) noteHealth(repository ledger.DesiredForgeRepository, found *ledger.ForgeHealthReport) bool {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	if w.forgeHealth == nil {
		w.forgeHealth = map[string]forgeHealthSeen{}
	}
	key := forgeKey(repository.Project, repository.Repository)
	seen := w.forgeHealth[key]
	seen.checkedAt = w.clock.Now()
	if found == nil {
		w.forgeHealth[key] = seen
		return false
	}
	seen.found = *found
	w.forgeHealth[key] = seen
	return !seen.told || seen.toldAs != *found
}

// recheckHealth makes the next pass read a repository's sign-in again, after a forge request there failed.
func (w *Worker) recheckHealth(project, repository string) {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	if seen, ok := w.forgeHealth[forgeKey(project, repository)]; ok {
		seen.checkedAt = time.Time{}
		w.forgeHealth[forgeKey(project, repository)] = seen
	}
}

// runForge runs one attempt at a forge request and journals its report. A failure is a report too, which
// the ledger keeps with the request before it offers the next attempt.
func (w *Worker) runForge(ctx context.Context, request ledger.DesiredForge) {
	report, err := w.performForge(ctx, request)
	report.Request, report.Attempt = request.ID, request.Attempt
	if errors.Is(err, errForgeBusy) {
		return
	}
	if err != nil {
		report.Failed, report.Detail, report.PullRequest, report.Merge = true, err.Error(), nil, nil
		w.forgeLock.Lock()
		if w.forgeFailed == nil {
			w.forgeFailed = map[string]time.Time{}
		}
		w.forgeFailed[forgeKey(request.Project, request.ID)] = w.clock.Now()
		w.forgeLock.Unlock()
		w.recheckHealth(request.Project, request.Repository)
	}
	payload, err := json.Marshal(report)
	if err == nil {
		_, err = w.Record(ctx, w.event(ledger.UploadForgeReported, lanes.Record{Project: request.Project, WorkOrder: request.WorkOrder}, payload))
	}
	if err != nil {
		w.config.Report(fmt.Errorf("forge request %s: %w", request.ID, err))
		return
	}
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	if w.forgeReported == nil {
		w.forgeReported = map[string]bool{}
	}
	w.forgeReported[forgeAttemptKey(request)] = true
}

// performForge runs one forge request and returns what its report carries beyond the request it answers.
func (w *Worker) performForge(ctx context.Context, request ledger.DesiredForge) (ledger.ForgeReport, error) {
	rt := w.config.Lanes
	dir, err := rt.Repository(ctx, request.Project, request.Repository)
	if err != nil {
		return ledger.ForgeReport{}, err
	}
	if request.Kind == ledger.ForgeMerge && request.Binding == "" {
		return w.mergeBranch(ctx, request, dir)
	}
	binding, err := ledger.ParseForgeBinding(request.Binding)
	if err != nil {
		return ledger.ForgeReport{}, err
	}
	repo := ports.ForgeRepo{Dir: dir, Binding: binding}
	switch request.Kind {
	case ledger.ForgePublish:
		pr, err := w.publish(ctx, request, repo)
		return ledger.ForgeReport{PullRequest: &pr}, err
	case ledger.ForgeComment:
		comment := lanes.Effect{Key: forgeAttemptKey(request) + "/comment", Kind: lanes.EffectComment, Target: fmt.Sprint(request.Number)}
		_, err := w.forgeBracket(ctx, comment, func() (string, error) {
			return "", rt.Forge.Comment(ctx, ports.PullRequestRef{Repo: repo, Number: request.Number}, request.ID, request.Body)
		})
		return ledger.ForgeReport{}, err
	case ledger.ForgeMerge:
		merge := lanes.Effect{Key: forgeAttemptKey(request) + "/merge", Kind: lanes.EffectMerge, Target: fmt.Sprint(request.Number)}
		result, err := w.mergeBracket(ctx, merge, func() (ledger.MergeResult, error) {
			return rt.Forge.Merge(ctx, ports.PullRequestRef{Repo: repo, Number: request.Number}, request.Commit, request.Method)
		})
		return ledger.ForgeReport{Merge: &result}, err
	}
	return ledger.ForgeReport{}, fmt.Errorf("this worker runs no forge request of kind %s", request.Kind)
}

// mergeBranch merges a reviewed branch into its base with git, in the repository's checkout here, as one
// recorded effect: review target `branch` has no forge, and the worker that ran the lane holds the branch.
func (w *Worker) mergeBranch(ctx context.Context, request ledger.DesiredForge, dir string) (ledger.ForgeReport, error) {
	merger, ok := w.config.Lanes.Provisioner.Worktrees.(ports.BranchMerger)
	if !ok {
		return ledger.ForgeReport{}, errors.New("this worker's worktrees cannot merge a branch")
	}
	merge := lanes.Effect{Key: forgeAttemptKey(request) + "/merge", Kind: lanes.EffectMerge, Target: request.Branch}
	result, err := w.mergeBracket(ctx, merge, func() (ledger.MergeResult, error) {
		return merger.MergeBranch(ctx, dir, ports.BranchMerge{
			Branch: request.Branch, Base: request.Base, Commit: request.Commit, Method: request.Method, WorkOrder: request.WorkOrder, Title: request.Title,
		})
	})
	return ledger.ForgeReport{Merge: &result}, err
}

// mergeBracket performs a merge as a recorded effect, keeping what it came to with the record, so a repeat
// finds the result and merges nothing twice.
func (w *Worker) mergeBracket(ctx context.Context, effect lanes.Effect, merge func() (ledger.MergeResult, error)) (ledger.MergeResult, error) {
	recorded, err := w.forgeBracket(ctx, effect, func() (string, error) {
		result, err := merge()
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(result)
		return string(raw), err
	})
	if err != nil {
		return ledger.MergeResult{}, err
	}
	var result ledger.MergeResult
	if err := json.Unmarshal([]byte(recorded), &result); err != nil {
		return ledger.MergeResult{}, fmt.Errorf("the merge on record: %w", err)
	}
	return result, nil
}

// publish pushes the branch, then opens its pull request, each a recorded effect under a key of the
// request's attempt. A forge shows a push as the pull request's head a moment later, so the head is read
// again for a short while before the report goes.
func (w *Worker) publish(ctx context.Context, request ledger.DesiredForge, repo ports.ForgeRepo) (ledger.PullRequest, error) {
	rt := w.config.Lanes
	key := forgeAttemptKey(request)
	push := lanes.Effect{Key: key + "/push", Kind: lanes.EffectPush, Target: request.Branch}
	if _, err := w.forgeBracket(ctx, push, func() (string, error) {
		return "", rt.Provisioner.Worktrees.PushBranch(ctx, repo.Dir, request.Branch)
	}); err != nil {
		return ledger.PullRequest{}, err
	}
	open := lanes.Effect{Key: key + "/open", Kind: lanes.EffectPullRequest, Target: request.Branch}
	result, err := w.forgeBracket(ctx, open, func() (string, error) {
		pr, err := rt.Forge.OpenPullRequest(ctx, ports.PullRequestSpec{Repo: repo, Base: request.Base, Branch: request.Branch, Title: request.Title, Body: request.Body})
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(pr)
		return string(raw), err
	})
	if err != nil {
		return ledger.PullRequest{}, err
	}
	var pr ledger.PullRequest
	if err := json.Unmarshal([]byte(result), &pr); err != nil {
		return ledger.PullRequest{}, fmt.Errorf("the pull request on record: %w", err)
	}
	for range headReads {
		if pr.State != ledger.PullRequestOpen || pr.HeadCommit == request.Commit {
			break
		}
		select {
		case <-ctx.Done():
			return pr, nil
		case <-time.After(headWait):
		}
		read, err := rt.Forge.PullRequest(ctx, ports.PullRequestRef{Repo: repo, Number: pr.Number})
		if err != nil {
			break
		}
		pr = read
	}
	return pr, nil
}

// forgeBracket performs one forge effect between two journal writes, keeping what it produced with the
// record, so a repeat finds the result and acts nothing twice. An effect a crash left unknown runs again, which
// each forge effect is safe to do.
func (w *Worker) forgeBracket(ctx context.Context, effect lanes.Effect, perform func() (string, error)) (string, error) {
	if !w.takeEffect(effect.Key) {
		return "", errForgeBusy
	}
	defer w.leaveEffect(effect.Key)
	begun, err := w.journal.BeginEffect(ctx, effect)
	if err != nil {
		return "", err
	}
	if !begun {
		current, err := w.journal.Effect(ctx, effect.Key)
		if err != nil {
			return "", err
		}
		switch current.State {
		case lanes.EffectCompleted:
			return current.Detail, nil
		case lanes.EffectFailed:
			return "", errors.New(current.Detail)
		case lanes.EffectUnknown:
			if _, err := w.journal.RetryEffect(ctx, effect.Key); err != nil {
				return "", err
			}
		}
	}
	result, failure := perform()
	state, detail := lanes.EffectCompleted, result
	if failure != nil {
		state, detail = lanes.EffectFailed, failure.Error()
	}
	if err := w.journal.FinishEffect(context.WithoutCancel(ctx), effect.Key, state, detail); err != nil {
		return "", errors.Join(failure, err)
	}
	return result, failure
}
