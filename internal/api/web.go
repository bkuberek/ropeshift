package api

import (
	"net/http"
	"strings"
)

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
		{http.MethodGet, ProjectsPath, "project list"},
		{http.MethodGet, NeedsYouPath, "needs"},
		{http.MethodPut, "/v1/workers/{id}/orchestrator", "orchestrator start"},
		{http.MethodGet, "/v1/workers/{id}/orchestrator", "orchestrator show"},
		{http.MethodPost, ProjectsPath, "project create"},
		{http.MethodPost, ProjectsPath + "/{project}/rename", "project rename"},
		{http.MethodPost, ProjectsPath + "/{project}/archive", "project archive"},
		{http.MethodPost, ProjectsPath + "/{project}/restore", "project restore"},
		{http.MethodPost, ProjectsPath + "/{project}/remove", "project remove"},
		{http.MethodPost, ImportUploadPath, "project import"},
		{http.MethodPost, InstallationBackupDownloadPath, "backup"},
		{http.MethodGet, "/v1/workers", "worker list"},
		{http.MethodPost, "/v1/workers/tokens", "worker token"},
		{http.MethodGet, "/v1/workers/{id}/autonomy", "worker autonomy"},
		{http.MethodPut, "/v1/workers/{id}/autonomy", "worker level"},
		{http.MethodPost, "/v1/workers/{id}/autonomy/acknowledgement", "worker acknowledge-risk"},
		{http.MethodPost, "/v1/workers/{id}/windows", "worker window-open"},
		{http.MethodDelete, "/v1/workers/{id}/windows", "worker window-close"},
		{http.MethodGet, RiskPath, "worker risk"},
		{http.MethodGet, MembersPath, "member list"},
		{http.MethodGet, AuditPath, "audit"},
	}
	// The roster's routes read and change the installation's layer at the top, and a project's under it.
	roster := []WebRoute{
		{http.MethodGet, "/v1/roster", "agent list"},
		{http.MethodGet, "/v1/roster/history", "agent history"},
		{http.MethodPost, "/v1/roster/changes", "agent set"},
		{http.MethodPost, "/v1/roster/revert", "agent revert"},
	}
	routes = append(routes, roster...)
	inProject := []WebRoute{
		{http.MethodGet, EventsPath, "events"},
		{http.MethodGet, "/v1/documents/read", "document show"},
		{http.MethodGet, "/v1/documents/versions", "document versions"},
		{http.MethodGet, "/v1/documents/recorded", "document recorded"},
		{http.MethodPost, "/v1/questions/{id}/answer", "question answer"},
		{http.MethodPost, "/v1/approvals/{id}/decide", "approval approve"},
		{http.MethodPost, "/v1/effects/{id}/decide", "effect decide"},
		{http.MethodPost, "/v1/work-orders/{id}/review", "work review"},
		{http.MethodPost, "/v1/work-orders/{id}/send-back", "work send-back"},
		{http.MethodPost, "/v1/work-orders/{id}/accept", "work accept"},
		{http.MethodPost, "/v1/work-orders/{id}/rework", "work rework"},
		{http.MethodPost, "/v1/work-orders/{id}/reassign", "work reassign"},
		{http.MethodPost, "/v1/work-orders/{id}/resolve", "work resolve"},
		{http.MethodPost, "/v1/work-orders/{id}/replan", "work replan"},
		{http.MethodPost, "/v1/work-orders/{id}/cancel", "work cancel"},
		{http.MethodPost, "/v1/work-orders/{id}/lane-reset", "lane reset"},
		{http.MethodPost, "/v1/work-orders/{id}/lane-respawn", "lane respawn"},
		{http.MethodPost, "/v1/work-orders/{id}/lane-resume", "lane resume"},
		{http.MethodGet, PanePath, "lane attach"},
		{http.MethodPost, PaneInputPath, "lane attach"},
		{http.MethodPost, BackupDownloadPath, "backup"},
		{http.MethodGet, AuditPath, "audit"},
		{http.MethodGet, "/v1/repositories", "repository list"},
		{http.MethodGet, "/v1/repositories/{id}", "repository show"},
		{http.MethodPatch, "/v1/repositories/{id}", "repository set"},
		{http.MethodPost, "/v1/repositories", "repository add"},
		{http.MethodPost, "/v1/repositories/{id}/remove", "repository remove"},
		{http.MethodGet, "/v1/fleet", "fleet show"},
		{http.MethodPut, "/v1/fleet/mode", "fleet pause"},
		{http.MethodPut, "/v1/fleet/caps", "fleet caps"},
		{http.MethodGet, "/v1/policies", "policy list"},
		{http.MethodGet, "/v1/policies/{version}", "policy show"},
		{http.MethodPost, "/v1/policies", "policy publish"},
		{http.MethodGet, "/v1/categories", "category list"},
		{http.MethodGet, "/v1/approvals", "approval list"},
		{http.MethodGet, "/v1/plans", "plan list"},
		{http.MethodGet, "/v1/plans/{id}", "plan show"},
		{http.MethodGet, "/v1/approvals/{id}", "approval show"},
		{http.MethodGet, "/v1/sources", "source list"},
		{http.MethodPost, "/v1/work-orders", "work create"},
		{http.MethodPost, "/v1/work-orders/{id}/claims", "work claim"},
		{http.MethodPost, "/v1/work-orders/{id}/lanes", "lane start"},
	}
	inProject = append(inProject, roster...)
	for _, name := range Views {
		inProject = append(inProject, WebRoute{http.MethodGet, ViewPath(name), "view " + name})
	}
	for _, route := range inProject {
		route.Pattern = ProjectRoute(route.Pattern)
		routes = append(routes, route)
	}
	return routes
}

// ProjectRoute is the pattern a project route, as it reads under /v1, has under /v1/projects/{project}/.
func ProjectRoute(path string) string {
	return ProjectsPath + "/{project}" + strings.TrimPrefix(path, "/v1")
}
