package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// What a worker does for the classification of finished checks (ADR-0037): it reads whether the base branch's tip fails
// the same checks, reruns the failed jobs of runs the control plane names, and reads the logs of the checks that failed.
// It decides none of it, and a log it reads is scrubbed and bounded before it leaves the worker.

// baseTipSeen is a read of a base branch's tip and when it was made.
type baseTipSeen struct {
	at   time.Time
	read ports.BaseChecksRead
}

// logReadBytes bounds what the forge's CLI output is read to before it is scrubbed, which holds the last of a log whose
// lines carry colour codes.
const logReadBytes = 4 * ledger.CILogHardBytes

// baseTip reads what failed on a base branch's tip, once for each repository and branch in a forge interval.
func (w *Worker) baseTip(ctx context.Context, project, repository string, repo ports.ForgeRepo, branch string) (ports.BaseChecksRead, error) {
	key := forgeKey(project, repository) + "\x00" + branch
	w.forgeLock.Lock()
	seen, ok := w.baseTips[key]
	w.forgeLock.Unlock()
	if ok && w.clock.Now().Sub(seen.at) < w.config.Lanes.forgeEvery() {
		return seen.read, nil
	}
	read, err := w.config.Lanes.Forge.BaseChecks(ctx, repo, branch)
	if err != nil {
		return ports.BaseChecksRead{}, err
	}
	w.rateKept(project, repository, read.Rate)
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	if w.baseTips == nil {
		w.baseTips = map[string]baseTipSeen{}
	}
	w.baseTips[key] = baseTipSeen{at: w.clock.Now(), read: read}
	return read, nil
}

// failedByConclusion says a check is a finished run that concluded `failure`, the one conclusion a red base excuses.
func failedByConclusion(check ledger.PullRequestCheck) bool {
	return check.Required && check.State == ledger.CheckFailed && check.Status == ledger.CIRunCompleted && strings.EqualFold(check.Conclusion, "failure")
}

// markBase marks each required check of a read that concluded `failure` and failed on the base branch's tip too with
// the commit it failed at. A read of the base the forge refuses does not fail the read of the pull request, whose
// merge and close are still news. It leaves the failing checks marked as unread, which holds them where no lane starts
// and nothing is rerun, because work sent back for a failure the base shares is a lane spent on what no change of it
// can mend, and a later read that can tell decides. The repository is told once for each stretch of refusals.
func (w *Worker) markBase(ctx context.Context, watched ledger.DesiredPullRequest, read *ledger.PullRequest) error {
	if read.State != ledger.PullRequestOpen || !slices.ContainsFunc(read.Checks, failedByConclusion) {
		w.refusalsEnd(watched)
		return nil
	}
	repo, ok := w.forgeTarget(ctx, watched)
	if !ok {
		return nil
	}
	tip, err := w.baseTip(ctx, watched.Project, watched.Repository, repo, read.Base)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	read.Checks = slices.Clone(read.Checks)
	if err != nil {
		reads := w.baseUnread(watched, err)
		for i, check := range read.Checks {
			if failedByConclusion(check) {
				read.Checks[i].BaseUnread, read.Checks[i].BaseUnreadReads = true, reads
			}
		}
		return nil
	}
	w.baseReadable(watched)
	for i, check := range read.Checks {
		if failedByConclusion(check) && slices.Contains(tip.Failed, check.Name) {
			read.Checks[i].RedOnBase = cmp.Or(tip.Commit, read.Base)
		}
	}
	return nil
}

// baseUnread holds a repository's reads for a rate limit as any refused read does, and says once, in a notice that
// holds nothing the forge wrote, that failed checks are held for want of a read of the base. It counts a refusal that is no
// rate limit among the reads of this pull request in a row, to ledger.CIBaseUnreadReads and no further, and returns the count: the
// control plane raises a finding to a person when it reaches it, and a count that stopped is a read that changes
// nothing in the ledger.
func (w *Worker) baseUnread(watched ledger.DesiredPullRequest, err error) int64 {
	limited := errors.Is(err, ports.ErrForgeRateLimited)
	if limited {
		w.rateLimited(watched.Project, watched.Repository, err)
	}
	key := forgeKey(watched.Project, watched.Repository)
	w.forgeLock.Lock()
	first := !w.baseNoticed[key]
	if w.baseNoticed == nil {
		w.baseNoticed = map[string]bool{}
	}
	w.baseNoticed[key] = true
	if w.baseRefused == nil {
		w.baseRefused = map[string]int64{}
	}
	counted := forgeKey(key, watched.WorkOrder)
	// A rate limit is a read that cannot tell yet and no refusal of the base, so it holds the check and counts for nothing.
	if !limited {
		w.baseRefused[counted] = min(w.baseRefused[counted]+1, ledger.CIBaseUnreadReads)
	}
	reads := w.baseRefused[counted]
	w.forgeLock.Unlock()
	if first {
		w.config.Notice(fmt.Sprintf("repository %s: the base branch's checks cannot be read, so a failed required check is held and nothing is sent back for it until they can", watched.Repository))
	}
	return reads
}

