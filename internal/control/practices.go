package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Practices records the requests that the worker holding a repository's checkout run the practices check, and the
// results the workers report (FR-PRAC-1). A request is offered to the one worker it names through desired state, and
// settled by that worker's first report. No decision of the control plane reads a result.
type Practices struct {
	store       ports.Store
	hosts       AssistantHosts
	heartbeatMs int64
	local       func(context.Context) (string, error)
}

// NewPractices returns the use cases over a Store.
func NewPractices(store ports.Store) *Practices {
	return &Practices{store: store, heartbeatMs: defaultHeartbeatMs}
}

// Placing says where a check can run: hosts reads the checkouts each worker keeps, with workers beating at heartbeatMs,
// and local names the worker this daemon runs, which is asked before any other. Nil hosts leaves no worker able to run
// a check, and a nil local names no worker first.
func (p *Practices) Placing(hosts AssistantHosts, heartbeatMs int64, local func(context.Context) (string, error)) *Practices {
	p.hosts, p.local = hosts, local
	if heartbeatMs > 0 {
		p.heartbeatMs = heartbeatMs
	}
	return p
}

// PracticesAsked is what became of one repository in a request: the request that was made or found open, or why none was.
type PracticesAsked struct {
	Repository ledger.Repository
	// Request is the request offered for the repository, and nil when none could be made.
	Request *ledger.PracticesRequest
	// Existing says the repository already had this request open, so none was made.
	Existing bool
	// Reason says why no request was made.
	Reason string
}

// invalidPractices refuses a request or a report as invalid.
func invalidPractices(format string, args ...any) error {
	return fmt.Errorf("%w: %s", workorders.ErrInvalid, fmt.Sprintf(format, args...))
}

