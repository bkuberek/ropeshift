package cli_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/test/apitest"
)

// creationAPI is a daemon's plan and approval routes with canned answers: it serves a plan's versions, the approval
// of each with the registration it recorded, and the retry and skip of an entry, and keeps every request it was asked.
type creationAPI struct {
	t      *testing.T
	socket string
	// versions lists the plan's versions newest first, and registrations holds what each approved version recorded.
	versions      []api.PlanVersionLine
	registrations map[int64][]api.RepositoryAdded
	// decided is the answer to a retry or a skip, and refusal replaces it with an error when set.
	decided api.ApprovalDecided
	refusal *api.ErrorDetail
	// shown is the request that waits, which a read of the plan with no version names answers with.
	shown *api.PlanApprovalView

	mu       sync.Mutex
	requests []string
	bodies   map[string]string
}

func newCreationAPI(t *testing.T) *creationAPI {
	t.Helper()
	a := &creationAPI{t: t, registrations: map[int64][]api.RepositoryAdded{}, bodies: map[string]string{}}
	a.socket = filepath.Join(apitest.ShortDir(t), "plan.sock")
	listener, err := net.Listen("unix", a.socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return a
}

func (a *creationAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body bytes.Buffer
	_, _ = body.ReadFrom(r.Body)
	line := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		line += "?" + r.URL.RawQuery
	}
	a.mu.Lock()
	a.requests = append(a.requests, line)
	a.bodies[line] = body.String()
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/tt/plans/p1":
		view := api.PlanView{Plan: planning.Plan{ID: "p1", Title: "Plan"}, Versions: a.versions}
		if r.URL.Query().Get("version") == "" && a.shown != nil {
			view.Approval = a.shown
			_ = json.NewEncoder(w).Encode(view)
			return
		}
		wanted, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		for _, line := range a.versions {
			if (wanted == 0 && line.State == authority.ApprovalApproved) || line.Version == wanted {
				if line.Approval == "" {
					continue
				}
				view.Approval = &api.PlanApprovalView{
					Approval: authority.Approval{ID: line.Approval, Kind: authority.KindPlan, State: line.State, PlanVersion: line.Version},
				}
				if registration, ok := a.registrations[line.Version]; ok {
					view.Approval.Registration = &api.ManifestRegistration{Repositories: registration}
				}
				break
			}
		}
		_ = json.NewEncoder(w).Encode(view)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/decide"):
		_ = json.NewEncoder(w).Encode(a.decided)
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/repositories/"):
		if a.refusal != nil {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.ErrorBody{Error: *a.refusal})
			return
		}
		_ = json.NewEncoder(w).Encode(a.decided)
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeNotFound, Message: "no such route " + line}})
	}
}

// posted lists the requests that changed something.
func (a *creationAPI) posted() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, line := range a.requests {
		if strings.HasPrefix(line, "POST ") {
			out = append(out, line)
		}
	}
	return out
}

func (a *creationAPI) run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(append(args, "--project", "tt"), &stdout, &stderr, cli.Options{Socket: a.socket})
	return code, stdout.String(), stderr.String()
}

// planWithAFailedCreation is a plan whose version 3 registered web, whose version 2 left web unbuilt after a refusal, and whose
// version 1 left it unbuilt after a failed push, with api registered by every version.
func planWithAFailedCreation(t *testing.T) *creationAPI {
	t.Helper()
	a := newCreationAPI(t)
	a.versions = []api.PlanVersionLine{
		{Version: 4, Approval: "ap4", State: authority.ApprovalPending},
		{Version: 3, Approval: "ap3", State: authority.ApprovalApproved},
		{Version: 2, Approval: "ap2", State: authority.ApprovalApproved},
		{Version: 1, Approval: "ap1", State: authority.ApprovalApproved},
	}
	entry := func(outcome ledger.RepositoryOutcome, reason string) []api.RepositoryAdded {
		return []api.RepositoryAdded{
			{Name: "api", ManifestPath: "repos/api", Path: "/work/tt/repos/api", Outcome: ledger.Registered},
			{Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Outcome: outcome, Reason: reason},
		}
	}
	a.registrations[3] = entry(ledger.Registered, "")
	a.registrations[2] = entry(ledger.RegistrationRefused, "gh is signed out")
	a.registrations[1] = entry(ledger.CreateFailed, "push failed")
	a.decided = api.ApprovalDecided{
		Approval:     authority.Approval{ID: "ap2", Kind: authority.KindPlan, State: authority.ApprovalApproved, PlanVersion: 2},
		Repositories: []api.RepositoryAdded{{Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Remote: "git@github.com:acme/web.git", Outcome: ledger.Registered}},
	}
	return a
}

