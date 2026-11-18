package contract

import (
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// CIHistoryChecks are the CI records the control plane keeps (FR-SPEND-21): one when a head's required checks
// finish, the p50 and p85 of a repository's recent ones with a test result, and the schedule that reads them.
var CIHistoryChecks = []Check{
	{"CI3", "a head's required checks that finish make a CI record, a rerun makes another, and a read that changes nothing makes none", checkCIRecordsAreMade},
	{"CI4", "a repository has CI history after its fifth record with a test result, and the schedule reads it", checkCIHistoryAfterFiveRecords},
	{"CI5", "ci_history_min_records is the repository's own, so three records give history when it says three", checkCIHistoryMinimumIsTheRepositorys},
}

// ciRunAt is a time on the day the checks of these rigs run, in the form a forge writes it.
func ciRunAt(minutes int) string {
	return time.Date(2026, 1, 2, 9, minutes, 0, 0, time.UTC).Format(time.RFC3339)
}

// ciRead is one read of the rig's pull request at a head with one required check.
func (f *forgeRig) ciRead(head string, check ledger.PullRequestCheck) error {
	check.Name, check.Required = "unit", true
	return f.read(ledger.PullRequest{
		Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: ledger.PullRequestOpen, Base: "main", Branch: branchName,
		HeadCommit: head, Checks: []ledger.PullRequestCheck{check},
	})
}

func running(start int) ledger.PullRequestCheck {
	return ledger.PullRequestCheck{State: ledger.CheckPending, Status: ledger.CIRunInProgress, StartedAt: ciRunAt(start)}
}

func finished(conclusion string, state ledger.ForgeCheckState, start, end int) ledger.PullRequestCheck {
	return ledger.PullRequestCheck{
		State: state, Status: ledger.CIRunCompleted, Conclusion: conclusion, StartedAt: ciRunAt(start), CompletedAt: ciRunAt(end),
	}
}

func (f *forgeRig) ciRecords() ([]ledger.CIRecord, error) {
	var records []ledger.CIRecord
	err := f.store.Read(f.r.Ctx, f.r.Scope, func(tx ports.ReadTx) error {
		var err error
		records, err = tx.Forge().CIRecords(f.repository, 100)
		return err
	})
	return records, err
}

func (f *forgeRig) watchedHistory() (*ledger.CIHistory, error) {
	desired, err := f.desired()
	if err != nil {
		return nil, err
	}
	if len(desired.PullRequests) != 1 {
		return nil, Violatedf("the worker is asked to watch %+v", desired.PullRequests)
	}
	return desired.PullRequests[0].History, nil
}

func checkCIRecordsAreMade(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	if err := f.ciRead(f.head, running(0)); err != nil {
		return err
	}
	if records, err := f.ciRecords(); err != nil || len(records) != 0 {
		return Violatedf("a check still running left %+v: %v", records, err)
	}
	if err := f.ciRead(f.head, finished("success", ledger.CheckPassed, 0, 4)); err != nil {
		return err
	}
	records, err := f.ciRecords()
	if err != nil || len(records) != 1 {
		return Violatedf("finished checks left %+v: %v", records, err)
	}
	first := records[0]
	if first.Repository != f.repository || first.Head != f.head || first.Attempt != 1 || first.Result != ledger.CIPass ||
		first.DurationMs != (4*time.Minute).Milliseconds() || first.QueueMs != 0 {
		return Violatedf("the record reads %+v", first)
	}
	if err := f.ciRead(f.head, finished("success", ledger.CheckPassed, 0, 4)); err != nil {
		return err
	}
	if again, err := f.ciRecords(); err != nil || len(again) != 1 {
		return Violatedf("a read that changed nothing left %+v: %v", again, err)
	}
	if err := f.ciRead(f.head, running(6)); err != nil {
		return err
	}
	if err := f.ciRead(f.head, finished("failure", ledger.CheckFailed, 6, 11)); err != nil {
		return err
	}
	if records, err = f.ciRecords(); err != nil || len(records) != 2 || records[0].Attempt != 2 || records[0].Result != ledger.CIFailure {
		return Violatedf("a rerun left %+v: %v", records, err)
	}
	if err := f.ciRead("other-head", finished("timed_out", ledger.CheckFailed, 0, 60)); err != nil {
		return err
	}
	if records, err = f.ciRecords(); err != nil || len(records) != 2 {
		return Violatedf("a timed out run is a test result in %+v: %v", records, err)
	}
	return nil
}

// completeHeads makes n heads whose required check ran for four minutes, one record each.
func (f *forgeRig) completeHeads(n int, history func() error) error {
	for i := 0; i < n; i++ {
		head := fmt.Sprintf("head-%d", i)
		if err := f.ciRead(head, running(0)); err != nil {
			return err
		}
		if err := f.ciRead(head, finished("success", ledger.CheckPassed, 0, 4)); err != nil {
			return err
		}
		if err := history(); err != nil {
			return err
		}
	}
	return nil
}

func checkCIHistoryAfterFiveRecords(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	records := 0
	return f.completeHeads(5, func() error {
		records++
		history, err := f.watchedHistory()
		if err != nil {
			return err
		}
		switch {
		case records < 5 && history != nil:
			return Violatedf("%d records give history %+v", records, history)
		case records == 5 && (history == nil || history.P50 != 4*time.Minute || history.P85 != 4*time.Minute):
			return Violatedf("five records give history %+v", history)
		}
		return nil
	})
}

func checkCIHistoryMinimumIsTheRepositorys(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	settings, err := ledger.DefaultCISettings().Set("ci_history_min_records", "3")
	if err != nil {
		return err
	}
	if _, err := r.change(f.store, f.repository, control.RepositoryChange{CI: &settings}, r.Actor); err != nil {
		return err
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	records := 0
	return f.completeHeads(3, func() error {
		records++
		history, err := f.watchedHistory()
		if err != nil {
			return err
		}
		if (records < 3) != (history == nil) {
			return Violatedf("with a minimum of three, %d records give history %+v", records, history)
		}
		return nil
	})
}
