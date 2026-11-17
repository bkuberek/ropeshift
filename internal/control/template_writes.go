package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The events of a template write, on the installation's log.
const (
	subjectTemplateWrite = "template_write"

	eventTemplateWriteRequested = "template_write.requested"
	eventTemplateWriteReported  = "template_write.reported"
)

// TemplateWrites is a member's request that a worker write a shape's starting files into a folder on its
// machine, and what the worker reported (FR-TPL-5). The request is recorded in the installation, offered in the
// desired state of the worker it names for two minutes, and settled by the worker's first report.
type TemplateWrites struct {
	installation ports.Installation
	heartbeatMs  int64
	local        func(context.Context) (string, error)
}

// NewTemplateWrites returns the use case. heartbeatMs is how often a worker beats, which tells a connected worker
// from one that stopped, and local names the worker this daemon runs, whose folders the register route reads.
// A nil local leaves no folder registrable from the web UI.
func NewTemplateWrites(installation ports.Installation, heartbeatMs int64, local func(context.Context) (string, error)) *TemplateWrites {
	if heartbeatMs == 0 {
		heartbeatMs = defaultHeartbeatMs
	}
	return &TemplateWrites{installation: installation, heartbeatMs: heartbeatMs, local: local}
}

// invalidTemplateWrite refuses a request as invalid.
func invalidTemplateWrite(format string, args ...any) error {
	return fmt.Errorf("%w: %s", workorders.ErrInvalid, fmt.Sprintf(format, args...))
}

// Check refuses a request no worker could be asked for, before anything is recorded: an unknown shape, a path
// that is not absolute or holds a `..` element, checked as it was sent, code repositories on a shape that takes
// none, a name or remote the ledger refuses, a project on a shape other than a repository, and a folder whose
// name makes no project name.
func (t *TemplateWrites) Check(in ledger.TemplateWriteRequest) error {
	if !projectfiles.Shape(in.Shape).Valid() {
		return invalidTemplateWrite("no shape %q; the shapes are workspace, monorepo, and repository", in.Shape)
	}
	if !strings.HasPrefix(in.Path, "/") {
		return invalidTemplateWrite("the folder %q is not an absolute path", in.Path)
	}
	if ledger.HasParentElement(in.Path) {
		return invalidTemplateWrite("the folder %q holds a .. element", in.Path)
	}
	repositories := make([]projectfiles.CodeRepository, 0, len(in.Repositories))
	for _, repository := range in.Repositories {
		repositories = append(repositories, projectfiles.CodeRepository{Name: repository.Name, Remote: repository.Remote})
	}
	if err := projectfiles.Check(projectfiles.Shape(in.Shape), repositories); err != nil {
		return fmt.Errorf("%w: %w", workorders.ErrInvalid, err)
	}
	if in.Project != "" {
		if in.Shape != string(projectfiles.Repository) {
			return invalidTemplateWrite("a %s names no project; only a repository does", in.Shape)
		}
		if err := ledger.ValidateProjectName(in.Project); err != nil {
			return fmt.Errorf("%w: project: %w", workorders.ErrInvalid, err)
		}
	}
	if projectfiles.NameOf(baseOf(in.Path)) == "" {
		return invalidTemplateWrite("the folder %q makes no project name; use a folder named with letters or digits", baseOf(in.Path))
	}
	return nil
}

// baseOf is the last element of a slash separated path.
func baseOf(path string) string {
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	return parts[len(parts)-1]
}

// Request records a member's request and offers it to the worker it names. It refuses a request that is
// invalid, a worker that is not connected, and a worker whose session speaks a protocol older than
// ProtocolTemplateWrites, which would never report it.
func (t *TemplateWrites) Request(ctx context.Context, worker string, in ledger.TemplateWriteRequest, actor ledger.Actor) (ledger.TemplateWriteView, error) {
	if !authority.Allowed(actor, authority.ActionWriteTemplates) {
		return ledger.TemplateWriteView{}, fmt.Errorf("%w: %w", workorders.ErrForbidden, authority.ErrMemberOnly)
	}
	var view ledger.TemplateWriteView
	err := t.installation.Write(ctx, "template_write.request", func(tx ports.SharedTx) error {
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		if err := t.Check(in); err != nil {
			return err
		}
		now := tx.Now()
		if !connected(record.Worker, now, t.heartbeatMs) {
			return invalidTemplateWrite("worker %s is not connected: it has not reached this control plane in the last few heartbeats", record.Name)
		}
		if record.Protocol < ledger.ProtocolTemplateWrites {
			return invalidTemplateWrite("worker %s speaks protocol %d; upgrade it to write files from the web UI", record.Name, record.Protocol)
		}
		id, err := newID(now)
		if err != nil {
			return err
		}
		request := in
		request.Repositories = slices.Clone(in.Repositories)
		stored := ledger.TemplateWriteRecord{
			ID: id, Worker: worker, RequestedBy: actor.ID, Request: request, RequestedAt: now, ExpiresAt: now + ledger.TemplateWriteTTLMs,
			State: ledger.TemplateWriteOffered,
		}
		if err := tx.TemplateWrites().Insert(stored); err != nil {
			return err
		}
		data, err := json.Marshal(map[string]string{"member": actor.ID, "worker": worker, "shape": in.Shape, "folder": in.Path})
		if err != nil {
			return err
		}
		if _, err := tx.Append(ledger.EventDraft{
			Type: eventTemplateWriteRequested, Actor: actor, Subject: ledger.Subject{Kind: subjectTemplateWrite, ID: id}, Data: data,
		}); err != nil {
			return err
		}
		view = stored.ViewAt(now, false)
		return nil
	})
	return view, err
}

