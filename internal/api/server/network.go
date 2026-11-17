package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// The network listener is a group server's one TCP listener (ADR-0031). It serves the web UI, the API, and
// the worker plane, and no route of it trusts the transport: the routes on the local socket may trust its
// file permissions, and a TCP port gives a caller no such proof. A request on this listener proves itself
// with exactly one of:
//
//   - a worker's credential in Authorization, which reaches the routes of PlaneRoutes and no other;
//   - a join token in the body of the registration, the one route a worker calls before it has a credential;
//   - a member's session as a bearer token in the Ropeshift-Session header, which reaches the API;
//   - the web UI's session cookie with its CSRF token, which reaches the web listener's closed list.
//
// Only OpenRoutes take no credential. A request that presents two credentials, or a delegate's, is refused.

// credentialKind names what a request proves itself with.
type credentialKind string

const (
	kindWorker  credentialKind = "a worker credential"
	kindSession credentialKind = "a member session"
	kindCookie  credentialKind = "a web session cookie"
	kindJoin    credentialKind = "a join token"
)

// OpenRoute is a route of the network listener that takes no credential.
type OpenRoute struct {
	Method  string
	Pattern string
	// Why says what makes the route safe to leave open.
	Why string
}

// OpenRoutes is every route the network listener serves with no credential of any kind: the application's
// own files, and the route that signs a member in. Everything else answers 401 to a request that presents
// none, and the test that names this list walks every route of the listener to hold it there.
func OpenRoutes() []OpenRoute {
	return []OpenRoute{
		{http.MethodGet, "/", "The application's own files, which every path outside /v1 reads, the sign-in page among them: static, and the same for every caller."},
		{http.MethodHead, "/", "The same files, asked for their headers alone."},
		{http.MethodPost, api.SignInPath, "The sign-in route: its credential is the passphrase in its body, which the web handler checks one attempt at a time, and its answer is a cookie only that page can read."},
	}
}

// PlaneRoute is a route of the worker plane.
type PlaneRoute struct {
	Method  string
	Pattern string
	// JoinToken says the route takes the join token in its body, where every other plane route takes the
	// worker's credential.
	JoinToken bool
}

// PlaneRoutes is every route a worker's credential reaches, and the registration its join token reaches. The
// use case behind each one checks the credential itself, and the listener hands it nothing else: a worker's
// credential opens none of a member's routes, whatever the credential is.
func PlaneRoutes() []PlaneRoute {
	return []PlaneRoute{
		{http.MethodPost, "/v1/workers/register", true},
		{http.MethodPost, "/v1/workers/{id}/sessions", false},
		{http.MethodPost, "/v1/workers/{id}/heartbeat", false},
		{http.MethodGet, "/v1/workers/{id}/desired", false},
		{http.MethodGet, "/v1/workers/{id}/watch", false},
		{http.MethodPost, "/v1/workers/{id}/events", false},
		{http.MethodPost, "/v1/workers/{id}/claims", false},
		{http.MethodPost, "/v1/workers/{id}/ask", false},
		{http.MethodPost, "/v1/workers/{id}/lane-facts", false},
		{http.MethodPost, "/v1/workers/{id}/lane-context", false},
		{http.MethodPost, "/v1/workers/{id}/rulings", false},
		{http.MethodPost, "/v1/workers/{id}/approvals/use", false},
		{http.MethodPost, "/v1/workers/{id}/approvals/decide", false},
		{http.MethodPost, "/v1/workers/{id}/assistant-credentials", false},
		{http.MethodPost, "/v1/workers/{id}/assistant-entries", false},
		{http.MethodPost, "/v1/workers/{id}/orchestrator/digest", false},
		{http.MethodPost, "/v1/workers/{id}/orchestrator/acts/{verb}", false},
	}
}

// A body is read before the credential it comes with is checked: a registration's join token, a sign-in's
// passphrase, and a worker's call, which its use case checks after it decodes it. So each class of route gives
// its body a limit and a window, and a client that sends one slowly or without end holds nothing past them.
const (
	// registerBodyMax and signInBodyMax are the most of a body the two routes with no credential in a header
	// read. Each is a few lines.
	registerBodyMax = 16 << 10
	signInBodyMax   = 16 << 10
)

var (
	// unauthenticatedWindow is how long a registration or a sign-in has to arrive, from the moment its headers
	// did, and planeWindow how long a worker's call has, which an upload of events may take more of.
	unauthenticatedWindow = 10 * time.Second
	planeWindow           = time.Minute
)

// bounded makes a request's body arrive within a window and up to a limit. A request with no body has no
// window: the server already reads its connection for the client to go away, and a deadline on that read would
// end the stream a worker holds open.
func bounded(w http.ResponseWriter, r *http.Request, window time.Duration, limit int64) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(window))
	r.Body = http.MaxBytesReader(w, r.Body, limit)
}

// network is the handler of the network listener.
type network struct {
	api http.Handler
	// web serves the application's files, and its closed list of routes behind a cookie and a CSRF token.
	web      http.Handler
	sessions *control.Sessions
	scope    ledger.WorkspaceID
	// workerRoutes and joinRoutes only match: a request names a plane route when they say so.
	workerRoutes *http.ServeMux
	joinRoutes   *http.ServeMux
	recheck      time.Duration
}

