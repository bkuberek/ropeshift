package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// unnamed is the operation of a changing route the class table does not name: consent, which no policy
// exempts, so a delegate's call to it is a card until the route is named.
var unnamed = authority.Operation{ID: "unnamed", Label: "Run an unnamed operation", Class: authority.ClassConsent, NeverReason: "This route names no operation."}

// resolver reads the operations a delegate's call to a route runs, from the request and the record it
// names, and whether it decides in a one-way category.
type resolver func(h *handler, r *http.Request, body []byte) (ids []string, oneWay bool, err error)

// changer reads what a card changes, for a route whose record says more than its arguments do.
type changer func(h *handler, r *http.Request, body []byte) (any, error)

// routeOperation is what a changing route runs, as the class table names it (ADR-0024). ids are every
// operation the route can run, and resolve picks among them for one call. kind is the kind of the record
// a card for it binds to, and subject names that record when the path's {id} does not. canonical reads
// the record a path's {id} names, for a route whose handler takes a name or a prefix as well as an
// identity, so a delegate's call, and the card it becomes, address that one record by its identity.
type routeOperation struct {
	ids       []string
	resolve   resolver
	kind      string
	subject   func(h *handler, r *http.Request, body []byte) string
	changes   changer
	canonical canonicalizer
	check     func(body []byte) error
}

// checked refuses a call whose body cannot make what it asks before anything reads it as a card or an
// exemption, which would name its record by what the body holds.
func (o routeOperation) checked(check func(body []byte) error) routeOperation {
	o.check = check
	return o
}

// plainID refuses a body whose id is not one a caller may name for a record it makes. A card names its record
// by that ID, in its subject and its title, which a member reads in Needs you.
func plainID(errInvalid error) func(body []byte) error {
	return func(body []byte) error {
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &in); err != nil || !ledger.PlainID(in.ID) {
			return fmt.Errorf("%w: the id is 1 to %d letters, digits, '-', and '_'", errInvalid, ledger.MaxRecordID)
		}
		return nil
	}
}

// canonicalizer reads the identity of the record a name, a prefix, or an identity names.
type canonicalizer func(h *handler, ctx context.Context, named string) (string, error)

func (o routeOperation) resolving(canonical canonicalizer) routeOperation {
	o.canonical = canonical
	return o
}

// repositoryID reads the identity of the repository a name, a prefix, or an identity names, as the
// repository routes' handlers read it.
func repositoryID(h *handler, ctx context.Context, named string) (string, error) {
	record, err := h.repositories.Resolve(ctx, h.Scope, named)
	return record.ID, err
}

func fixed(id string) routeOperation { return routeOperation{ids: []string{id}} }

func (o routeOperation) about(kind string) routeOperation {
	o.kind = kind
	return o
}

func (o routeOperation) named(subject func(h *handler, r *http.Request, body []byte) string) routeOperation {
	o.subject = subject
	return o
}

func (o routeOperation) showing(changes changer) routeOperation {
	o.changes = changes
	return o
}

func resolved(resolve resolver, ids ...string) routeOperation {
	return routeOperation{ids: ids, resolve: resolve}
}

// Subject kinds a card binds to: the ones the ledger's events name.
const (
	subjectWorkOrder = workorders.SubjectKind
	subjectPlan      = planning.SubjectPlan
	subjectQuestion  = "question"
	subjectDecision  = "decision"
	subjectCategory  = "category"
	subjectPolicy    = "policy"
	subjectWorkspace = "workspace"
	subjectLane      = "lane"
)