// Retry with no version goes to the newest approved version whose approval holds the entry uncreated, skipping a
// version that registered it and a version nobody approved yet, and prints the entry's new line.
func TestPlanRetryRunsTheNewestApprovedVersionThatHoldsTheEntryUncreated(t *testing.T) {
	a := planWithAFailedCreation(t)
	code, stdout, stderr := a.run("plan", "retry", "p1", "web")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if got := a.posted(); len(got) != 1 || got[0] != "POST /v1/projects/tt/approvals/ap2/repositories/web/retry" {
		t.Errorf("posted %v", got)
	}
	if want := "registered\tweb\trepos/web\t/work/tt/repos/web\tgit@github.com:acme/web.git\n"; stdout != want {
		t.Errorf("printed %q, want %q", stdout, want)
	}
}

// --version names the version, and its approval must be approved.
func TestPlanRetryTakesTheVersionItIsGiven(t *testing.T) {
	a := planWithAFailedCreation(t)
	if code, _, stderr := a.run("plan", "retry", "p1", "web", "--version", "1"); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := a.posted(); len(got) != 1 || got[0] != "POST /v1/projects/tt/approvals/ap1/repositories/web/retry" {
		t.Errorf("posted %v", got)
	}
	a = planWithAFailedCreation(t)
	code, _, stderr := a.run("plan", "retry", "p1", "web", "--version", "4")
	if code != cli.ExitConflict || !strings.Contains(stderr, "version 4") || !strings.Contains(stderr, "approved") {
		t.Errorf("retrying a version nobody approved: exit %d, %s", code, stderr)
	}
	if got := a.posted(); len(got) != 0 {
		t.Errorf("posted %v", got)
	}
}

// A repository no approved version holds uncreated is a conflict the command names, and calls nothing.
func TestPlanRetryRefusesARepositoryNoApprovedVersionHoldsUncreated(t *testing.T) {
	a := planWithAFailedCreation(t)
	code, _, stderr := a.run("plan", "retry", "p1", "api")
	if code != cli.ExitConflict || !strings.Contains(stderr, "api") || !strings.Contains(stderr, "uncreated") {
		t.Errorf("exit %d, %s", code, stderr)
	}
	if got := a.posted(); len(got) != 0 {
		t.Errorf("posted %v", got)
	}
}

// What the daemon refuses is printed and exits as a conflict, as a member-only refusal does as forbidden.
func TestPlanRetryAndSkipReportWhatTheDaemonRefuses(t *testing.T) {
	a := planWithAFailedCreation(t)
	a.refusal = &api.ErrorDetail{Code: api.CodeIllegalMove, Message: "web is being created now"}
	if code, _, stderr := a.run("plan", "retry", "p1", "web"); code != cli.ExitConflict || !strings.Contains(stderr, "web is being created now") {
		t.Errorf("retry: exit %d, %s", code, stderr)
	}
	a.refusal = &api.ErrorDetail{Code: api.CodeMemberOnly, Message: "only a member does this"}
	if code, _, stderr := a.run("plan", "skip", "p1", "web", "--reason", "built by hand"); code != cli.ExitForbidden || !strings.Contains(stderr, "only a member does this") {
		t.Errorf("skip: exit %d, %s", code, stderr)
	}
}