// Request asks, for each repository named, or for each active repository when none is named, the worker that holds its
// checkout to run the practices check. A repository is named by its ID or its name. The daemon's own worker is asked
// first, then the others by name, and only a connected worker that speaks ProtocolPracticesChecks is. A repository with
// no such worker gets no request, and its entry says why. One request per repository is open at a time, so a
// repository that has one answers it, and nothing is recorded for it. Each request is one event, and a call that
// records none changes nothing.
func (p *Practices) Request(ctx context.Context, scope ledger.WorkspaceID, repositories []string, cause ledger.PracticesCause, actor ledger.Actor) ([]PracticesAsked, error) {
	if !cause.Valid() {
		return nil, invalidPractices("a practices check's cause is registration, planning_session, or member, and %q is none of them", cause)
	}
	hosts, local, err := p.placement(ctx, scope)
	if err != nil {
		return nil, err
	}
	var out []PracticesAsked
	err = p.store.Write(ctx, scope, "practices.request", func(tx ports.WriteTx) error {
		out = nil
		now := tx.Now()
		all, err := tx.Repositories().Repositories()
		if err != nil {
			return err
		}
		chosen, err := chooseRepositories(all, repositories)
		if err != nil {
			return err
		}
		workers, err := tx.Workers().List()
		if err != nil {
			return err
		}
		for _, repository := range chosen {
			asked := PracticesAsked{Repository: repository}
			open, err := tx.Practices().Open(repository.ID, now)
			if err == nil {
				asked.Request, asked.Existing = &open, true
				out = append(out, asked)
				continue
			}
			if !errors.Is(err, ports.ErrNotFound) {
				return err
			}
			worker, reason := p.place(repository, workers, hosts, local, now)
			if reason != "" {
				asked.Reason = reason
				out = append(out, asked)
				continue
			}
			id, err := newID(now)
			if err != nil {
				return err
			}
			request := ledger.PracticesRequest{
				ID: id, Repository: repository.ID, Worker: worker.ID, Cause: cause, RequestedBy: actor.ID,
				RequestedAt: now, ExpiresAt: now + ledger.PracticesRequestTTLMs, State: ledger.PracticesOffered,
			}
			if err := tx.Practices().Insert(request); err != nil {
				return err
			}
			data, err := json.Marshal(map[string]string{"request": id, "repository": repository.ID, "worker": worker.ID, "cause": string(cause)})
			if err != nil {
				return err
			}
			if _, err := tx.Append(ledger.EventDraft{
				Type: ledger.EventPracticesRequested, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: id}, Data: data,
			}); err != nil {
				return err
			}
			asked.Request = &request
			out = append(out, asked)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// placement reads what the workers keep and which worker is the daemon's own.
func (p *Practices) placement(ctx context.Context, scope ledger.WorkspaceID) (map[string]AssistantHost, string, error) {
	hosts := map[string]AssistantHost{}
	if p.hosts != nil {
		read, err := p.hosts(ctx, scope)
		if err != nil {
			return nil, "", err
		}
		if read != nil {
			hosts = read
		}
	}
	var local string
	if p.local != nil {
		var err error
		if local, err = p.local(ctx); err != nil {
			local = ""
		}
	}
	return hosts, local, nil
}

// chooseRepositories picks the repositories a request names, by ID or by name, in the order named and each once, or
// every active repository by name when it names none. A repository the project removed is held by no one.
func chooseRepositories(all []ledger.Repository, named []string) ([]ledger.Repository, error) {
	active := slices.DeleteFunc(slices.Clone(all), func(r ledger.Repository) bool { return r.Removed() })
	slices.SortFunc(active, func(a, b ledger.Repository) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	if len(named) == 0 {
		return active, nil
	}
	var chosen []ledger.Repository
	for _, key := range named {
		at := slices.IndexFunc(active, func(r ledger.Repository) bool { return r.ID == key })
		if at < 0 {
			at = slices.IndexFunc(active, func(r ledger.Repository) bool { return r.Name == key })
		}
		if at < 0 {
			return nil, fmt.Errorf("repository %q: %w", key, ports.ErrNotFound)
		}
		if !slices.ContainsFunc(chosen, func(r ledger.Repository) bool { return r.ID == active[at].ID }) {
			chosen = append(chosen, active[at])
		}
	}
	return chosen, nil
}

// place picks the worker a repository's check is asked of, or says why there is none. Only a connected worker that
// keeps a checkout is a candidate, the daemon's own first and then by name, and the first that speaks
// ProtocolPracticesChecks is asked. When every candidate is older, the reply names the first and says to upgrade it.
func (p *Practices) place(repository ledger.Repository, workers []ledger.Worker, hosts map[string]AssistantHost, local string, now int64) (ledger.Worker, string) {
	var keeping []ledger.Worker
	for _, worker := range workers {
		if host, known := hosts[worker.ID]; known && host.Checkouts[repository.ID] && connected(worker, now, p.heartbeatMs) {
			keeping = append(keeping, worker)
		}
	}
	if len(keeping) == 0 {
		return ledger.Worker{}, "no connected worker keeps a checkout of " + cmp.Or(repository.Name, repository.ID)
	}
	slices.SortFunc(keeping, func(a, b ledger.Worker) int {
		if (a.ID == local) != (b.ID == local) {
			if a.ID == local {
				return -1
			}
			return 1
		}
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	for _, worker := range keeping {
		if worker.Protocol >= ledger.ProtocolPracticesChecks {
			return worker, ""
		}
	}
	return ledger.Worker{}, fmt.Sprintf("worker %s speaks protocol %d; upgrade it to run the practices check", keeping[0].Name, keeping[0].Protocol)
}

// Expire stores as expired each request that no worker reported by its expiry, with one event each, and returns their
// IDs. A report that comes later is still recorded.
func (p *Practices) Expire(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var ids []string
	err := p.store.Write(ctx, scope, "practices.expire", func(tx ports.WriteTx) error {
		ids = nil
		expired, err := tx.Practices().Expire(tx.Now())
		if err != nil {
			return err
		}
		for _, request := range expired {
			data, err := json.Marshal(map[string]string{"request": request.ID, "repository": request.Repository, "worker": request.Worker})
			if err != nil {
				return err
			}
			if _, err := tx.Append(ledger.EventDraft{
				Type: ledger.EventPracticesExpired, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request.ID}, Data: data,
			}); err != nil {
				return err
			}
			ids = append(ids, request.ID)
		}
		return nil
	})
	return ids, err
}

// desiredPractices lists the practices checks offered to a worker: its own, not reported and not expired, for a
// repository the project still holds, and only while its session speaks a protocol that reads them.
func desiredPractices(tx ports.ReadTx, worker string, protocol int, state *ledger.DesiredState) error {
	if protocol < ledger.ProtocolPracticesChecks {
		return nil
	}
	offered, err := tx.Practices().Offered(worker)
	if err != nil || len(offered) == 0 {
		return err
	}
	now, err := tx.Now()
	if err != nil {
		return err
	}
	records, err := tx.Repositories().Repositories()
	if err != nil {
		return err
	}
	for _, request := range offered {
		at := slices.IndexFunc(records, func(r ledger.Repository) bool { return r.ID == request.Repository })
		if !request.Open(now) || at < 0 || records[at].Removed() {
			continue
		}
		tip, err := tx.Practices().ScannedTip(request.Repository)
		if err != nil {
			return err
		}
		repository := records[at]
		state.PracticesChecks = append(state.PracticesChecks, ledger.DesiredPracticesCheck{
			ID: request.ID, Repository: repository.ID, BaseBranch: repository.Base(), Binding: repository.ForgeBinding,
			MergeAuthority: string(repository.Level()), ScannedFrom: tip, ExpiresAt: request.ExpiresAt,
		})
	}
	return nil
}

// ReportPracticesCheck records what a worker did with a request. The first report of a request is recorded, and a
// later one is accepted and changes nothing. A report after the request expired is recorded all the same, because the
// result is true.
//
// A checked report keeps the result as the repository's latest, unless a request made after this one already did, and
// moves the repository's scan tip to the commit the result read, or clears it when the secret scan could not run, so the
// next check reads the whole history. It appends the result as the repository keeps it. A failed report appends why and
// keeps the previous result and tip.
func (w *Workers) ReportPracticesCheck(ctx context.Context, report ledger.PracticesReport) error {
	switch report.Outcome {
	case ledger.PracticesOutcomeChecked:
		if report.Result == nil || report.Result.Commit == "" {
			return invalidPractices("a checked report holds the result, with the commit it read")
		}
	case ledger.PracticesOutcomeFailed:
		if strings.TrimSpace(report.Detail) == "" {
			return invalidPractices("a failed report says why")
		}
	default:
		return invalidPractices("a report's outcome is checked or failed, and %q is neither", report.Outcome)
	}
	if report.Request == "" {
		return invalidPractices("a report names its request")
	}
	return w.store.Write(ctx, w.scope, "practices.report", func(tx ports.WriteTx) error {
		if _, err := authenticate(tx.Workers(), report.Auth); err != nil {
			return err
		}
		request, err := tx.Practices().Request(report.Request)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && request.Worker != report.Auth.WorkerID) {
			return fmt.Errorf("practices request %s: %w", report.Request, ports.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if report.Outcome == ledger.PracticesOutcomeChecked && report.Result.Repository != request.Repository {
			return invalidPractices("request %s is for repository %s, and the result is of %s", request.ID, request.Repository, report.Result.Repository)
		}
		first, err := tx.Practices().Settle(request.ID, tx.Now())
		if err != nil || !first {
			return err
		}
		actor := ledger.Actor{Kind: ledger.ActorWorker, ID: request.Worker}
		subject := ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request.ID}
		if report.Outcome == ledger.PracticesOutcomeFailed {
			data, err := json.Marshal(map[string]string{
				"request": request.ID, "repository": request.Repository, "worker": request.Worker, "detail": practicerun.Clip(report.Detail),
			})
			if err != nil {
				return err
			}
			_, err = tx.Append(ledger.EventDraft{Type: ledger.EventPracticesFailed, Actor: actor, Subject: subject, Data: data})
			return err
		}
		result, err := keepResult(tx, request, *report.Result)
		if err != nil {
			return err
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: ledger.EventPracticesChecked, Actor: actor, Subject: subject, Data: data})
		return err
	})
}

// keepResult makes a reported result the repository's latest, and returns the result as it is kept. A result of a
// request made before the request of the one kept is not kept, and comes back as it was reported.
func keepResult(tx ports.WriteTx, request ledger.PracticesRequest, result practices.Result) (practices.Result, error) {
	previous, err := tx.Practices().Result(request.Repository)
	switch {
	case errors.Is(err, ports.ErrNotFound):
	case err != nil:
		return result, err
	default:
		kept, err := tx.Practices().Request(previous.Request)
		if err == nil && (kept.RequestedAt > request.RequestedAt || (kept.RequestedAt == request.RequestedAt && kept.ID > request.ID)) {
			return result, nil
		}
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return result, err
		}
		result = practices.CarryForward(previous.Result, result)
	}
	if err := tx.Practices().SaveResult(ledger.PracticesRecord{
		Repository: request.Repository, Request: request.ID, Commit: result.Commit, Result: result, CheckedAt: result.CheckedAt.UnixMilli(),
	}); err != nil {
		return result, err
	}
	tip := result.Commit
	if secretsUnknown(result) {
		tip = ""
	}
	return result, tx.Practices().SaveScan(request.Repository, tip)
}

// secretsUnknown says the result's secret scan could not run, so it holds no completed scan to continue from.
func secretsUnknown(result practices.Result) bool {
	return slices.ContainsFunc(result.Practices, func(p practices.PracticeStatus) bool {
		return p.ID == practices.Secrets && p.Status == practices.StatusUnknown
	})
}

// ReportPracticesCheck records what a worker did with a request, in the project the report names.
func (p *Plane) ReportPracticesCheck(ctx context.Context, report ledger.PracticesReport) error {
	w, _, err := p.route(ctx, report.Auth, report.Project, true)
	if err != nil {
		return err
	}
	return w.ReportPracticesCheck(ctx, report)
}