// routeOperations names the operation of every changing route, by the pattern its mux matched. A test
// reads the routes from the source and fails while one is missing, and another fails while an operation
// of the table is reached by none.
var routeOperations = map[string]routeOperation{
	"POST /v1/projects":                                fixed(authority.OpProjectCreate),
	"POST /v1/projects/import":                         fixed(authority.OpProjectImport),
	"POST " + api.ImportUploadPath:                     fixed(authority.OpProjectImport),
	"POST /v1/projects/{project}/rename":               fixed(authority.OpProjectRename),
	"POST /v1/projects/{project}/archive":              fixed(authority.OpProjectArchive),
	"POST /v1/projects/{project}/restore":              fixed(authority.OpProjectRestore),
	"POST /v1/projects/{project}/remove":               fixed(authority.OpProjectRemove),
	"POST /v1/installation/backup":                     fixed(authority.OpInstallationBackup),
	"POST " + api.InstallationBackupDownloadPath:       fixed(authority.OpInstallationBackup),
	"POST /v1/workers/tokens":                          fixed(authority.OpWorkerJoinToken),
	"PUT /v1/workers/{id}/autonomy":                    fixed(authority.OpAutonomySet),
	"POST /v1/workers/{id}/autonomy/acknowledgement":   fixed(authority.OpAutonomyAcknowledge),
	"POST /v1/workers/{id}/windows":                    fixed(authority.OpAutonomyWindow),
	"DELETE /v1/workers/{id}/windows":                  fixed(authority.OpAutonomyWindow),
	"PUT /v1/workers/{id}/caps":                        fixed(authority.OpWorkerLaneCaps),
	"PUT /v1/workers/{id}/orchestrator":                fixed(authority.OpOrchestratorRun),
	"POST /v1/roster/changes":                          fixed(authority.OpRosterChange).about("roster").named(rosterScope).showing(rosterChanges),
	"POST /v1/roster/revert":                           fixed(authority.OpRosterChange).about("roster").named(rosterScope).showing(rosterChanges),
	"POST /v1/sessions/end":                            fixed(authority.OpSessionEnd),
	"POST /v1/sessions/end-all":                        fixed(authority.OpSessionEndAll),
	"PUT /v1/passphrase":                               fixed(authority.OpPassphraseSet),
	"GET /v1/delegates":                                fixed(authority.OpDelegateList),
	"POST /v1/delegates":                               fixed(authority.OpDelegateCreate),
	"POST /v1/delegates/{id}/revoke":                   fixed(authority.OpDelegateRevoke),
	"POST /v1/delegates/{id}/remove":                   fixed(authority.OpDelegateRemove),
	"POST /v1/backup":                                  fixed(authority.OpProjectBackup),
	"POST " + api.BackupDownloadPath:                   fixed(authority.OpProjectBackup),
	"POST /v1/work-orders":                             resolved(byKind(authority.OpWorkOrderCreate, authority.OpWorkOrderCreateByAssistant), authority.OpWorkOrderCreate, authority.OpWorkOrderCreateByAssistant).about(subjectWorkOrder).named(bodyField("id")).checked(plainID(workorders.ErrInvalid)),
	"POST /v1/work-orders/{id}/review":                 resolved(reviewOperation, authority.OpWorkOrderReviewPlan, authority.OpWorkOrderReview).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/claims":                 fixed(authority.OpWorkOrderClaim),
	"POST /v1/work-orders/{id}/lanes":                  fixed(authority.OpLaneStart),
	"POST /v1/work-orders/{id}/lane-resume":            fixed(authority.OpLaneResume),
	"POST /v1/work-orders/{id}/lane-reset":             fixed(authority.OpLaneReset),
	"POST /v1/work-orders/{id}/lane-respawn":           fixed(authority.OpLaneRestart),
	"POST /v1/work-orders/{id}/lane-archive":           fixed(authority.OpLaneArchive),
	"POST /v1/work-orders/{id}/replan":                 fixed(authority.OpWorkOrderReplan),
	"POST /v1/work-orders/{id}/retry":                  resolved(retryOperation, authority.OpWorkOrderRetry, authority.OpWorkOrderRetryExtra).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/accept":                 fixed(authority.OpWorkOrderAccept).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/rework":                 fixed(authority.OpWorkOrderRework).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/cancel":                 fixed(authority.OpWorkOrderCancel).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/reassign":               fixed(authority.OpWorkOrderReassign).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/send-back":              fixed(authority.OpWorkOrderSendBack).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/resolve":                fixed(authority.OpWorkOrderResolve).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/writes":                 fixed(authority.OpWorkOrderWrite).about(subjectWorkOrder),
	"POST /v1/work-orders/{id}/claims/{claim}/release": fixed(authority.OpWorkOrderRelease).about(subjectWorkOrder),
	"PUT /v1/fleet/mode":                               resolved(fleetModeOperation, authority.OpFleetPause, authority.OpFleetRun).about(subjectWorkspace).named(theWorkspace),
	"PUT /v1/fleet/caps":                               fixed(authority.OpFleetCaps).about(subjectWorkspace).named(theWorkspace).showing(capsChanges),
	"POST /v1/plans/{id}/reviews":                      resolved(byKind(authority.OpPlanStartReview, authority.OpPlanStartReviewByAssistant), authority.OpPlanStartReview, authority.OpPlanStartReviewByAssistant).about(subjectPlan),
	"POST /v1/questions":                               fixed(authority.OpQuestionRecord).about(subjectQuestion).named(bodyField("id")).checked(plainID(questions.ErrInvalid)),
	"POST /v1/questions/{id}/dismiss":                  fixed(authority.OpQuestionDismiss).about(subjectQuestion),
	"POST /v1/questions/{id}/answer":                   resolved(answerOperation, authority.OpQuestionAnswer).about(subjectQuestion),
	"POST /v1/decisions":                               resolved(decisionOperation, authority.OpDecisionAssume, authority.OpDecisionRecord).about(subjectDecision).named(bodyField("id")).checked(plainID(questions.ErrInvalid)),
	"POST /v1/decisions/{id}/overturn":                 fixed(authority.OpDecisionOverturn).about(subjectDecision),
	"POST /v1/policies":                                resolved(policyOperation, authority.OpDelegatePolicyPublish, authority.OpPolicyPublish).about(subjectPolicy).named(policyKindOf).showing(policyChanges),
	"POST /v1/categories":                              fixed(authority.OpCategoryAdd).about(subjectCategory).named(bodyField("name")),
	"POST /v1/repositories":                            fixed(authority.OpRepositoryRegister),
	"PATCH /v1/repositories/{id}":                      resolved(repositoryOperation, repositoryOperations...).about(ledger.SubjectRepository).showing(repositoryChanges).resolving(repositoryID),
	"POST /v1/repositories/{id}/remove":                fixed(authority.OpRepositoryRemove).about(ledger.SubjectRepository).resolving(repositoryID),
	"POST /v1/approvals/{id}/decide":                   resolved(approvalOperation, authority.OpApprovalDecideDelegated, authority.OpApprovalDecide, authority.OpCardConfirm).about(authority.SubjectApproval),
	"POST /v1/effects/{id}/decide":                     fixed(authority.OpEffectDecide).about(ledger.SubjectEffect),
	"POST /v1/messages":                                resolved(messageOperation, authority.OpMessageSend, authority.OpMessageOrchestrator),
	"POST " + api.PaneInputPath:                        fixed(authority.OpPaneInput).about(subjectLane),
	"POST /v1/conversations":                           fixed(authority.OpConversationCreate),
	"POST /v1/conversations/{id}/open":                 fixed(authority.OpConversationOpen),
	"POST /v1/conversations/{id}/stop":                 fixed(authority.OpConversationStop),
	"POST /v1/conversations/{id}/rename":               fixed(authority.OpConversationRename),
	"POST /v1/conversations/{id}/delete":               fixed(authority.OpConversationDelete),
	"POST /v1/conversations/{id}/compact":              fixed(authority.OpConversationCompact),
	"POST /v1/conversations/{id}/clear":                fixed(authority.OpConversationClear),
	"POST /v1/conversations/{id}/messages":             fixed(authority.OpConversationMessage),
}

