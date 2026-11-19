package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/projectremote"
)

// TemplateRuntime is what a worker needs to write a shape's starting files for a member (FR-TPL-5). The entry
// package passes the renderer, because this package never imports the templates.
type TemplateRuntime struct {
	Folders   ports.ProjectFolders
	Checkouts ports.Checkouts
	// Forge is the person's forge account, which a request to create a repository and a forge account read use. Nil
	// has no `gh`: a request that creates a repository is refused as gh_missing.
	Forge  ports.ForgeAccounts
	Render projectfiles.Render
	// StateDir holds the journal of each request, in template-writes/<request>.json.
	StateDir string
}

// The states of a request's journal. A request is started before the first file is written, and reported once the
// report is sent.
const (
	templateStarted  = "started"
	templateReported = "reported"
)

// templateJournal is what a worker keeps of one request, so a crash between the write and the report writes
// nothing twice and reports once.
type templateJournal struct {
	State string `json:"state"`
	// Progress is what the run decided before its first write and what it has done since: the first commit and the
	// repository it needs, the folder it wrote by what the disk said of it, each repository's git init, commit, and
	// remote steps, so a resume is held to that folder and a crash repeats no step.
	projectremote.Progress
	Report *ledger.TemplateWriteReport `json:"report,omitempty"`
}

func (rt *TemplateRuntime) journalPath(request string) string {
	return filepath.Join(rt.StateDir, "template-writes", request+".json")
}

// read returns a request's journal, and whether it has one.
func (rt *TemplateRuntime) read(request string) (templateJournal, bool, error) {
	raw, err := os.ReadFile(rt.journalPath(request))
	if errors.Is(err, fs.ErrNotExist) {
		return templateJournal{}, false, nil
	}
	if err != nil {
		return templateJournal{}, false, err
	}
	var journal templateJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return templateJournal{}, false, fmt.Errorf("%s: %w", rt.journalPath(request), err)
	}
	// A journal written before each repository had a record of its own holds the folder and the git init at the top level.
	var earlier struct {
		Folder      string `json:"folder"`
		Initialized bool   `json:"initialized"`
	}
	if err := json.Unmarshal(raw, &earlier); err != nil {
		return templateJournal{}, false, fmt.Errorf("%s: %w", rt.journalPath(request), err)
	}
	if earlier.Folder != "" || earlier.Initialized {
		journal.KeepEarlierRecord(earlier.Folder, earlier.Initialized)
	}
	return journal, true, nil
}

// save replaces a request's journal through a rename, so a crash leaves the old one or the new one.
func (rt *TemplateRuntime) save(request string, journal templateJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	dir := filepath.Dir(rt.journalPath(request))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+request+".*")
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
	return os.Rename(temp.Name(), rt.journalPath(request))
}

// tendTemplateWrites carries out each request to write a shape's starting files that desired state offers this
// worker and its journal has not finished. Each runs on a goroutine of its own, and none runs twice at once.
func (w *Worker) tendTemplateWrites(ctx context.Context) {
	rt := w.config.Templates
	if rt == nil {
		return
	}
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading the files this worker is asked to write: %w", err))
		return
	}
	for _, request := range state.TemplateWrites {
		key := "template-write/" + request.ID
		if !w.takeEffect(key) {
			continue
		}
		w.goTracked("template write", func() {
			defer w.leaveEffect(key)
			if err := w.runTemplateWrite(ctx, rt, request); err != nil {
				w.config.Report(fmt.Errorf("template write %s: %w", request.ID, err))
			}
		})
	}
}

// errTemplateExpired says a request that was never started is past its expiry, so nobody waits for it.
var errTemplateExpired = errors.New("the request expired before this worker took it")

// runTemplateWrite does what the journal and the folder say is left of one request, and reports it. A request
// whose journal says reported is reported again, because the control plane still offers it, and one whose
// journal says started goes on from where it stopped.
func (w *Worker) runTemplateWrite(ctx context.Context, rt *TemplateRuntime, request ledger.DesiredTemplateWrite) error {
	journal, started, err := rt.read(request.ID)
	if err != nil {
		return err
	}
	if started && journal.State == templateReported && journal.Report != nil {
		return w.sendTemplateReport(ctx, rt, request.ID, journal, *journal.Report)
	}
	report, journal, err := w.performTemplateWrite(ctx, rt, request, journal, started)
	if errors.Is(err, errTemplateExpired) {
		return nil
	}
	if err != nil {
		return err
	}
	return w.sendTemplateReport(ctx, rt, request.ID, journal, report)
}

// sendTemplateReport tells the control plane what was done, and journals that it did.
func (w *Worker) sendTemplateReport(ctx context.Context, rt *TemplateRuntime, id string, journal templateJournal, report ledger.TemplateWriteReport) error {
	report.Request = id
	report.Auth = w.Auth()
	if err := w.link.ReportTemplateWrite(ctx, report); err != nil {
		return fmt.Errorf("reporting it: %w", err)
	}
	if journal.State == templateReported {
		return nil
	}
	journal.State, journal.Report = templateReported, &report
	return rt.save(id, journal)
}

