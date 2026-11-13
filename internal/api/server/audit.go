package server

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// auditChunk is how many events one read of an audit takes, walking back from the newest.
const auditChunk = 200

// auditRequest is an audit's filters and page, as a request states them.
type auditRequest struct {
	actor, actorID, kind, subject string
	before                        int64
	limit                         int
}

func auditOf(r *http.Request) (auditRequest, error) {
	query := r.URL.Query()
	request := auditRequest{
		actor: query.Get("actor"), actorID: query.Get("actor_id"), kind: query.Get("kind"), subject: query.Get("subject"), limit: api.AuditDefaultLimit,
	}
	if text := query.Get("before"); text != "" {
		before, err := strconv.ParseInt(text, 10, 64)
		if err != nil || before < 1 {
			return auditRequest{}, fmt.Errorf("before is a revision from 1, and not %q", text)
		}
		request.before = before
	}
	if text := query.Get("limit"); text != "" {
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 1 || limit > api.AuditMaxLimit {
			return auditRequest{}, fmt.Errorf("limit is a number from 1 to %d, and not %q", api.AuditMaxLimit, text)
		}
		request.limit = limit
	}
	return request, nil
}

func (q auditRequest) matches(event ledger.Event) bool {
	return (q.actor == "" || string(event.Actor.Kind) == q.actor) && (q.actorID == "" || event.Actor.ID == q.actorID) &&
		(q.kind == "" || strings.HasPrefix(event.Type, q.kind)) &&
		(q.subject == "" || event.Subject.Kind == q.subject)
}

// eventsAfter reads a log's events after a revision, in order, as both of the ledger's logs do.
type eventsAfter func(after int64, limit int) ([]ledger.Event, error)

// headOf is the newest revision of a log, starting from a revision the store believes is the newest and
// reading on past it, so a store that counts its revision another way still finds its last event.
func headOf(read eventsAfter, believed int64) (int64, error) {
	head := max(believed, 0)
	for {
		page, err := read(head, 1000)
		if err != nil || len(page) == 0 {
			return head, err
		}
		head = page[len(page)-1].Rev
	}
}

// readAudit walks a log back from the newest event, or from the revision a page starts below, and keeps
// what the filters match until the page is full. It reads at most api.AuditScanLimit events, so a filter
// that matches little answers with what it found and where to go on.
func readAudit(read eventsAfter, head int64, q auditRequest) (api.AuditPage, error) {
	page := api.AuditPage{Head: head, Events: []api.AuditEvent{}}
	upper := head + 1
	if q.before > 0 && q.before < upper {
		upper = q.before
	}
	looked := 0
	for upper > 1 {
		if looked >= api.AuditScanLimit {
			page.Scanned, page.Next = true, upper
			return page, nil
		}
		lower := max(0, upper-1-auditChunk)
		events, err := read(lower, auditChunk)
		if err != nil {
			return api.AuditPage{}, err
		}
		for _, event := range slices.Backward(events) {
			if event.Rev >= upper {
				continue
			}
			looked++
			if !q.matches(event) {
				continue
			}
			page.Events = append(page.Events, api.AuditEvent{StreamEvent: api.StreamEventOf(event), ID: event.ID, Data: event.Data})
			if len(page.Events) == q.limit {
				page.Next = event.Rev
				if event.Rev <= 1 {
					page.Next = 0
				}
				return page, nil
			}
		}
		upper = lower + 1
	}
	return page, nil
}

// projectAudit reads this handler's project log.
func (h *handler) projectAudit(w http.ResponseWriter, r *http.Request) {
	q, err := auditOf(r)
	if err != nil {
		invalid(w, "%v", err)
		return
	}
	var page api.AuditPage
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		head, err := headOf(tx.Events, workspace.Rev)
		if err != nil {
			return err
		}
		if page, err = readAudit(tx.Events, head, q); err != nil {
			return err
		}
		if err := labelAudit(tx.Members(), page.Events); err != nil {
			return err
		}
		page.AsOf, err = tx.Now()
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	page.Scope = "project"
	reply(w, http.StatusOK, page)
}

// installationAudit reads the installation's own log: its projects, members, workers, and roster.
func (h *handler) installationAudit(w http.ResponseWriter, r *http.Request) {
	q, err := auditOf(r)
	if err != nil {
		invalid(w, "%v", err)
		return
	}
	var page api.AuditPage
	err = h.Installation.Read(r.Context(), func(tx ports.SharedReadTx) error {
		believed, err := tx.Revision()
		if err != nil {
			return err
		}
		head, err := headOf(tx.Events, believed)
		if err != nil {
			return err
		}
		if page, err = readAudit(tx.Events, head, q); err != nil {
			return err
		}
		if err := labelAudit(tx.Members(), page.Events); err != nil {
			return err
		}
		page.AsOf, err = tx.Now()
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	page.Scope = "installation"
	reply(w, http.StatusOK, page)
}

// labelAudit names each delegate a page of the audit holds by its credential's label.
func labelAudit(members ports.MemberReader, events []api.AuditEvent) error {
	stream := make([]api.StreamEvent, len(events))
	for i := range events {
		stream[i] = events[i].StreamEvent
	}
	if err := labelsOf(members, stream); err != nil {
		return err
	}
	for i := range events {
		events[i].StreamEvent = stream[i]
	}
	return nil
}
