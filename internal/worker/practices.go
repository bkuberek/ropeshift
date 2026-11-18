package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PracticesRuntime is what a worker needs to run the practices check on the checkouts it keeps (FR-PRAC-1). The entry
// package passes the reads, because this package never imports an adapter.
type PracticesRuntime struct {
	// Sources are the reads a run makes of a checkout, the settings its forge keeps, and the clock.
	Sources practicerun.Sources
	// Repository finds this machine's checkout of a project's repository. An empty project names the plane's only
	// project, as a plane of one project sends it.
	Repository func(ctx context.Context, project, id string) (string, error)
	// StateDir holds the journal of each request, in practices/<request>.json.
	StateDir string
}

// The states of a request's journal. A request is journaled as ran once its run decided what to report, before the
// report is sent, and as reported once the control plane accepted the report.
const (
	practicesRan      = "ran"
	practicesReported = "reported"
)

// practicesJournal is what a worker keeps of one request, so a crash between the run and the report runs nothing
// twice and reports once: it holds the report the run decided, and a restart sends that one.
type practicesJournal struct {
	State  string                  `json:"state"`
	Report *ledger.PracticesReport `json:"report,omitempty"`
}

// checkoutPlaceholder stands for the directory of a checkout in what a worker reports, which says nothing of the
// layout of the worker's machine.
const checkoutPlaceholder = "<checkout>"

// errNoFileName refuses a request ID a journal cannot be named by.
var errNoFileName = errors.New("the request's ID is no file name")

