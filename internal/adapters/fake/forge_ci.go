package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// SetLog says what the log of a failing check holds, by the check's name, as `gh run view --log-failed` would print
// it.
func (f *Forge) SetLog(check, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.logs == nil {
		f.logs = map[string]string{}
	}
	f.logs[check] = text
}

// SetRun says which head a workflow run belongs to, as the forge knows it.
func (f *Forge) SetRun(run, head string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runs == nil {
		f.runs = map[string]string{}
	}
	f.runs[run] = head
}

// SetBase says which checks concluded `failure` on a branch's tip, and at which commit.
func (f *Forge) SetBase(binding ledger.ForgeBinding, branch, commit string, failed ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bases == nil {
		f.bases = map[string]baseTip{}
	}
	f.bases[headKey(binding.String(), branch)] = baseTip{commit: commit, failed: slices.Clone(failed)}
}

// RefuseReruns makes every rerun come back refused with the reason, as the forge does for a run that is still going.
// An empty reason lets them be taken again.
func (f *Forge) RefuseReruns(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuseRerun = reason
}

// Reruns lists each rerun the forge took, as the pull request, the head, and the runs asked for.
func (f *Forge) Reruns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reruns)
}

// RerunChecks takes a rerun of runs of a head, and says which it started again. With none named it starts again the
// one run the forge's checks for the head stand for.
func (f *Forge) RerunChecks(_ context.Context, ref ports.PullRequestRef, head string, runs []string) (ledger.CIRerun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("rerun %d %s", ref.Number, strings.Join(runs, ",")))
	if err := f.readable(ref.Repo.Binding.Host); err != nil {
		return ledger.CIRerun{}, err
	}
	if held := f.find(ref.Number); held == nil || held.binding != ref.Repo.Binding.String() {
		return ledger.CIRerun{}, fmt.Errorf("pull request %d: %w", ref.Number, ports.ErrNotFound)
	}
	if f.refuseRerun != "" {
		return ledger.CIRerun{Refused: f.refuseRerun}, nil
	}
	if len(runs) == 0 {
		f.reruns = append(f.reruns, fmt.Sprintf("%d %s all", ref.Number, head))
		return ledger.CIRerun{Runs: []string{"all"}}, nil
	}
	var result ledger.CIRerun
	var refusals []string
	for i, run := range runs {
		// A run the forge does not hold as a run of the head is refused, as the forge's own answer for it says.
		if f.runs[run] != head {
			refusals = append(refusals, fmt.Sprintf("run %s is not a run of the head", run))
			continue
		}
		if f.failRuns[run] > 0 {
			f.failRuns[run]--
			result.Refused, result.Pending = strings.Join(refusals, "; "), slices.Clone(runs[i:])
			return result, fmt.Errorf("the fake forge failed on run %s", run)
		}
		f.reruns = append(f.reruns, fmt.Sprintf("%d %s %s", ref.Number, head, run))
		result.Runs = append(result.Runs, run)
	}
	result.Refused = strings.Join(refusals, "; ")
	return result, nil
}

// FailRerunOf makes the next rerun of a run fail on the forge's side, for the number of times given, after the runs
// before it in the same rerun were started again.
func (f *Forge) FailRerunOf(run string, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRuns == nil {
		f.failRuns = map[string]int{}
	}
	f.failRuns[run] = times
}

// SetLogOf says what the log of the check with a key, the run and job its address names, holds.
func (f *Forge) SetLogOf(key, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.logsByKey == nil {
		f.logsByKey = map[string]string{}
	}
	f.logsByKey[key] = text
}

// FailedLog reads what a failing check's log holds, its end when it is longer than maxBytes. The run the check's
// address names must be a run the forge holds for the head, as the forge's own answer says.
func (f *Forge) FailedLog(_ context.Context, repo ports.ForgeRepo, head string, check ledger.CICheckRef, maxBytes int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "log "+check.Name)
	if err := f.readable(repo.Binding.Host); err != nil {
		return "", err
	}
	run, _, ok := ledger.ActionsRun(check.URL, repo.Binding)
	if ok && f.runs[run] != head {
		return "", fmt.Errorf("%w: run %s", ports.ErrRunNotOfHead, run)
	}
	text, byKey := f.logsByKey[ledger.ActionsKey(check.URL, repo.Binding)]
	if !byKey {
		text = f.logs[check.Name]
	}
	if maxBytes > 0 && len(text) > maxBytes {
		text = text[len(text)-maxBytes:]
	}
	return text, nil
}

// FailBase makes every read of a branch's tip fail with the error while it is set, as a forge that refuses that read
// does, whatever it answers of the rest. A nil error lets it read again.
func (f *Forge) FailBase(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.baseErr = err
}

// BaseChecks reads which checks concluded `failure` on a branch's tip.
func (f *Forge) BaseChecks(_ context.Context, repo ports.ForgeRepo, branch string) (ports.BaseChecksRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "base "+branch)
	if f.baseErr != nil {
		return ports.BaseChecksRead{}, f.baseErr
	}
	if err := f.readable(repo.Binding.Host); err != nil {
		return ports.BaseChecksRead{}, err
	}
	tip := f.bases[headKey(repo.Binding.String(), branch)]
	return ports.BaseChecksRead{Commit: tip.commit, Failed: slices.Clone(tip.failed)}, nil
}