// NewNetwork returns the handler of a group server's TCP listener over the same use cases as the socket.
func NewNetwork(config Config, options WebOptions) http.Handler {
	n := &network{
		api: New(config), web: NewWeb(config, options), sessions: control.NewSessions(config.Store, config.Identity),
		scope: config.Scope, workerRoutes: http.NewServeMux(), joinRoutes: http.NewServeMux(), recheck: options.Recheck,
	}
	if n.recheck <= 0 {
		n.recheck = defaultRecheck
	}
	for _, route := range PlaneRoutes() {
		mux := n.workerRoutes
		if route.JoinToken {
			mux = n.joinRoutes
		}
		mux.HandleFunc(route.Method+" "+route.Pattern, func(http.ResponseWriter, *http.Request) {})
	}
	return n
}

// inAPI says a path is the API's, which only a credential opens. The path is the one the router matches, so
// a name that decodes to /v1 is /v1 here too, and a path that is not /v1's reaches the application's files
// and nothing else.
func inAPI(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

// presented lists the credentials a request carries.
func presented(r *http.Request) []credentialKind {
	var kinds []credentialKind
	if r.Header.Get("Authorization") != "" {
		kinds = append(kinds, kindWorker)
	}
	if sessionOf(r) != "" {
		kinds = append(kinds, kindSession)
	}
	if cookie, err := r.Cookie(api.CookieSession); err == nil && cookie.Value != "" {
		kinds = append(kinds, kindCookie)
	}
	return kinds
}

func (n *network) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(api.HeaderDelegate) != "" {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForbidden, Message: "A delegate uses the local socket, never the network listener."})
		return
	}
	if !inAPI(r.URL.Path) {
		n.web.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	kinds := presented(r)
	if len(kinds) > 1 {
		invalid(w, "a request carries one credential, and this one carries %s and %s", kinds[0], kinds[1])
		return
	}
	// A sign-in carries its passphrase in its body, whatever else it carries, such as a cookie that has expired.
	if r.Method == http.MethodPost && r.URL.Path == api.SignInPath {
		bounded(w, r, unauthenticatedWindow, signInBodyMax)
	}
	switch {
	case matches(n.joinRoutes, r):
		n.register(w, r, kinds)
	case len(kinds) == 0:
		if r.Method == http.MethodPost && r.URL.Path == api.SignInPath {
			n.web.ServeHTTP(w, r)
			return
		}
		n.missing(w, r)
	case kinds[0] == kindWorker:
		n.worker(w, r)
	case kinds[0] == kindSession:
		n.member(w, r)
	default:
		n.web.ServeHTTP(w, r)
	}
}

// matches says a request is for a route a mux of patterns holds.
func matches(mux *http.ServeMux, r *http.Request) bool {
	_, pattern := mux.Handler(r)
	return pattern != ""
}

// missing refuses a request that carries no credential. The code says which one the route would take.
func (n *network) missing(w http.ResponseWriter, r *http.Request) {
	code := api.CodeSessionRequired
	if matches(n.workerRoutes, r) {
		code = api.CodeBadCredential
	}
	refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: code, Message: "this listener answers a request that carries a credential, and this one carries none"})
}

// register passes a registration on only when it carries a join token, which the use case then checks
// against the digest it holds, once.
func (n *network) register(w http.ResponseWriter, r *http.Request, kinds []credentialKind) {
	if len(kinds) != 0 {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForbidden, Message: "a registration carries a join token and no other credential"})
		return
	}
	bounded(w, r, unauthenticatedWindow, registerBodyMax)
	raw, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		refuse(w, http.StatusRequestEntityTooLarge, api.ErrorDetail{Code: api.CodeInvalid, Message: "a registration is small"})
		return
	case err != nil:
		refuse(w, http.StatusRequestTimeout, api.ErrorDetail{Code: api.CodeInvalid, Message: "a registration arrives whole and at once"})
		return
	}
	var in struct {
		JoinToken string `json:"join_token"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.JoinToken == "" {
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadJoinToken, Message: "a registration carries a join token"})
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	n.api.ServeHTTP(w, r)
}

// worker hands the plane a request that carries a worker's credential, and refuses any other route.
func (n *network) worker(w http.ResponseWriter, r *http.Request) {
	if !matches(n.workerRoutes, r) {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForbidden, Message: "a worker's credential opens the worker plane and nothing else"})
		return
	}
	if bearer(r) == "" {
		refuse(w, http.StatusUnauthorized, api.ErrorDetail{Code: api.CodeBadCredential, Message: "a worker's credential is a bearer token"})
		return
	}
	bounded(w, r, planeWindow, maxBody)
	n.api.ServeHTTP(w, r)
}

// member hands the API a request whose session is live, and ends the request when the session ends, so a
// stream outlives neither.
func (n *network) member(w http.ResponseWriter, r *http.Request) {
	token := sessionOf(r)
	if _, err := n.sessions.Member(r.Context(), n.scope, token); err != nil {
		fail(w, err)
		return
	}
	// The socket sets the passphrase on the strength of its file permissions, and the web UI changes it with
	// the passphrase in force, ending the member's other sessions. A bearer token proves neither, and a
	// session token that leaked would otherwise replace the passphrase for good.
	if r.URL.Path == api.PassphrasePath && r.Method != http.MethodGet && r.Method != http.MethodHead {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForbidden, Message: "the passphrase is set on the server's own machine, and changed in the web UI with the passphrase in force"})
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go n.keep(ctx, cancel, token)
	n.api.ServeHTTP(w, r.WithContext(ctx))
}

// keep asks whether a session still stands, and ends the request that holds it when it does not.
func (n *network) keep(ctx context.Context, cancel context.CancelFunc, token string) {
	ticker := time.NewTicker(n.recheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := n.sessions.Member(ctx, n.scope, token); err != nil && !errors.Is(err, context.Canceled) {
				cancel()
				return
			}
		}
	}
}