// Skip names a reason of one to 2000 characters, and sends it with the entry's route.
func TestPlanSkipSendsItsReasonAndPrintsTheEntrysLine(t *testing.T) {
	a := planWithAFailedCreation(t)
	a.decided.Repositories = []api.RepositoryAdded{{Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Outcome: ledger.Skipped, Reason: "built by hand"}}
	code, stdout, stderr := a.run("plan", "skip", "p1", "web", "--reason", "built by hand")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	const route = "POST /v1/projects/tt/approvals/ap2/repositories/web/skip"
	if got := a.posted(); len(got) != 1 || got[0] != route {
		t.Errorf("posted %v", got)
	}
	var sent api.SkipRepository
	if err := json.Unmarshal([]byte(a.bodies[route]), &sent); err != nil || sent.Reason != "built by hand" {
		t.Errorf("sent %q: %v", a.bodies[route], err)
	}
	if !strings.HasPrefix(stdout, "skipped\tweb\trepos/web\t/work/tt/repos/web\t") || !strings.Contains(stdout, "built by hand") || strings.Count(stdout, "\n") != 1 {
		t.Errorf("printed %q", stdout)
	}
}

func TestPlanSkipNeedsAReasonOfOneToTwoThousandCharacters(t *testing.T) {
	a := planWithAFailedCreation(t)
	for name, args := range map[string][]string{
		"none":      {"plan", "skip", "p1", "web"},
		"empty":     {"plan", "skip", "p1", "web", "--reason", ""},
		"too long":  {"plan", "skip", "p1", "web", "--reason", strings.Repeat("x", 2001)},
		"no plan":   {"plan", "skip", "--reason", "r"},
		"no entry":  {"plan", "skip", "p1", "--reason", "r"},
		"one extra": {"plan", "skip", "p1", "web", "more", "--reason", "r"},
	} {
		if code, _, _ := a.run(args...); code != cli.ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	if code, _, _ := a.run("plan", "skip", "p1", "web", "--reason", strings.Repeat("x", 2000)); code != cli.ExitOK {
		t.Errorf("a reason of 2000 characters: exit %d", code)
	}
	if got := a.posted(); len(got) != 1 {
		t.Errorf("posted %v", got)
	}
}

func TestPlanRetryNeedsAPlanAndARepository(t *testing.T) {
	a := planWithAFailedCreation(t)
	for name, args := range map[string][]string{
		"none":      {"plan", "retry"},
		"no entry":  {"plan", "retry", "p1"},
		"one extra": {"plan", "retry", "p1", "web", "more"},
	} {
		if code, _, _ := a.run(args...); code != cli.ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	if got := a.requests; len(got) != 0 {
		t.Errorf("asked %v", got)
	}
}

// With --json both print the decided document and nothing else.
func TestPlanRetryAndSkipPrintTheDecidedDocumentAsJSON(t *testing.T) {
	a := planWithAFailedCreation(t)
	for _, args := range [][]string{{"plan", "retry", "p1", "web", "--json"}, {"plan", "skip", "p1", "web", "--reason", "r", "--json"}} {
		code, stdout, stderr := a.run(args...)
		var decided api.ApprovalDecided
		if err := json.Unmarshal([]byte(stdout), &decided); err != nil || code != cli.ExitOK || stderr != "" {
			t.Fatalf("%v: exit %d, %q, %v", args, code, stdout, err)
		}
		if decided.ID != "ap2" || len(decided.Repositories) != 1 || decided.Repositories[0].Name != "web" {
			t.Errorf("%v printed %+v", args, decided)
		}
	}
}

// A refused entry's line carries why it is refused, as the approval's own lines do, and a problem the run could not get
// past is printed after it.
func TestPlanRetryPrintsWhyTheEntryIsStillRefused(t *testing.T) {
	a := planWithAFailedCreation(t)
	a.decided.Repositories = []api.RepositoryAdded{{Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Outcome: ledger.RegistrationRefused, Reason: "gh is signed out"}}
	a.decided.RepositoriesProblem = "registration stopped"
	code, stdout, _ := a.run("plan", "retry", "p1", "web")
	if code != cli.ExitOK || !strings.HasPrefix(stdout, "refused\tweb\trepos/web\t/work/tt/repos/web\t") || !strings.Contains(stdout, "gh is signed out") || !strings.Contains(stdout, "registration stopped") {
		t.Errorf("exit %d, printed %q", code, stdout)
	}
}

// creationViews are the repositories of a version whose manifest creates api, plans web, and has docs refused, as a member
// reads them before approving.
func creationPreview() []api.RepositoryAdded {
	return []api.RepositoryAdded{
		{
			Name: "api", ManifestPath: "repos/api", Path: "/work/tt/repos/api", Outcome: ledger.Created,
			Creation: &ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate, DefaultBranch: "main", URL: "git@github.com:acme/api.git", Login: "octo"},
		},
		{Name: "web", ManifestPath: "repos/web", Outcome: ledger.Planned, Reason: "no work order of this version targets web"},
		{Name: "docs", ManifestPath: "repos/docs", Path: "/work/tt/repos/docs", Outcome: ledger.RegistrationRefused, Reason: "gh is signed out: run `gh auth login --hostname github.com` on this machine"},
	}
}

// plan show prints each repository approving would create with the repository it creates, and says of one it plans that
// approving creates nothing.
func TestPlanShowPrintsWhatApprovingWouldCreate(t *testing.T) {
	a := newCreationAPI(t)
	a.shown = &api.PlanApprovalView{
		Approval:     authority.Approval{ID: "ap2", Kind: authority.KindPlan, State: authority.ApprovalPending, PlanVersion: 2, Plan: "tt-v2"},
		Registration: &api.ManifestRegistration{Preview: true, Repositories: creationPreview()},
	}
	code, stdout, stderr := a.run("plan", "show", "p1")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	want := "  repositories approving would register, read now on the daemon's worker's machine:\n" +
		"    would create\tapi\trepos/api\t/work/tt/repos/api\tgit@github.com:acme/api.git\n" +
		"      creates github.com/acme/api, private, on branch main, with gh signed in as octo\n" +
		"    planned\tweb\trepos/web\t-\t-\tno work order of this version targets web, so approving creates nothing; the approval of a version that gives it work creates it\n" +
		"    refused\tdocs\trepos/docs\t/work/tt/repos/docs\t-\tgh is signed out: run `gh auth login --hostname github.com` on this machine\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("plan show printed\n%s\nwant a block\n%s", stdout, want)
	}
}