var repositoryOperations = []string{
	authority.OpRepositorySet, authority.OpRepositoryLowerMergeAuthority, authority.OpRepositoryReviewTargetBranch,
	authority.OpRepositoryRaiseMergeAuthority, authority.OpRepositoryReviewTargetPullRequest, authority.OpRepositoryForge,
}

// operationsOf reads operations by ID. An ID the table lacks reads as the unnamed consent operation.
func operationsOf(ids []string) []authority.Operation {
	if len(ids) == 0 {
		return []authority.Operation{unnamed}
	}
	out := make([]authority.Operation, 0, len(ids))
	for _, id := range ids {
		op, ok := authority.OperationOf(id)
		if !ok {
			op = unnamed
		}
		out = append(out, op)
	}
	return out
}

// memberOnlyRoute says a route's every operation is a member's alone.
func memberOnlyRoute(pattern string) bool {
	route, ok := routeOperations[pattern]
	return ok && authority.Strictest(operationsOf(route.ids)) == authority.ClassMemberOnly
}

// The values a request's context carries: the delegate a credential proved, the project a route serves,
// and where a member's word came from.
type (
	delegateKey struct{}
	projectKey  struct{}
	channelKey  struct{}
)

func withDelegate(ctx context.Context, credential authority.DelegateCredential) context.Context {
	return context.WithValue(ctx, delegateKey{}, credential)
}

func delegateOf(ctx context.Context) (authority.DelegateCredential, bool) {
	credential, ok := ctx.Value(delegateKey{}).(authority.DelegateCredential)
	return credential, ok
}

func withProject(ctx context.Context, project ledger.Project) context.Context {
	return context.WithValue(ctx, projectKey{}, project)
}

func projectOf(ctx context.Context) (ledger.Project, bool) {
	project, ok := ctx.Value(projectKey{}).(ledger.Project)
	return project, ok
}

func withChannel(ctx context.Context, channel string) context.Context {
	return context.WithValue(ctx, channelKey{}, channel)
}

// channelOf is where a member's word came from: the web listener, or the local socket.
func channelOf(ctx context.Context) string {
	if channel, ok := ctx.Value(channelKey{}).(string); ok {
		return channel
	}
	return authority.ChannelCLI
}

// gate stands between the socket and every route. A request with no delegate credential passes as it
// came. One with a credential must carry no session, must name a credential that acts, and reaches only
// its own project's routes: an installation route refuses it, as a member's alone when its operation is.
type gate struct {
	mux    *http.ServeMux
	home   *handler
	router *router
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get(api.HeaderDelegate)
	if token == "" {
		g.mux.ServeHTTP(w, r)
		return
	}
	if r.Header.Get(api.HeaderSession) != "" {
		invalid(w, "a request carries a member's session or a delegate credential, never both")
		return
	}
	credential, err := g.home.delegates.Authenticate(r.Context(), g.home.Scope, token)
	if err != nil {
		fail(w, err)
		return
	}
	_, pattern := g.mux.Handler(r)
	if pattern != projectPrefix+"{project}/" {
		if memberOnlyRoute(pattern) {
			fail(w, fmt.Errorf("%w: a delegate never calls %s", authority.ErrMemberOnly, r.URL.Path))
			return
		}
		fail(w, fmt.Errorf("%w: %s is not a route of project %s", authority.ErrOutsideProject, r.URL.Path, credential.Project))
		return
	}
	project, served, err := g.router.project(r.Context(), projectNameOf(r))
	if err != nil || project.ID != credential.Project {
		fail(w, fmt.Errorf("%w: this credential acts in project %s", authority.ErrOutsideProject, credential.Project))
		return
	}
	// An assistant's credential acts only while the generation it was made for runs, whatever a revocation
	// that may have been lost left.
	if err := control.AssistantActs(r.Context(), served.handler.Store, project.ID, credential); err != nil {
		fail(w, err)
		return
	}
	g.mux.ServeHTTP(w, r.WithContext(withDelegate(r.Context(), credential)))
}

// projectNameOf reads the project a path under /v1/projects/ names.
func projectNameOf(r *http.Request) string {
	segments := strings.SplitN(r.URL.EscapedPath(), "/", 5)
	if len(segments) < 4 {
		return ""
	}
	name, err := url.PathUnescape(segments[3])
	if err != nil {
		return ""
	}
	return name
}

// member runs a handler for the principal a request proves: a member's live session, or a delegate
// credential the gate admitted. A member runs every operation. A delegate runs an operational one as
// itself, and a consent one the delegate policy in force exempts, which leaves an event naming that
// version. Any other consent operation becomes a card, and a member-only one is refused. The socket
// admits only the owning user (NFR-SEC-2), and a lane runs as that user, so reaching the socket proves
// nothing: everything that changes passes through here (D26).
func (h *handler) member(next func(http.ResponseWriter, *http.Request, ledger.Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if credential, ok := delegateOf(r.Context()); ok && credential.Assistant() {
			h.assisted(w, r, credential, next)
			return
		}
		actor, exempt, ok := h.admit(w, r)
		if !ok {
			return
		}
		if exempt == nil {
			next(w, r, actor)
			return
		}
		recorder := &statusWriter{ResponseWriter: w}
		next(recorder, r, actor)
		if recorder.status < http.StatusMultipleChoices {
			// The operation's own events already name the delegate, so a failure to keep this record
			// loses only the version that exempted it.
			_ = h.approvals.Exempted(r.Context(), h.Scope, exempt.subject, exempt.detail, exempt.version, actor)
		}
	}
}

