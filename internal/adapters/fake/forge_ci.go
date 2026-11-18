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
		runs = []string{"all"}
	}
	f.reruns = append(f.reruns, fmt.Sprintf("%d %s %s", ref.Number, head, strings.Join(runs, ",")))
	return ledger.CIRerun{Runs: slices.Clone(runs)}, nil
}

// FailedLog reads what a failing check's log holds, its end when it is longer than maxBytes.
func (f *Forge) FailedLog(_ context.Context, repo ports.ForgeRepo, check ledger.CICheckRef, maxBytes int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "log "+check.Name)
	if err := f.readable(repo.Binding.Host); err != nil {
		return "", err
	}
	text := f.logs[check.Name]
	if maxBytes > 0 && len(text) > maxBytes {
		text = text[len(text)-maxBytes:]
	}
	return text, nil
}

// BaseChecks reads which checks concluded `failure` on a branch's tip.
func (f *Forge) BaseChecks(_ context.Context, repo ports.ForgeRepo, branch string) (ports.BaseChecksRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "base "+branch)
	if err := f.readable(repo.Binding.Host); err != nil {
		return ports.BaseChecksRead{}, err
	}
	tip := f.bases[headKey(repo.Binding.String(), branch)]
	return ports.BaseChecksRead{Commit: tip.commit, Failed: slices.Clone(tip.failed)}, nil
}