// baseReadable ends a stretch of refusals, so the next one is told again and counted from the first.
func (w *Worker) baseReadable(watched ledger.DesiredPullRequest) {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	delete(w.baseNoticed, forgeKey(watched.Project, watched.Repository))
	delete(w.baseRefused, forgeKey(forgeKey(watched.Project, watched.Repository), watched.WorkOrder))
}

// refusalsEnd forgets the refusals counted for a pull request that has no failed check left to place.
func (w *Worker) refusalsEnd(watched ledger.DesiredPullRequest) {
	w.forgeLock.Lock()
	defer w.forgeLock.Unlock()
	delete(w.baseRefused, forgeKey(forgeKey(watched.Project, watched.Repository), watched.WorkOrder))
}

// rerunRequest runs a rerun the control plane asked for, as a recorded effect. A rerun it names runs for stays on
// those runs, and one that names none starts again every run of the head that finished without passing.
func (w *Worker) rerunRequest(ctx context.Context, request ledger.DesiredForge, repo ports.ForgeRepo) (ledger.ForgeReport, error) {
	var runs []string
	if request.Body != "" {
		var refs []ledger.CICheckRef
		if err := json.Unmarshal([]byte(request.Body), &refs); err != nil {
			return ledger.ForgeReport{}, fmt.Errorf("the checks of rerun %s: %w", request.ID, err)
		}
		for _, ref := range refs {
			if run, _, ok := ledger.ActionsRun(ref.URL, repo.Binding); ok && !slices.Contains(runs, run) {
				runs = append(runs, run)
			}
		}
		if len(runs) == 0 {
			return ledger.ForgeReport{Rerun: &ledger.CIRerun{Refused: "no check of the rerun names a run of this repository"}}, nil
		}
	}
	effect := lanes.Effect{Key: forgeAttemptKey(request) + "/rerun", Kind: lanes.EffectRerun, Target: fmt.Sprint(request.Number)}
	recorded, err := w.forgeBracket(ctx, effect, func() (string, error) {
		result, err := w.config.Lanes.Forge.RerunChecks(ctx, ports.PullRequestRef{Repo: repo, Number: request.Number}, request.Commit, runs)
		if err != nil && len(result.Runs) == 0 {
			return "", err
		}
		if err != nil {
			// The forge failed after it started some runs again. They are on record as done, and the rest are the result's
			// to ask for as a rerun of their own, so no run is started twice.
			w.heldByRate(request, err)
		}
		raw, err := json.Marshal(result)
		return string(raw), err
	})
	if err != nil {
		w.heldByRate(request, err)
		return ledger.ForgeReport{}, err
	}
	var result ledger.CIRerun
	if err := json.Unmarshal([]byte(recorded), &result); err != nil {
		return ledger.ForgeReport{}, fmt.Errorf("the rerun on record: %w", err)
	}
	return ledger.ForgeReport{Rerun: &result}, nil
}

// heldByRate holds a repository's reads after the forge refused a request for a rate limit, as a refused read does.
func (w *Worker) heldByRate(request ledger.DesiredForge, err error) {
	if errors.Is(err, ports.ErrForgeRateLimited) {
		w.rateLimited(request.Project, request.Repository, err)
	}
}

// logsRequest reads the log of each check a logs request names. A log is the end of what the forge's CLI printed for
// the failed steps, scrubbed of escapes, control characters and the shapes of credentials, and cut to the most the
// control plane will ever keep. A check whose log cannot be read says so, because work waits for no log: only a forge
// that cannot be reached, or one over its rate limit, is tried again.
func (w *Worker) logsRequest(ctx context.Context, request ledger.DesiredForge, repo ports.ForgeRepo) (ledger.ForgeReport, error) {
	var refs []ledger.CICheckRef
	if err := json.Unmarshal([]byte(request.Body), &refs); err != nil {
		return ledger.ForgeReport{}, fmt.Errorf("the checks of logs request %s: %w", request.ID, err)
	}
	logs := make([]ledger.CICheckLog, 0, len(refs))
	for _, ref := range refs {
		raw, err := w.config.Lanes.Forge.FailedLog(ctx, repo, request.Commit, ref, logReadBytes)
		if errors.Is(err, ports.ErrRunNotOfHead) {
			logs = append(logs, ledger.CICheckLog{Check: ref.Name, URL: ref.URL, Key: ref.Key, Refused: true})
			continue
		}
		if err != nil {
			w.heldByRate(request, err)
			if errors.Is(err, ports.ErrForgeRateLimited) || errors.Is(err, ports.ErrForgeUnavailable) || ctx.Err() != nil {
				return ledger.ForgeReport{}, err
			}
			logs = append(logs, ledger.CICheckLog{Check: ref.Name, URL: ref.URL, Key: ref.Key, Note: "the forge would not give its log"})
			continue
		}
		text, cut := ledger.CIExcerpt(raw, ledger.CILogHardLines, ledger.CILogHardBytes)
		entry := ledger.CICheckLog{Check: ref.Name, URL: ref.URL, Key: ref.Key, Text: text}
		switch {
		case text == "":
			entry.Note = "the forge holds no log of what failed in it"
		case cut:
			entry.Note = "the end of a longer log"
		}
		logs = append(logs, entry)
	}
	return ledger.ForgeReport{Logs: logs}, nil
}