// callKey carries what a call of the assistant's resolved to, for the entry it leaves in its conversation.
type callKey struct{}

// assistantCall is the operation an assistant's call resolved to, and the record it is about.
type assistantCall struct {
	op      authority.Operation
	subject string
}

// callSubject names the record a call is about, as a card binds to it.
func callSubject(h *handler, r *http.Request, body []byte, route routeOperation) string {
	kind, id := route.kind, r.PathValue("id")
	if route.subject != nil {
		id = route.subject(h, r, body)
	}
	if kind == "" || id == "" {
		return ""
	}
	return kind + ":" + id
}

// assisted runs a changing call of an assistant's credential as any delegate's, and records it in the
// conversation the credential acts in: an action that ran, or the card it became. A refusal records
// nothing. A start of a planning or adopt lane names the lane, so the conversation can follow it.
func (h *handler) assisted(w http.ResponseWriter, r *http.Request, credential authority.DelegateCredential, next func(http.ResponseWriter, *http.Request, ledger.Actor)) {
	call := &assistantCall{}
	r = r.WithContext(context.WithValue(r.Context(), callKey{}, call))
	recorder := &capturingWriter{ResponseWriter: w}
	actor, exempt, ok := h.admit(recorder, r)
	if ok {
		next(recorder, r, actor)
		if exempt != nil && recorder.status < http.StatusMultipleChoices {
			_ = h.approvals.Exempted(r.Context(), h.Scope, exempt.subject, exempt.detail, exempt.version, actor)
		}
	}
	if recorder.status >= http.StatusMultipleChoices || call.op.ID == "" {
		return
	}
	action := conversations.ActionData{Operation: call.op.ID, Label: call.op.Label, Subject: call.subject, Outcome: conversations.OutcomeRan}
	if recorder.status == http.StatusAccepted {
		var prepared api.Prepared
		if json.Unmarshal(recorder.body.Bytes(), &prepared) != nil || prepared.Card.ID == "" {
			return
		}
		action.Outcome, action.Approval = conversations.OutcomeCard, prepared.Card.ID
	}
	if action.Outcome == conversations.OutcomeRan && call.op.ID == authority.OpLaneStart {
		action.PlanningLane = h.planningLane(r, recorder.body.Bytes())
	}
	// The call already ran or became a card, so a failure to record it loses only its line in the
	// conversation, and its events and its card stay.
	_ = h.conversations.RecordAction(context.WithoutCancel(r.Context()), h.Scope, credential, action)
}

// planningLane names the lane a start made when its work order plans or adopts.
func (h *handler) planningLane(r *http.Request, answer []byte) string {
	var dispatched api.LaneDispatched
	if json.Unmarshal(answer, &dispatched) != nil || dispatched.LaneID == "" {
		return ""
	}
	order, err := h.workOrder(r.Context(), r.PathValue("id"))
	if err != nil || order.Kind != workorders.KindPlan {
		return ""
	}
	return dispatched.LaneID
}

// capturingWriter keeps the status and the body a handler answered with, as it passes them on.
type capturingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capturingWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturingWriter) Write(raw []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body.Write(raw)
	return c.ResponseWriter.Write(raw)
}

// exemption is a consent operation a delegate runs with no card, and the version that exempted it.
type exemption struct {
	subject ledger.Subject
	detail  authority.CardDetail
	version int64
}

// statusWriter keeps the status a handler answered with.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusWriter) Write(raw []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(raw)
}

// admit reads who a request proves, and for a delegate what its call may do.
func (h *handler) admit(w http.ResponseWriter, r *http.Request) (ledger.Actor, *exemption, bool) {
	if credential, ok := delegateOf(r.Context()); ok {
		return h.delegated(w, r, credential)
	}
	if r.Header.Get(api.HeaderDelegate) != "" {
		fail(w, fmt.Errorf("%w: this credential did not pass the gate", authority.ErrBadDelegate))
		return ledger.Actor{}, nil, false
	}
	actor, err := h.sessions.Member(r.Context(), h.Scope, sessionOf(r))
	if err != nil {
		fail(w, err)
		return ledger.Actor{}, nil, false
	}
	return actor, nil, true
}

// readBody reads a request's body whole and puts it back, so a resolver and the handler both read it.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		invalid(w, "%v", err)
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, true
}

// delegated applies the class of a delegate's call.
func (h *handler) delegated(w http.ResponseWriter, r *http.Request, credential authority.DelegateCredential) (ledger.Actor, *exemption, bool) {
	actor := credential.Actor()
	body, ok := readBody(w, r)
	if !ok {
		return actor, nil, false
	}
	route, known := routeOperations[r.Pattern]
	if !known && !changes(r.Method) {
		return actor, nil, true
	}
	// A record a handler finds by a name or a prefix is found once, here, and the call goes on naming it by
	// its identity, so the class, the card, and the handler all read the same record.
	if known && route.canonical != nil {
		id, err := route.canonical(h, r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return actor, nil, false
		}
		r.SetPathValue("id", id)
	}
	if known && route.check != nil {
		if err := route.check(body); err != nil {
			fail(w, err)
			return actor, nil, false
		}
	}
	var ids []string
	var oneWay bool
	if known {
		ids = route.ids[:1]
		if route.resolve != nil {
			var err error
			if ids, oneWay, err = route.resolve(h, r, body); err != nil {
				fail(w, err)
				return actor, nil, false
			}
		}
	}
	ops := operationsOf(ids)
	if call, ok := r.Context().Value(callKey{}).(*assistantCall); ok {
		call.op, call.subject = strictestOf(ops), callSubject(h, r, body, route)
	}
	switch authority.Strictest(ops) {
	case authority.ClassMemberOnly:
		fail(w, fmt.Errorf("%w: %s", authority.ErrMemberOnly, strictestOf(ops).Label))
		return actor, nil, false
	case authority.ClassOperational:
		return actor, nil, true
	}
	version, policy, err := h.approvals.DelegatePolicyInForce(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return actor, nil, false
	}
	detail, subject, err := h.cardDetail(r, body, route, ops, actor)
	if err != nil {
		fail(w, err)
		return actor, nil, false
	}
	if known && policy.Exempts(ops, oneWay) {
		return actor, &exemption{subject: ledger.Subject{Kind: subject.Kind, ID: subject.ID}, detail: detail, version: version.Version}, true
	}
	h.prepare(w, r, body, route, ops, detail, subject, actor)
	return actor, nil, false
}