// View reads one request of a worker as a member reads it.
func (t *TemplateWrites) View(ctx context.Context, worker, request string) (ledger.TemplateWriteView, error) {
	var record ledger.TemplateWriteRecord
	var now int64
	err := t.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		if now, err = tx.Now(); err != nil {
			return err
		}
		record, err = tx.TemplateWrites().Find(request)
		return err
	})
	if err != nil {
		return ledger.TemplateWriteView{}, err
	}
	if record.Worker != worker {
		return ledger.TemplateWriteView{}, fmt.Errorf("template write %s: %w", request, ports.ErrNotFound)
	}
	return record.ViewAt(now, t.registrable(ctx, worker)), nil
}

// registrable says the register route can read a folder this worker wrote: it is the worker this daemon runs.
func (t *TemplateWrites) registrable(ctx context.Context, worker string) bool {
	if t.local == nil {
		return false
	}
	local, err := t.local(ctx)
	return err == nil && local == worker
}

// desiredTemplateWrites lists the requests offered to a worker: its own, not yet reported or expired, and only
// while its session speaks a protocol that reads them.
func desiredTemplateWrites(tx ports.SharedReadTx, worker ledger.WorkerRecord) ([]ledger.DesiredTemplateWrite, error) {
	if worker.Protocol < ledger.ProtocolTemplateWrites {
		return nil, nil
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	offered, err := tx.TemplateWrites().Offered(worker.ID)
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredTemplateWrite
	for _, record := range offered {
		if record.StateAt(now) == ledger.TemplateWriteOffered {
			out = append(out, record.Desired())
		}
	}
	return out, nil
}

// reportTemplateWrite records a worker's first report of a request. A later one is accepted and changes
// nothing, and a report that comes after the request expired is recorded all the same, because the files exist.
func reportTemplateWrite(ctx context.Context, installation ports.Installation, report ledger.TemplateWriteReport) error {
	var state ledger.TemplateWriteState
	switch report.Outcome {
	case ledger.TemplateOutcomeWritten:
		state = ledger.TemplateWriteWritten
	case ledger.TemplateOutcomeRefused:
		state = ledger.TemplateWriteRefused
	default:
		return invalidTemplateWrite("a report's outcome is written or refused, and %q is neither", report.Outcome)
	}
	if report.Request == "" {
		return invalidTemplateWrite("a report names its request")
	}
	return installation.Write(ctx, "template_write.report", func(tx ports.SharedTx) error {
		if _, err := authenticate(tx.Workers(), report.Auth); err != nil {
			return err
		}
		record, err := tx.TemplateWrites().Find(report.Request)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && record.Worker != report.Auth.WorkerID) {
			return fmt.Errorf("template write %s: %w", report.Request, ports.ErrNotFound)
		}
		if err != nil {
			return err
		}
		first, err := tx.TemplateWrites().Settle(record.ID, state, report, tx.Now())
		if err != nil || !first {
			return err
		}
		data, err := json.Marshal(map[string]string{
			"member": record.RequestedBy, "worker": record.Worker, "shape": record.Request.Shape, "folder": record.Request.Path, "outcome": report.Outcome,
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: eventTemplateWriteReported, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: record.Worker},
			Subject: ledger.Subject{Kind: subjectTemplateWrite, ID: record.ID}, Data: data,
		})
		return err
	})
}

// ReportTemplateWrite records what a worker did with a request, on the plane of one project.
func (w *Workers) ReportTemplateWrite(ctx context.Context, report ledger.TemplateWriteReport) error {
	return reportTemplateWrite(ctx, w.store.Installation(w.scope), report)
}

// ReportTemplateWrite records what a worker did with a request.
func (p *Plane) ReportTemplateWrite(ctx context.Context, report ledger.TemplateWriteReport) error {
	if _, err := p.worker(ctx, report.Auth, nil); err != nil {
		return err
	}
	return reportTemplateWrite(ctx, p.installation, report)
}