// A registered approval prints what it created and how: the first commit, each step, and the line to add to the Makefile; and
// a creation that failed says where it stopped, what is left to run, and the command that retries it.
func TestPlanShowPrintsWhatAnApprovalCreatedAndWhatFailed(t *testing.T) {
	a := newCreationAPI(t)
	created := &ledger.CreationOutcome{
		Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate, DefaultBranch: "main", URL: "git@github.com:acme/api.git",
		Commit: "1a2b3c4d5e6f", Steps: []ledger.RemoteStep{{Step: "create", Outcome: "done"}, {Step: "origin", Outcome: "done"}, {Step: "push", Outcome: "done"}},
		Makefile: "api=git@github.com:acme/api.git=repos/api",
	}
	failed := &ledger.CreationOutcome{
		Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "web", Visibility: ledger.VisibilityPrivate, DefaultBranch: "main", URL: "git@github.com:acme/web.git",
		Commit: "5d6e7f8a9b0c", Steps: []ledger.RemoteStep{{Step: "create", Outcome: "done"}, {Step: "origin", Outcome: "done"}, {Step: "push", Outcome: "failed", Detail: "remote\x1b[31m rejected"}},
		Remaining: []string{"git -C /work/tt/repos/web push --set-upstream origin main"},
	}
	a.shown = &api.PlanApprovalView{
		Approval: authority.Approval{ID: "ap2", Kind: authority.KindPlan, State: authority.ApprovalApproved, PlanVersion: 2, Plan: "tt-v2"},
		Registration: &api.ManifestRegistration{Repositories: []api.RepositoryAdded{
			{Name: "api", ManifestPath: "repos/api", Path: "/work/tt/repos/api", Outcome: ledger.Created, Creation: created},
			{Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Outcome: ledger.CreateFailed, Creation: failed},
		}},
	}
	code, stdout, stderr := a.run("plan", "show", "p1")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	want := "  repositories this approval registered:\n" +
		"    created\tapi\trepos/api\t/work/tt/repos/api\tgit@github.com:acme/api.git\n" +
		"      created github.com/acme/api, private, on branch main: first commit 1a2b3c4, create done, origin done, push done\n" +
		"      add to REPOSITORIES in the workspace Makefile: api=git@github.com:acme/api.git=repos/api\n" +
		"    create failed\tweb\trepos/web\t/work/tt/repos/web\tgit@github.com:acme/web.git\n" +
		"      first commit 5d6e7f8, create done, origin done, push failed: remote rejected\n" +
		"      remaining: git -C /work/tt/repos/web push --set-upstream origin main\n" +
		"      retry with: ropeshift plan retry tt-v2 web\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("plan show printed\n%s\nwant a block\n%s", stdout, want)
	}
	if strings.Contains(stdout, "\x1b") {
		t.Errorf("plan show printed an escape sequence: %q", stdout)
	}
}