// strictestOf is the operation a card names when a call runs several: the strictest, and of those, one
// no policy exempts.
func strictestOf(ops []authority.Operation) authority.Operation {
	weight := func(op authority.Operation) int {
		switch {
		case op.Class == authority.ClassMemberOnly:
			return 3
		case op.Class == authority.ClassConsent && !op.Exemptable:
			return 2
		case op.Class == authority.ClassConsent:
			return 1
		}
		return 0
	}
	best := ops[0]
	for _, op := range ops[1:] {
		if weight(op) > weight(best) {
			best = op
		}
	}
	return best
}

// cardDetail is what a card for this call holds, and the subject it binds to.
func (h *handler) cardDetail(r *http.Request, body []byte, route routeOperation, ops []authority.Operation, actor ledger.Actor) (authority.CardDetail, control.Binding, error) {
	op := strictestOf(ops)
	arguments := json.RawMessage("null")
	if len(bytes.TrimSpace(body)) != 0 {
		if !json.Valid(body) {
			return authority.CardDetail{}, control.Binding{}, fmt.Errorf("%w: the request body is not JSON", authority.ErrInvalid)
		}
		arguments = json.RawMessage(body)
	}
	kind, id := route.kind, r.PathValue("id")
	if kind == "" {
		kind, id = "route", r.Pattern
	}
	if route.subject != nil {
		id = route.subject(h, r, body)
	}
	if id == "" {
		id = "new"
	}
	binding, err := control.BindingFor(op.ID, kind, id, arguments)
	if err != nil {
		return authority.CardDetail{}, control.Binding{}, err
	}
	var changes any
	if route.changes != nil {
		if changes, err = route.changes(h, r, body); err != nil {
			return authority.CardDetail{}, control.Binding{}, err
		}
	} else {
		changes = argumentChanges(body)
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return authority.CardDetail{}, control.Binding{}, err
	}
	return authority.CardDetail{
		Operation: op.ID, Label: op.Label, Method: r.Method, Path: projectPath(r), Arguments: arguments,
		Why: r.Header.Get(api.HeaderWhy), Changes: raw,
	}, binding, nil
}

// routePath is a call's path as its route's pattern spells it with the request's path values, so a value
// read again, such as a record's identity in place of its name, is the one the path holds.
func routePath(r *http.Request) string {
	_, pattern, found := strings.Cut(r.Pattern, " ")
	if !found {
		return r.URL.EscapedPath()
	}
	var b strings.Builder
	for {
		start := strings.Index(pattern, "{")
		if start < 0 {
			b.WriteString(pattern)
			return b.String()
		}
		end := strings.Index(pattern[start:], "}")
		if end < 0 {
			return r.URL.EscapedPath()
		}
		b.WriteString(pattern[:start])
		b.WriteString(url.PathEscape(r.PathValue(pattern[start+1 : start+end])))
		pattern = pattern[start+end+1:]
	}
}

// projectPath is the full project-scoped path of a call a project's routes serve.
func projectPath(r *http.Request) string {
	path := routePath(r)
	if project, ok := projectOf(r.Context()); ok {
		path = projectPrefix + url.PathEscape(project.Name) + strings.TrimPrefix(path, "/v1")
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return path
}

// argumentChanges is what a call that names no record to read changes: each argument it sends.
func argumentChanges(body []byte) []authority.ArgumentChange {
	changes := []authority.ArgumentChange{}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return changes
	}
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		changes = append(changes, authority.ArgumentChange{Field: field, To: fields[field]})
	}
	return changes
}

// prepare records a card for a delegate's consent call, which runs nothing, and answers with it.
func (h *handler) prepare(w http.ResponseWriter, r *http.Request, body []byte, route routeOperation, ops []authority.Operation, detail authority.CardDetail, binding control.Binding, actor ledger.Actor) {
	why := strings.TrimSpace(detail.Why)
	if why == "" {
		invalid(w, "A card needs the reason the delegate proposes it.")
		return
	}
	if len(detail.Why) > authority.MaxWhy {
		invalid(w, "a delegate's reason is at most %d characters", authority.MaxWhy)
		return
	}
	digest, err := h.approvals.Bind(r.Context(), h.Scope, binding)
	if err != nil {
		fail(w, err)
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		fail(w, err)
		return
	}
	card, err := h.approvals.Prepare(r.Context(), h.Scope, authority.Approval{
		Subject: binding.Subject(), SubjectDigest: digest, Summary: detail.Label + ": " + h.subjectName(r.Context(), binding), Detail: string(raw),
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusAccepted, api.Prepared{Card: card})
}

// subjectName is how a card's title names its subject: a repository by its name, and anything else by
// its ID.
func (h *handler) subjectName(ctx context.Context, binding control.Binding) string {
	if binding.Kind == ledger.SubjectRepository {
		var name string
		err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
			record, err := tx.Repositories().Repository(binding.ID)
			name = record.Name
			return err
		})
		if err == nil && name != "" {
			return name
		}
	}
	if binding.Kind == "route" {
		return strings.TrimPrefix(binding.ID, "/v1/")
	}
	return binding.ID
}

