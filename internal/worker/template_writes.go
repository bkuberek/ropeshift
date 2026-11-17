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
)

// TemplateRuntime is what a worker needs to write a shape's starting files for a member (FR-TPL-5). The entry
// package passes the renderer, because this package never imports the templates.
type TemplateRuntime struct {
	Folders   ports.ProjectFolders
	Checkouts ports.Checkouts
	Render    projectfiles.Render
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
	// Commit says the folder's first commit is to be made, and NeedsRepository that the folder needs a repository
	// of its own, which were decided before the first write and stand across a restart.
	Commit          bool `json:"commit"`
	NeedsRepository bool `json:"needs_repository"`
	// Initialized says the worker was about to run git init in the folder, so a .git it finds on a resume is its own.
	// A .git with no such record is somebody else's repository, which the worker commits nothing in.
	Initialized bool `json:"initialized,omitempty"`
	// Folder names the folder the worker wrote, by what the disk said of it, so a resume and the first commit are held to that
	// folder and not to another that a link swapped in at the path.
	Folder string                      `json:"folder,omitempty"`
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
	shape := projectfiles.Shape(request.Shape)
	name := projectfiles.NameOf(path.Base(request.Path))
	data := projectfiles.Data{Project: name}
	for _, repository := range request.Repositories {
		data.Repositories = append(data.Repositories, projectfiles.CodeRepository{Name: repository.Name, Remote: repository.Remote})
	}
	files, err := projectfiles.Files(shape, data, rt.Render)
	if errors.Is(err, projectfiles.ErrInvalid) || (err == nil && name == "") {
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
		journal = templateJournal{State: templateStarted, Commit: request.FirstCommit && needs, NeedsRepository: needs}
		if err := rt.save(request.ID, journal); err != nil {
			return ledger.TemplateWriteReport{}, journal, err
		}
	}
	return w.writeTemplate(ctx, rt, request, journal, data, files)
}
