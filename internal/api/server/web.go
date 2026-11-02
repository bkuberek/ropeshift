package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// The application's files. A build writes the first, and a tree that was never built serves the
// second, which says so (D28).
const (
	indexFile       = "index.html"
	placeholderFile = "placeholder.html"
	assetDirectory  = "assets/"
)

// contentPolicy lets a page run only what this binary serves, and talk only to this listener. An
// application holds text from documents and agents, so nothing inline may run, whatever that text says.
const contentPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// csrfLabel separates this token's digest from any other use of a session.
const csrfLabel = "ropeshift web csrf"

// defaultRecheck is how often an open stream asks whether its session still stands.
const defaultRecheck = 5 * time.Second

// mediaTypes names what the application ships. A wrong guess from a name would meet `nosniff`.
var mediaTypes = map[string]string{
	".css": "text/css; charset=utf-8", ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8",
	".map": "application/json", ".svg": "image/svg+xml", ".woff2": "font/woff2",
}

// WebOptions is what the web listener serves beyond the API.
type WebOptions struct {
	// Port is the one this listener bound. Its names are the only hosts it answers to.
	Port int
	// Assets is the built application.
	Assets fs.FS
	// Recheck is how often an open stream asks whether its session still stands. Zero takes the
	// default.
	Recheck time.Duration
}

// web is the API as a browser reaches it: a closed list of routes, behind checks the local socket
// needs none of, because any process and any page on this machine can reach a loopback port.
type web struct {
	api      http.Handler
	routes   *http.ServeMux
	sessions *control.Sessions
	scope    ledger.WorkspaceID
	assets   fs.FS
	hosts    map[string]bool
	recheck  time.Duration
	// signIn lets one hash run at a time, so a flood of guesses costs its sender the wait.
	signIn chan struct{}
}

// NewWeb returns the web listener's handler over the same use cases as the API.
func NewWeb(config Config, options WebOptions) http.Handler {
	port := strconv.Itoa(options.Port)
	site := &web{
		api: New(config), routes: http.NewServeMux(), sessions: control.NewSessions(config.Store, config.Identity), scope: config.Scope,
		assets: options.Assets, recheck: options.Recheck, signIn: make(chan struct{}, 1),
		hosts: map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true},
	}
	if site.recheck <= 0 {
		site.recheck = defaultRecheck
	}
	for _, route := range api.WebRoutes() {
		pattern := route.Method + " " + route.Pattern
		switch route.Pattern {
		case api.SignInPath:
			site.routes.HandleFunc(pattern, site.openSession)
		case api.SignOutPath:
			site.routes.HandleFunc(pattern, site.member(site.closeSession))
		case api.EventsPath:
			site.routes.HandleFunc(pattern, site.member(site.stream))
		default:
			site.routes.HandleFunc(pattern, site.member(site.forward))
		}
	}
	return site
}

func (s *web) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Security-Policy", contentPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	if !s.hosts[r.Host] {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForeignHost, Message: "this listener answers only to its own loopback names"})
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForeignOrigin, Message: "this listener answers only its own pages"})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		s.serveAsset(w, r)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeForeignOrigin, Message: "this listener answers only its own pages"})
		return
	}
	header.Set("Cache-Control", "no-store")
	s.routes.ServeHTTP(w, r)
}

func csrfToken(session string) string {
	digest := hmac.New(sha256.New, []byte(session))
	digest.Write([]byte(csrfLabel))
	return hex.EncodeToString(digest.Sum(nil))
}

func changes(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

func jsonBody(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// member admits a request that carries a live session in its cookie, and for a change the token only
// a page of this listener can read. The header a client on the socket uses counts for nothing here,
// because anything can set a header and a browser guards only a cookie.
func (s *web) member(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(api.CookieSession)
		token := ""
		if err == nil {
			token = cookie.Value
		}
		if _, err := s.sessions.Member(r.Context(), s.scope, token); err != nil {
			fail(w, err)
			return
		}
		if changes(r.Method) {
			if !jsonBody(r) {
				refuse(w, http.StatusUnsupportedMediaType, api.ErrorDetail{Code: api.CodeMediaType, Message: "this listener reads JSON"})
				return
			}
			if !hmac.Equal([]byte(r.Header.Get(api.HeaderCSRF)), []byte(csrfToken(token))) {
				refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeCSRF, Message: "this needs the token from this listener's own page"})
				return
			}
		}
		next(w, r, token)
	}
}