// onConfirmed runs between a card's approval and its call, which a test uses to act at that moment.
var onConfirmed = func(context.Context) {}

// confirmCard applies a member's word on a card. A confirmation that stands runs the card's exact call
// through the project's own routes, as the member whose session made this one, so the operation takes
// the same checks and leaves the same events as the member's own call. What the API answered decides
// whether the card was used or failed.
func (h *handler) confirmCard(w http.ResponseWriter, r *http.Request, decision authority.Decision, actor ledger.Actor) {
	confirmed, err := h.approvals.Confirm(r.Context(), h.Scope, decision, actor, channelOf(r.Context()))
	if err != nil {
		fail(w, err)
		return
	}
	if !confirmed.Decided || confirmed.Card.State != authority.ApprovalApproved {
		reply(w, http.StatusOK, confirmed.Card)
		return
	}
	onConfirmed(r.Context())
	// The card is approved, so its call runs and its outcome is kept whatever becomes of this request: a
	// member's client that goes away must not leave a card running for good.
	ctx := context.WithoutCancel(r.Context())
	if moved, err := h.movedSince(ctx, confirmed.Card); err != nil || moved != "" {
		if err == nil {
			_, err = h.approvals.Void(ctx, h.Scope, confirmed.Card.ID, moved, actor)
		}
		if err == nil {
			err = fmt.Errorf("%w: %s", authority.ErrStaleDigest, moved)
		}
		fail(w, err)
		return
	}
	ran, refusal := h.replay(ctx, r, confirmed.Card)
	finished, err := h.approvals.Finish(ctx, h.Scope, confirmed.Card.ID, ran, refusal, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, finished)
}

// movedSince says why a confirmed card no longer addresses what it was bound to, or nothing when it
// still does. The card is bound again just before its call runs, so a subject that moved after the
// confirmation's own check voids it. A repository's call reaches its record by a name or a prefix as well
// as its identity, so the identity the card holds must also still read as that record and no other.
func (h *handler) movedSince(ctx context.Context, card authority.Approval) (string, error) {
	bound, err := h.approvals.StillBound(ctx, h.Scope, card)
	if err != nil {
		return "", err
	}
	if !bound {
		return "The subject changed since the card was prepared.", nil
	}
	kind, id, _ := strings.Cut(card.Subject, ":")
	if kind != ledger.SubjectRepository {
		return "", nil
	}
	resolved, err := repositoryID(h, ctx, id)
	if err == nil && resolved == id {
		return "", nil
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) && !errors.Is(err, ledger.ErrInvalidRepository) {
		return "", err
	}
	return "The record the card names no longer reads as the one it was prepared for.", nil
}

// replay runs a card's call as the member who confirmed it, and says whether the API ran it, with its
// refusal when it did not.
func (h *handler) replay(ctx context.Context, r *http.Request, card authority.Approval) (bool, string) {
	detail, err := authority.CardDetailOf(card)
	if err != nil {
		return false, err.Error()
	}
	path := detail.Path
	if project, ok := projectOf(r.Context()); ok {
		path = retarget(path, project.Name)
	}
	var body io.Reader
	if args := bytes.TrimSpace(detail.Arguments); len(args) != 0 && !bytes.Equal(args, []byte("null")) {
		body = bytes.NewReader(args)
	}
	inner, err := http.NewRequestWithContext(ctx, detail.Method, path, body)
	if err != nil {
		return false, err.Error()
	}
	if body != nil {
		inner.Header.Set("Content-Type", "application/json")
	}
	inner.Header.Set(api.HeaderSession, sessionOf(r))
	answer := &bufferedAnswer{header: http.Header{}}
	h.root.ServeHTTP(answer, inner)
	if answer.status == 0 {
		answer.status = http.StatusOK
	}
	if answer.status < http.StatusMultipleChoices {
		return true, ""
	}
	var refused api.ErrorBody
	if err := json.Unmarshal(answer.body.Bytes(), &refused); err != nil || refused.Error.Code == "" {
		return false, fmt.Sprintf("the API answered %d", answer.status)
	}
	return false, refused.Error.Code + ": " + refused.Error.Message
}

// retarget names the project a path serves by its current name, which a rename since the card was
// prepared changed.
func retarget(path, name string) string {
	rest, ok := strings.CutPrefix(path, projectPrefix)
	if !ok {
		return path
	}
	_, after, found := strings.Cut(rest, "/")
	if !found {
		return path
	}
	return projectPrefix + url.PathEscape(name) + "/" + after
}

// bufferedAnswer is a response held in memory, for a call the server makes of itself.
type bufferedAnswer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedAnswer) Header() http.Header { return b.header }

func (b *bufferedAnswer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferedAnswer) Write(raw []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(raw)
}

// The resolvers of the routes whose class turns on the call.

func reviewOperation(h *handler, r *http.Request, _ []byte) ([]string, bool, error) {
	order, err := h.workOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, false, err
	}
	if order.Kind == workorders.KindPlan {
		return []string{authority.OpWorkOrderReviewPlan}, false, nil
	}
	return []string{authority.OpWorkOrderReview}, false, nil
}

func retryOperation(h *handler, r *http.Request, _ []byte) ([]string, bool, error) {
	order, err := h.workOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, false, err
	}
	left, err := h.recoveries.AttemptsLeft(r.Context(), h.Scope, order)
	if err != nil {
		return nil, false, err
	}
	if left {
		return []string{authority.OpWorkOrderRetry}, false, nil
	}
	return []string{authority.OpWorkOrderRetryExtra}, false, nil
}