// plan approve prints the preview before it decides, and what the approval created after, as plan show prints them.
func TestPlanApprovePrintsWhatItCreated(t *testing.T) {
	a := newCreationAPI(t)
	a.shown = &api.PlanApprovalView{
		Approval:     authority.Approval{ID: "ap2", Kind: authority.KindPlan, State: authority.ApprovalPending, PlanVersion: 2, Plan: "tt-v2", SubjectDigest: "sha256:v2"},
		Registration: &api.ManifestRegistration{Preview: true, Repositories: creationPreview()[:1]},
	}
	a.decided = api.ApprovalDecided{
		Approval: authority.Approval{ID: "ap2", Kind: authority.KindPlan, State: authority.ApprovalApproved, PlanVersion: 2, Plan: "tt-v2"},
		Repositories: []api.RepositoryAdded{{
			Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Outcome: ledger.CreateFailed,
			Creation: &ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "web", Visibility: ledger.VisibilityPublic, URL: "git@github.com:acme/web.git", Steps: []ledger.RemoteStep{{Step: "create", Outcome: "failed", Detail: "name taken"}}},
		}},
	}
	code, stdout, stderr := a.run("plan", "approve", "p1")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"    would create\tapi\trepos/api\t/work/tt/repos/api\tgit@github.com:acme/api.git\n      creates github.com/acme/api, private, on branch main, with gh signed in as octo\n",
		"create failed\tweb\trepos/web\t/work/tt/repos/web\tgit@github.com:acme/web.git\n  create failed: name taken\n  retry with: ropeshift plan retry tt-v2 web\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan approve printed\n%s\nwant a block\n%s", stdout, want)
		}
	}
}

// retry and skip print the entry's new line with what the creation did.
func TestPlanRetryPrintsWhatTheCreationDid(t *testing.T) {
	a := planWithAFailedCreation(t)
	a.decided.Plan = "tt-v2"
	a.decided.Repositories = []api.RepositoryAdded{{
		Name: "web", ManifestPath: "repos/web", Path: "/work/tt/repos/web", Outcome: ledger.Created,
		Creation: &ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "web", Visibility: ledger.VisibilityPrivate, DefaultBranch: "main", URL: "git@github.com:acme/web.git", Commit: "5d6e7f8a9b0c", Steps: []ledger.RemoteStep{{Step: "push", Outcome: "done"}}},
	}}
	code, stdout, _ := a.run("plan", "retry", "p1", "web")
	want := "created\tweb\trepos/web\t/work/tt/repos/web\tgit@github.com:acme/web.git\n  created github.com/acme/web, private, on branch main: first commit 5d6e7f8, push done\n"
	if code != cli.ExitOK || stdout != want {
		t.Errorf("exit %d, printed %q, want %q", code, stdout, want)
	}
}