// journalPath is where a request's journal is kept. An ID that is not a plain file name is refused, so a control plane
// cannot place a journal outside the worker's own folder.
func (rt *PracticesRuntime) journalPath(request string) (string, error) {
	if request == "" || request == "." || request == ".." || strings.ContainsAny(request, `/\`+"\x00") || filepath.Base(request) != request {
		return "", fmt.Errorf("%q: %w", request, errNoFileName)
	}
	return filepath.Join(rt.StateDir, "practices", request+".json"), nil
}

// read returns a request's journal, and whether it has one.
func (rt *PracticesRuntime) read(path string) (practicesJournal, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return practicesJournal{}, false, nil
	}
	if err != nil {
		return practicesJournal{}, false, err
	}
	var journal practicesJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return practicesJournal{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return journal, true, nil
}

// save replaces a request's journal through a rename, so a crash leaves the old one or the new one.
func (rt *PracticesRuntime) save(path string, journal practicesJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".practices.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// held lists the request IDs this worker keeps a journal of.
func (rt *PracticesRuntime) held() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(rt.StateDir, "practices"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok && !strings.HasPrefix(id, ".") && !entry.IsDir() {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// tendPracticesChecks does what this worker's journals and the practices checks desired state offers it leave to do,
// one at a time: it sends each result it journaled and could not send, whatever desired state lists, because a request
// the control plane no longer offers still takes a late report; it runs each offered check that has no journal; and it
// deletes the journal of each request that was reported and that desired state no longer lists. The run reads a whole
// history, so it holds no lane and no other check: a second pass finds the first still running and leaves it.
func (w *Worker) tendPracticesChecks(ctx context.Context) {
	rt := w.config.Practices
	if rt == nil {
		return
	}
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading the practices checks this worker is asked to run: %w", err))
		return
	}
	held, err := rt.held()
	if err != nil {
		w.config.Report(fmt.Errorf("reading the journals of the practices checks: %w", err))
		return
	}
	if len(state.PracticesChecks) == 0 && len(held) == 0 {
		return
	}
	const key = "practices-checks"
	if !w.takeEffect(key) {
		return
	}
	checks := slices.Clone(state.PracticesChecks)
	w.goTracked("practices check", func() {
		defer w.leaveEffect(key)
		listed := map[string]bool{}
		for _, check := range checks {
			listed[check.ID] = true
		}
		tried := map[string]bool{}
		for _, id := range held {
			tried[id] = true
			if err := w.sendJournaledPractices(ctx, rt, id); err != nil && ctx.Err() == nil {
				w.config.Report(fmt.Errorf("practices check %s: %w", id, err))
			}
		}
		for _, check := range checks {
			if ctx.Err() != nil {
				return
			}
			if tried[check.ID] {
				continue
			}
			if err := w.runPracticesCheck(ctx, rt, check); err != nil && ctx.Err() == nil {
				w.config.Report(fmt.Errorf("practices check %s: %w", check.ID, err))
			}
		}
		rt.prune(ctx, held, listed)
	})
}

// prune deletes the journal of each of these requests that was reported and that desired state does not list. One
// that desired state still lists stays, so a state read before the report does not make the worker run it again.
func (rt *PracticesRuntime) prune(ctx context.Context, held []string, listed map[string]bool) {
	for _, id := range held {
		path, err := rt.journalPath(id)
		if err != nil || listed[id] || ctx.Err() != nil {
			continue
		}
		if journal, found, err := rt.read(path); err == nil && found && journal.State == practicesReported {
			_ = os.Remove(path)
		}
	}
}

// sendJournaledPractices sends the result a request's journal holds when its report was not sent.
func (w *Worker) sendJournaledPractices(ctx context.Context, rt *PracticesRuntime, id string) error {
	path, err := rt.journalPath(id)
	if err != nil {
		return err
	}
	journal, found, err := rt.read(path)
	if err != nil || !found || journal.State != practicesRan || journal.Report == nil {
		return err
	}
	return w.sendPracticesReport(ctx, rt, path, id, journal)
}

// runPracticesCheck does what the journal says is left of one request: it runs the check when no run was journaled,
// and sends the report the journal holds. A request whose journal says reported is done. A request that was never run
// and is past its expiry is left, because nobody waits for it, and one whose run was journaled is reported however late,
// because its result is true.
func (w *Worker) runPracticesCheck(ctx context.Context, rt *PracticesRuntime, check ledger.DesiredPracticesCheck) error {
	path, err := rt.journalPath(check.ID)
	if err != nil {
		return err
	}
	journal, found, err := rt.read(path)
	if err != nil {
		return err
	}
	if found && journal.State == practicesReported {
		return nil
	}
	if !found || journal.Report == nil {
		if w.clock.Now().UnixMilli() >= check.ExpiresAt {
			return nil
		}
		report, err := w.performPracticesCheck(ctx, rt, check)
		if err != nil {
			return err
		}
		journal = practicesJournal{State: practicesRan, Report: &report}
		if err := rt.save(path, journal); err != nil {
			return err
		}
	}
	return w.sendPracticesReport(ctx, rt, path, check.ID, journal)
}

// sendPracticesReport tells the control plane what the journal says the run decided, and journals that it did. A report
// the control plane refuses for good, because it names no request it knows or no repository the result is of, is
// journaled as sent and said once, so it is not sent again every pass.
func (w *Worker) sendPracticesReport(ctx context.Context, rt *PracticesRuntime, path, id string, journal practicesJournal) error {
	report := *journal.Report
	report.Request, report.Auth = id, w.Auth()
	err := w.link.ReportPracticesCheck(ctx, report)
	refused := errors.Is(err, workorders.ErrInvalid) || errors.Is(err, ports.ErrNotFound)
	if err != nil && !refused {
		return fmt.Errorf("reporting it: %w", err)
	}
	journal.State = practicesReported
	if saved := rt.save(path, journal); saved != nil {
		return errors.Join(err, saved)
	}
	if refused {
		return fmt.Errorf("the control plane refused the report: %w", err)
	}
	return nil
}

// performPracticesCheck runs the check on this worker's checkout and builds the report: the result, or why none could
// be made. A run that ends with the worker's context returns that error, so nothing is decided for a check the worker
// did not finish. A settings read the forge refused for a rate limit still has a result, and holds the repository's
// reads as the worker does for any other refusal.
func (w *Worker) performPracticesCheck(ctx context.Context, rt *PracticesRuntime, check ledger.DesiredPracticesCheck) (ledger.PracticesReport, error) {
	failed := func(detail string) (ledger.PracticesReport, error) {
		return ledger.PracticesReport{Project: check.Project, Request: check.ID, Outcome: ledger.PracticesOutcomeFailed, Detail: detail}, nil
	}
	dir, err := rt.Repository(ctx, check.Project, check.Repository)
	if errors.Is(err, ports.ErrNotFound) {
		return failed(fmt.Sprintf("this worker keeps no checkout of repository %s", check.Repository))
	}
	if err != nil {
		return ledger.PracticesReport{}, err
	}
	request := practicerun.Request{
		Repository: check.Repository, Worker: w.config.Name, Dir: dir, BaseBranch: check.BaseBranch,
		MergeAuthority: ledger.MergeAuthority(check.MergeAuthority), ScannedFrom: check.ScannedFrom,
	}
	if check.Binding != "" {
		binding, err := ledger.ParseForgeBinding(check.Binding)
		if err != nil {
			return failed(withoutCheckout(err.Error(), dir))
		}
		request.Binding = binding
	}
	result, err := practicerun.Run(ctx, rt.Sources, request)
	if ctx.Err() != nil {
		return ledger.PracticesReport{}, ctx.Err()
	}
	switch {
	case errors.Is(err, ports.ErrForgeRateLimited):
		w.rateLimited(check.Project, check.Repository, err)
	case err != nil:
		return failed(withoutCheckout(err.Error(), dir))
	}
	for i, status := range result.Practices {
		result.Practices[i].Reason = withoutCheckout(status.Reason, dir)
	}
	return ledger.PracticesReport{Project: check.Project, Request: check.ID, Outcome: ledger.PracticesOutcomeChecked, Result: &result}, nil
}

// withoutCheckout writes text for a report: on one line and bounded, with the directory of the checkout, as it was
// given and as it reads cleaned, replaced by a placeholder. What a run reports names nothing of the layout of the
// worker's machine.
func withoutCheckout(text, dir string) string {
	if text == "" {
		return text
	}
	for _, spelled := range []string{dir, filepath.Clean(dir)} {
		if len(spelled) > 1 && spelled != string(filepath.Separator) {
			text = strings.ReplaceAll(text, spelled, checkoutPlaceholder)
		}
	}
	return practicerun.Clip(text)
}