func fleetModeOperation(_ *handler, _ *http.Request, body []byte) ([]string, bool, error) {
	var in api.SetFleetMode
	if err := json.Unmarshal(body, &in); err == nil && (in.Mode == ledger.FleetPause || in.Mode == ledger.FleetDrain) {
		return []string{authority.OpFleetPause}, false, nil
	}
	return []string{authority.OpFleetRun}, false, nil
}

func answerOperation(h *handler, r *http.Request, _ []byte) ([]string, bool, error) {
	oneWay, err := h.questions.AnswerStanding(r.Context(), h.Scope, r.PathValue("id"))
	return []string{authority.OpQuestionAnswer}, oneWay, err
}

func decisionOperation(h *handler, r *http.Request, body []byte) ([]string, bool, error) {
	var in api.RecordDecision
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, false, fmt.Errorf("%w: %v", questions.ErrInvalid, err)
	}
	standing, err := h.questions.Weigh(r.Context(), h.Scope, questions.Decision{
		ID: in.ID, Status: in.Status, Statement: in.Statement, Rationale: in.Rationale, Citations: in.Citations,
		Scope: in.Scope, Reversibility: in.Reversibility, Categories: in.Categories, Supersedes: in.Supersedes,
		QuestionID: in.QuestionID, NoConflict: in.NoConflict,
	})
	if err != nil {
		return nil, false, err
	}
	if standing.WithinPolicy {
		return []string{authority.OpDecisionAssume}, false, nil
	}
	return []string{authority.OpDecisionRecord}, standing.OneWay, nil
}

func policyOperation(_ *handler, _ *http.Request, body []byte) ([]string, bool, error) {
	var in api.PublishPolicy
	if err := json.Unmarshal(body, &in); err == nil && holdsTable(in.Body, delegatePolicyTable) {
		return []string{authority.OpDelegatePolicyPublish}, false, nil
	}
	return []string{authority.OpPolicyPublish}, false, nil
}

// messageOperation reads the class of a message from who it is for: only a member instructs an orchestrator,
// which holds tools such as approval_decide and lane_start. A body that does not say who it is for takes
// the stricter class.
func messageOperation(_ *handler, _ *http.Request, body []byte) ([]string, bool, error) {
	var in messages.Draft
	if err := json.Unmarshal(body, &in); err != nil || in.To.Of() == messages.TargetOrchestrator {
		return []string{authority.OpMessageOrchestrator}, false, nil
	}
	return []string{authority.OpMessageSend}, false, nil
}

// byKind reads the class of a call that creates a work order from the credential that makes it. An
// orchestrator holds tools such as approval_decide and lane_start, and its wake states the title of work to
// claim, so an outside delegate's call takes the consent operation, outside. The assistant acts in a
// member's own conversation, so its call takes the operational one, assistant.
func byKind(outside, assistant string) resolver {
	return func(_ *handler, r *http.Request, _ []byte) ([]string, bool, error) {
		if credential, ok := delegateOf(r.Context()); ok && credential.Assistant() {
			return []string{assistant}, false, nil
		}
		return []string{outside}, false, nil
	}
}

func repositoryOperation(h *handler, r *http.Request, body []byte) ([]string, bool, error) {
	var in api.RepositoryChange
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ledger.ErrInvalidRepository, err)
	}
	before, err := h.repository(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, false, err
	}
	var ids []string
	if in.Name != nil || in.Base != nil || in.StartFrom != nil || in.Planning != nil {
		ids = append(ids, authority.OpRepositorySet)
	}
	if in.MergeAuthority != nil {
		switch {
		case in.MergeAuthority.Rank() > before.Level().Rank():
			ids = append(ids, authority.OpRepositoryRaiseMergeAuthority)
		default:
			ids = append(ids, authority.OpRepositoryLowerMergeAuthority)
		}
	}
	if in.ReviewTarget != nil {
		if *in.ReviewTarget == ledger.ReviewOnPullRequest {
			ids = append(ids, authority.OpRepositoryReviewTargetPullRequest)
		} else {
			ids = append(ids, authority.OpRepositoryReviewTargetBranch)
		}
	}
	if in.ForgeBinding != nil || in.MergeMethod != nil {
		ids = append(ids, authority.OpRepositoryForge)
	}
	if len(ids) == 0 {
		ids = append(ids, authority.OpRepositorySet)
	}
	return ids, false, nil
}

func approvalOperation(h *handler, r *http.Request, _ []byte) ([]string, bool, error) {
	row, err := h.approvals.Get(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		return nil, false, err
	}
	switch {
	case row.Kind == authority.KindConfirm:
		return []string{authority.OpCardConfirm}, false, nil
	case row.MembersOnly():
		return []string{authority.OpApprovalDecide}, false, nil
	}
	covered, err := h.approvals.Covered(r.Context(), h.Scope, row)
	if err != nil {
		return nil, false, err
	}
	if covered {
		return []string{authority.OpApprovalDecideDelegated}, false, nil
	}
	return []string{authority.OpApprovalDecide}, false, nil
}

// The subjects of the routes whose path does not name their record.

func rosterScope(*handler, *http.Request, []byte) string { return "project" }

func theWorkspace(h *handler, _ *http.Request, _ []byte) string { return string(h.Scope) }

func bodyField(name string) func(*handler, *http.Request, []byte) string {
	return func(_ *handler, _ *http.Request, body []byte) string {
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			return ""
		}
		value, _ := fields[name].(string)
		return value
	}
}

func policyKindOf(_ *handler, _ *http.Request, body []byte) string {
	var in api.PublishPolicy
	if err := json.Unmarshal(body, &in); err != nil {
		return ""
	}
	return string(policyKindOfBody(in.Body))
}