// performTemplateWrite checks the request, writes the folder, makes its first commit when asked, and builds the
// report. A refusal is a report like any other. An error is what keeps the request from being decided, so the
// next pass looks again.
func (w *Worker) performTemplateWrite(ctx context.Context, rt *TemplateRuntime, request ledger.DesiredTemplateWrite, journal templateJournal, started bool) (ledger.TemplateWriteReport, templateJournal, error) {
	refused := func(reason, detail string) (ledger.TemplateWriteReport, templateJournal, error) {
		return ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: reason, Detail: detail}, journal, nil
	}
	if !started && w.clock.Now().UnixMilli() >= request.ExpiresAt {
		return ledger.TemplateWriteReport{}, journal, errTemplateExpired
	}
	job := projectremote.Job{Request: ledger.TemplateWriteRequest{
		Shape: request.Shape, Path: request.Path, Repositories: request.Repositories, Name: request.Name, Project: request.Project,
		FirstCommit: request.FirstCommit, Remote: request.Remote,
	}}
	remote := w.projectRuntime(rt)
	files, err := projectremote.Files(remote, job, &journal.Progress)
	if errors.Is(err, projectfiles.ErrInvalid) || (err == nil && job.Name() == "") {
		return refused(ledger.TemplateRefusedInvalid, "the request names no shape this worker can write")
	}
	if err != nil {
		return ledger.TemplateWriteReport{}, journal, err
	}
	switch {
	case !strings.HasPrefix(request.Path, "/"):
		return refused(ledger.TemplateRefusedNotAbsolute, "the folder is not an absolute path")
	case ledger.HasParentElement(request.Path):
		return refused(ledger.TemplateRefusedParentElement, "the folder holds a .. element")
	case request.Path != path.Clean(request.Path):
		return refused(ledger.TemplateRefusedNotClean, "the folder is not written in its clean form, with no trailing slash, //, or /./")
	}
	found, err := rt.Folders.Inspect(ctx, request.Path)
	if err != nil {
		return ledger.TemplateWriteReport{}, journal, err
	}
	switch {
	case !found.ParentDirectory:
		return refused(ledger.TemplateRefusedNoParent, "the folder's parent does not exist, and a write makes no parent")
	case found.Link:
		return refused(ledger.TemplateRefusedLink, "the folder is a symbolic link")
	case found.Exists && !found.Directory:
		return refused(ledger.TemplateRefusedNotDirectory, "something that is not a folder is there")
	}
	if reserved, err := w.insideReserved(ctx, rt, found.Real); err != nil {
		return ledger.TemplateWriteReport{}, journal, err
	} else if reserved {
		return refused(ledger.TemplateRefusedReserved, "the folder is at or inside a folder ropeshift keeps for itself")
	}
	if found.Exists && !found.Empty {
		ours, err := w.holdsOnly(ctx, rt, request.Path, files)
		if err != nil {
			return ledger.TemplateWriteReport{}, journal, err
		}
		if !started || !ours {
			return refused(ledger.TemplateRefusedNotEmpty, "the folder holds files")
		}
	}
	if detail, inside, err := w.insideKeptClone(ctx, rt, found.Real); err != nil {
		return ledger.TemplateWriteReport{}, journal, err
	} else if inside {
		return refused(ledger.TemplateRefusedInsideClone, detail)
	}
	if !started {
		needs, err := w.needsRepository(ctx, rt, found)
		if err != nil {
			return ledger.TemplateWriteReport{}, journal, err
		}
		job.NeedsRepository = needs
		// The checks of a created repository run once, here and before anything is written, so a refusal creates
		// nothing and writes nothing. What they decided stands across a restart in the journal.
		var progress projectremote.Progress
		refusal, err := projectremote.Start(ctx, remote, job, &progress)
		if err != nil {
			return ledger.TemplateWriteReport{}, journal, err
		}
		if refusal != nil {
			return *refusal, journal, nil
		}
		journal = templateJournal{State: templateStarted, Progress: progress}
		if err := rt.save(request.ID, journal); err != nil {
			return ledger.TemplateWriteReport{}, journal, err
		}
	}
	report, err := projectremote.Finish(ctx, remote, job, &journal.Progress, func() error { return rt.save(request.ID, journal) })
	return report, journal, err
}

// projectRuntime is what a run reaches this worker's machine through.
func (w *Worker) projectRuntime(rt *TemplateRuntime) projectremote.Runtime {
	return projectremote.Runtime{Folders: rt.Folders, Forge: rt.Forge, Render: rt.Render, Machine: w.config.Name}
}
