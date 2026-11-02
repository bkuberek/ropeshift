package api

import "net/http"

// The web listener's two cookies. The session cannot be read by a page, and the token that proves a
// request came from one can.
const (
	CookieSession = "ropeshift_session"
	CookieCSRF    = "ropeshift_csrf"
	// HeaderCSRF carries that token. A page on another origin cannot read the cookie, so it cannot
	// send this, and a page cannot send it across origins with no preflight, which nothing answers.
	HeaderCSRF = "Ropeshift-Csrf"
)

// The refusals only the web listener makes.
const (
	CodeForeignHost   = "foreign_host"
	CodeForeignOrigin = "foreign_origin"
	CodeCSRF          = "csrf"
	CodeMediaType     = "media_type"
)

// WebRoute is one operation the web listener serves, and the command that does the same from a
// terminal. The list is closed, so the UI can reach nothing else, and a test holds every row to a
// command, so the UI can do nothing the CLI cannot (FR-UI-4).
type WebRoute struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
	Command string `json:"command"`
}

// WebRoutes is everything the web listener serves of the API. A worker's routes stay on the socket,
// and so does setting a passphrase: the first needs no session, and any local process can reach this
// listener.
func WebRoutes() []WebRoute {
	routes := []WebRoute{
		{http.MethodPost, SignInPath, "login"},
		{http.MethodPost, SignOutPath, "sign-out"},
		{http.MethodGet, EventsPath, "events"},
		{http.MethodPost, "/v1/questions/{id}/answer", "question answer"},
		{http.MethodPost, "/v1/approvals/{id}/decide", "approval approve"},
		{http.MethodPost, "/v1/effects/{id}/decide", "effect decide"},
		{http.MethodPost, "/v1/work-orders/{id}/accept", "work accept"},
		{http.MethodPost, "/v1/work-orders/{id}/rework", "work rework"},
		{http.MethodPost, "/v1/work-orders/{id}/reassign", "work reassign"},
		{http.MethodPost, "/v1/work-orders/{id}/resolve", "work resolve"},
		{http.MethodPost, "/v1/work-orders/{id}/replan", "work replan"},
		{http.MethodPost, "/v1/work-orders/{id}/lane-reset", "lane reset"},
		{http.MethodPost, "/v1/work-orders/{id}/lane-respawn", "lane respawn"},
		{http.MethodPost, "/v1/work-orders/{id}/lane-resume", "lane resume"},
	}
	for _, name := range Views {
		routes = append(routes, WebRoute{http.MethodGet, ViewPath(name), "view " + name})
	}
	return routes
}