// policyKindOfBody is the kind of policy a body's table publishes.
func policyKindOfBody(body string) authority.PolicyKind {
	for _, kind := range []struct {
		table string
		kind  authority.PolicyKind
	}{
		{delegatePolicyTable, authority.PolicyDelegate}, {notifyTable, authority.PolicyNotify}, {autonomyTable, authority.PolicyAutonomy},
		{delegationTable, authority.PolicyDelegation}, {recoveryPolicyTable, authority.PolicyRecovery}, {lanePolicyTable, authority.PolicyLane},
		{assistantTable, authority.PolicyAssistant}, {estimatePolicyTable, delivery.PolicyEstimate},
	} {
		if holdsTable(body, kind.table) {
			return kind.kind
		}
	}
	return authority.PolicyDecision
}

// The changes of the routes whose record says what a call changes.

func repositoryChanges(h *handler, r *http.Request, body []byte) (any, error) {
	before, err := h.repository(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	var in api.RepositoryChange
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", ledger.ErrInvalidRepository, err)
	}
	changes := []authority.FieldChange{}
	add := func(field string, from, to any) {
		changes = append(changes, authority.FieldChange{Field: field, From: from, To: to})
	}
	if in.Name != nil {
		add("name", before.Name, *in.Name)
	}
	if in.Base != nil {
		add("base", before.Base(), *in.Base)
	}
	if in.MergeMethod != nil {
		add("merge_method", before.MergeMethod, *in.MergeMethod)
	}
	if in.StartFrom != nil {
		add("start_from", before.StartFrom, *in.StartFrom)
	}
	if in.ReviewTarget != nil {
		add("review_target", before.Target(), *in.ReviewTarget)
	}
	if in.ForgeBinding != nil {
		add("forge_binding", before.ForgeBinding, *in.ForgeBinding)
	}
	if in.MergeAuthority != nil {
		add("merge_authority", before.Level(), *in.MergeAuthority)
	}
	if in.Planning != nil {
		add("planning", before.Planning, *in.Planning)
	}
	return changes, nil
}

func capsChanges(h *handler, r *http.Request, body []byte) (any, error) {
	workspace, err := h.fleet.Read(r.Context(), h.Scope)
	if err != nil {
		return nil, err
	}
	var to map[string]any
	if err := json.Unmarshal(body, &to); err != nil {
		return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	raw, err := json.Marshal(workspace.Caps())
	if err != nil {
		return nil, err
	}
	var from map[string]any
	if err := json.Unmarshal(raw, &from); err != nil {
		return nil, err
	}
	changes := []authority.FieldChange{}
	for _, field := range slices.Sorted(maps.Keys(to)) {
		changes = append(changes, authority.FieldChange{Field: field, From: from[field], To: to[field]})
	}
	return changes, nil
}

func policyChanges(h *handler, r *http.Request, body []byte) (any, error) {
	var in api.PublishPolicy
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	versions, err := h.versionsOf(r.Context(), policyKindOfBody(in.Body))
	if err != nil {
		return nil, err
	}
	change := authority.BodyChange{After: in.Body}
	if len(versions) != 0 {
		change.Before = versions[0].Body
	}
	return change, nil
}

func rosterChanges(h *handler, r *http.Request, body []byte) (any, error) {
	var change *api.RosterChange
	var revert int64
	if strings.HasSuffix(r.Pattern, "/revert") {
		var in api.RosterRevert
		if err := json.Unmarshal(body, &in); err != nil {
			return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
		}
		revert = in.Version
	} else {
		change = &api.RosterChange{}
		if err := json.Unmarshal(body, change); err != nil {
			return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
		}
	}
	var asked *roster.Change
	if change != nil {
		asked = &change.Change
	}
	before, after, err := h.rosterOps().Preview(r.Context(), h.rosterTarget(roster.ScopeProject), asked, revert)
	if err != nil {
		return nil, err
	}
	return authority.BodyChange{Before: before, After: after}, nil
}

// The reads the resolvers share.

func (h *handler) workOrder(ctx context.Context, id string) (workorders.WorkOrder, error) {
	var order workorders.WorkOrder
	err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		var err error
		order, err = tx.WorkOrders().Get(id)
		return err
	})
	return order, err
}

func (h *handler) repository(ctx context.Context, id string) (ledger.Repository, error) {
	var record ledger.Repository
	err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Repositories().Repository(id)
		return err
	})
	return record, err
}

// versionsOf reads the versions of one kind of policy, newest first.
func (h *handler) versionsOf(ctx context.Context, kind authority.PolicyKind) ([]authority.PolicyVersion, error) {
	switch kind {
	case authority.PolicyNotify:
		return h.notices.NotifyVersions(ctx, h.Scope)
	case authority.PolicyAutonomy:
		return h.autonomy.AutonomyVersions(ctx, h.Scope)
	case authority.PolicyDelegation:
		return h.approvals.DelegationVersions(ctx, h.Scope)
	case authority.PolicyLane:
		return h.questions.LanePolicyVersions(ctx, h.Scope)
	case authority.PolicyRecovery:
		return h.recoveries.RecoveryPolicyVersions(ctx, h.Scope)
	case delivery.PolicyEstimate:
		return h.estimates.EstimatePolicyVersions(ctx, h.Scope)
	case authority.PolicyDelegate:
		return h.approvals.DelegatePolicyVersions(ctx, h.Scope)
	case authority.PolicyAssistant:
		return h.conversations.AssistantVersions(ctx, h.Scope)
	}
	return h.questions.PolicyVersions(ctx, h.Scope)
}

// operations answers the class table, which a person reads to see what a delegate may do.
func (h *handler) operations(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusOK, api.OperationList{Operations: authority.Operations()})
}