// forward hands an admitted request to the API, as the session its cookie holds.
func (s *web) forward(w http.ResponseWriter, r *http.Request, token string) {
	inner := r.Clone(r.Context())
	inner.Header.Del("Authorization")
	inner.Header.Set(api.HeaderSession, token)
	s.api.ServeHTTP(w, inner)
}

// stream forwards a request that outlives the check that admitted it, so it asks again while it runs.
func (s *web) stream(w http.ResponseWriter, r *http.Request, token string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		ticker := time.NewTicker(s.recheck)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.sessions.Member(ctx, s.scope, token); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	s.forward(w, r.WithContext(ctx), token)
}

// sessionCookies sets a life in seconds from now and no date, because a browser's clock is not the
// ledger's, and the ledger decides when a session ends either way.
func sessionCookies(session, csrf string, maxAge int) []*http.Cookie {
	return []*http.Cookie{
		{Name: api.CookieSession, Value: session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge},
		{Name: api.CookieCSRF, Value: csrf, Path: "/", SameSite: http.SameSiteStrictMode, MaxAge: maxAge},
	}
}

// openSession signs a person in. The token goes into a cookie no page can read, so the answer a page
// sees holds no secret. A form can cross origins with no preflight, and JSON cannot.
func (s *web) openSession(w http.ResponseWriter, r *http.Request) {
	if !jsonBody(r) {
		refuse(w, http.StatusUnsupportedMediaType, api.ErrorDetail{Code: api.CodeMediaType, Message: "this listener reads JSON"})
		return
	}
	var in api.SignIn
	if !decode(w, r, &in) {
		return
	}
	select {
	case s.signIn <- struct{}{}:
		defer func() { <-s.signIn }()
	case <-r.Context().Done():
		return
	}
	grant, err := s.sessions.SignIn(r.Context(), s.scope, ports.Proof{Passphrase: in.Passphrase}, in.TTLMs)
	if err != nil {
		fail(w, err)
		return
	}
	lifeSeconds := int(authority.BoundTTL(in.TTLMs) / time.Second.Milliseconds())
	for _, cookie := range sessionCookies(grant.Token, csrfToken(grant.Token), lifeSeconds) {
		http.SetCookie(w, cookie)
	}
	reply(w, http.StatusCreated, api.Session{Member: grant.Member, ExpiresAt: grant.ExpiresAt})
}

// closeSession ends a session in the ledger, so its cookie opens nothing even if a browser keeps it.
func (s *web) closeSession(w http.ResponseWriter, r *http.Request, token string) {
	for _, cookie := range sessionCookies("", "", -1) {
		http.SetCookie(w, cookie)
	}
	s.forward(w, r, token)
}

// serveAsset serves the application. A path with no file is a view of the application's own, which
// reads its route when it loads, and a name with an extension that holds nothing is simply missing.
func (s *web) serveAsset(w http.ResponseWriter, r *http.Request) {
	if changes(r.Method) {
		w.Header().Set("Allow", "GET, HEAD")
		refuse(w, http.StatusMethodNotAllowed, api.ErrorDetail{Code: api.CodeInvalid, Message: "this listener serves its files to read"})
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || (!strings.HasPrefix(name, strings.TrimSuffix(assetDirectory, "/")) && path.Ext(name) == "") {
		name = indexFile
	}
	raw, served, err := s.readAsset(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	header := w.Header()
	if mediaType, known := mediaTypes[path.Ext(served)]; known {
		header.Set("Content-Type", mediaType)
	}
	header.Set("Cache-Control", "no-cache")
	if strings.HasPrefix(served, assetDirectory) {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(raw))
}

// readAsset reads one file, and the page a tree that was never built has instead of its index.
func (s *web) readAsset(name string) ([]byte, string, error) {
	if !fs.ValidPath(name) {
		return nil, "", fs.ErrInvalid
	}
	raw, err := fs.ReadFile(s.assets, name)
	if err != nil && name == indexFile {
		name = placeholderFile
		raw, err = fs.ReadFile(s.assets, name)
	}
	return raw, name, err
}
