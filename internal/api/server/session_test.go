package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

func refusedWith(t *testing.T, err error, status int, code string) {
	t.Helper()
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != status || refused.Detail.Code != code {
		t.Fatalf("got %v, want %d %s", err, status, code)
	}
}

// TestAHumanDecisionWithNoSessionIsRefused holds FR-AUTH-3: the socket admits the owning
// user, and a lane runs as that user, so reaching the socket proves nothing.
func TestAHumanDecisionWithNoSessionIsRefused(t *testing.T) {
	d := apitest.Start(t)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	var out questions.Decision
	err := anonymous.Do(t.Context(), http.MethodPost, apitest.Route("/v1/decisions"), api.RecordDecision{
		ID: "d1", Status: questions.DecisionDecided, Statement: "Use one port.",
	}, &out)
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusUnauthorized || refused.Detail.Code != api.CodeSessionRequired {
		t.Fatalf("got %v, want a refusal that asks for a session", err)
	}
	readErr := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		_, err := tx.Questions().Decision("d1")
		return err
	})
	if !errors.Is(readErr, ports.ErrNotFound) {
		t.Fatalf("the refused decision reads %v, want it absent", readErr)
	}
}

func TestASessionExpires(t *testing.T) {
	d := apitest.Start(t)
	client := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer client.Close()
	session, err := client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	client.UseSession(session.Token)
	time.Sleep(5 * time.Millisecond)
	err = client.Do(t.Context(), http.MethodPost, apitest.Route("/v1/categories"), api.AddCategory{Name: "pricing"}, nil)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

func TestALiveSessionNamesItsMember(t *testing.T) {
	d := apitest.Start(t)
	client := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer client.Close()
	session, err := client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	client.UseSession(session.Token)
	var out questions.Decision
	err = client.Do(t.Context(), http.MethodPost, apitest.Route("/v1/decisions"), api.RecordDecision{
		ID: "d1", Status: questions.DecisionDecided, Statement: "Use one port.",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.DecidedBy != session.Member || session.Member == "" {
		t.Fatalf("decided by %q under %q", out.DecidedBy, session.Member)
	}
}

func TestASessionCannotOutliveALogin(t *testing.T) {
	d := apitest.Start(t)
	client := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer client.Close()
	before := time.Now().UnixMilli()
	session, err := client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: 24 * authority.LoginTTLMs})
	if err != nil {
		t.Fatal(err)
	}
	if limit := before + authority.LoginTTLMs + 60_000; session.ExpiresAt > limit {
		t.Fatalf("expires at %d, past %d", session.ExpiresAt, limit)
	}
}

// TestAWrongPassphraseOpensNothingAndStaysVisible holds what the design promises in solo
// mode: an agent can try, and a try is on record with no trace of what it tried.
func TestAWrongPassphraseOpensNothingAndStaysVisible(t *testing.T) {
	d := apitest.Start(t)
	client := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer client.Close()
	const guess = "a guess at the passphrase"
	_, err := client.SignIn(t.Context(), api.SignIn{Passphrase: guess})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadPassphrase)
	var seen bool
	readErr := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		events, err := tx.Events(0, 200)
		for _, event := range events {
			seen = seen || event.Type == "member.sign_in_refused"
			if strings.Contains(string(event.Data), guess) {
				t.Errorf("event %s holds the guess", event.Type)
			}
		}
		return err
	})
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !seen {
		t.Fatal("the refused sign-in left no event")
	}
}

func TestNobodySignsInBeforeAPassphraseIsSet(t *testing.T) {
	d := apitest.StartWith(t, apitest.Options{NoMember: true})
	_, err := d.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeNotEnrolled)
	if err := d.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: "short"}); err == nil {
		t.Fatal("a short passphrase was taken")
	}
	if err := d.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: apitest.Passphrase}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase}); err != nil {
		t.Fatal(err)
	}
	err = d.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: "another passphrase altogether"})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

// TestTheLedgerHoldsATokenOnlyAsADigest reads what a session leaves behind.
func TestTheLedgerHoldsATokenOnlyAsADigest(t *testing.T) {
	d := apitest.Start(t)
	sum := sha256.Sum256([]byte(d.Session))
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		if _, err := tx.Members().Session(hex.EncodeToString(sum[:])); err != nil {
			return err
		}
		if _, err := tx.Members().Session(d.Session); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("a token reads as a row: %v", err)
		}
		events, err := tx.Events(0, 200)
		for _, event := range events {
			if strings.Contains(string(event.Data), d.Session) || strings.Contains(string(event.Data), hex.EncodeToString(sum[:])) {
				t.Errorf("event %s holds the session", event.Type)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// sessionless are the changing routes a member's session does not guard: what a worker's
// credential proves, and the two a person uses before any session exists. A new route that
// changes something has to appear here or go through h.member, or this test fails.
var sessionless = map[string]string{
	"POST /v1/sessions":                              "signing in is how a session starts",
	"PUT /v1/passphrase":                             "the first has no session, and the provider guards the rest",
	"POST /v1/work-orders/{id}/claims":               "a worker's credential, or a session checked in the handler",
	"POST /v1/workers/register":                      "a join token",
	"POST /v1/workers/{id}/sessions":                 "a worker's credential",
	"POST /v1/workers/{id}/heartbeat":                "a worker's credential",
	"POST /v1/workers/{id}/events":                   "a worker's credential",
	"POST /v1/workers/{id}/claims":                   "a worker's credential",
	"POST /v1/workers/{id}/ask":                      "a worker's credential",
	"POST /v1/workers/{id}/lane-facts":               "a worker's credential",
	"POST /v1/workers/{id}/lane-context":             "a worker's credential",
	"POST /v1/workers/{id}/rulings":                  "a worker's credential",
	"POST /v1/workers/{id}/approvals/use":            "a worker's credential",
	"POST /v1/workers/{id}/approvals/decide":         "a worker's credential, which reaches only what a member delegated",
	"POST /v1/workers/{id}/orchestrator/digest":      "a worker's credential, and it changes nothing",
	"POST /v1/workers/{id}/orchestrator/acts/{verb}": "a worker's credential, and only for the orchestrator a member started",
}

var routeLine = regexp.MustCompile(`mux\.HandleFunc\("((POST|PUT|PATCH|DELETE) ([^"]+))", (.+)\)$`)

// reached is where a route a handler registers is served: an installation route where it reads, and a
// project route under the harness's project.
func reached(path string) string {
	for _, prefix := range []string{"/v1/sessions", "/v1/passphrase", "/v1/workers", "/v1/autonomy", "/v1/telemetry", "/v1/health", "/v1/projects", "/v1/installation", "/v1/delegates", "/v1/operations"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return path
		}
	}
	return apitest.Route(path)
}

// TestEveryChangingRouteNeedsASession reads the routes from the source, so none can be
// left out, and calls each one with no session.
func TestEveryChangingRouteNeedsASession(t *testing.T) {
	d := apitest.Start(t)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(raw)) {
			route := routeLine.FindStringSubmatch(strings.TrimSpace(line))
			if route == nil {
				continue
			}
			pattern, method, path, target := route[1], route[2], route[3], route[4]
			if _, exempt := sessionless[pattern]; exempt {
				continue
			}
			if !strings.HasPrefix(target, "h.member(") {
				t.Errorf("%s reaches %s with no session check", pattern, target)
				continue
			}
			checked++
			concrete := strings.NewReplacer("{id}", "w1", "{claim}", "c1").Replace(path)
			err := anonymous.Do(t.Context(), method, reached(concrete), struct{}{}, nil)
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
		}
	}
	if checked < 15 {
		t.Fatalf("checked %d routes, so the source scan found too few", checked)
	}
}

// namedPaths are the changing routes the source spells through a constant.
var namedPaths = map[string]string{
	"api.InstallationBackupDownloadPath": api.InstallationBackupDownloadPath, "api.BackupDownloadPath": api.BackupDownloadPath,
	"api.ImportUploadPath": api.ImportUploadPath, "api.PaneInputPath": api.PaneInputPath,
}

var namedRouteLine = regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PUT|PATCH|DELETE) "\+(api\.\w+), (.+)\)$`)

var anyRouteLine = regexp.MustCompile(`mux\.HandleFunc\("((GET|POST|PUT|PATCH|DELETE) ([^"]+))", (.+)$`)

// sourceRoutes reads every route a handler registers, literal or through a constant, with what it reaches.
func sourceRoutes(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(raw)) {
			line = strings.TrimSpace(line)
			if route := anyRouteLine.FindStringSubmatch(line); route != nil {
				routes[route[1]] = route[4]
			}
			if route := namedRouteLine.FindStringSubmatch(line); route != nil {
				if path, ok := namedPaths[route[2]]; ok {
					routes[route[1]+" "+path] = route[3]
				}
			}
		}
	}
	return routes
}

// credentialed are the changing routes no member's or delegate's operation names: a worker's credential
// or a join token proves who calls them, and signing in is how a session starts.
func credentialed(pattern string) bool {
	reason, ok := sessionless[pattern]
	return ok && (strings.Contains(reason, "worker's credential") || strings.Contains(reason, "join token") || pattern == "POST /v1/sessions") &&
		pattern != "POST /v1/work-orders/{id}/claims"
}

// TestEveryChangingRouteNamesAnOperation holds the class table to the routes: a changing route that names
// no operation fails here until it does, because a delegate's call to it would read as unnamed consent.
func TestEveryChangingRouteNamesAnOperation(t *testing.T) {
	table := server.RouteOperations()
	checked := 0
	for pattern, target := range sourceRoutes(t) {
		if strings.HasPrefix(pattern, "GET ") {
			if strings.HasPrefix(target, "h.member(") {
				if _, ok := table[pattern]; !ok && pattern != "GET "+api.PanePath {
					t.Errorf("%s needs a session and names no operation", pattern)
				}
			}
			continue
		}
		if credentialed(pattern) {
			continue
		}
		checked++
		route, ok := table[pattern]
		if !ok {
			t.Errorf("%s names no operation", pattern)
			continue
		}
		for _, id := range route.IDs {
			if _, ok := authority.OperationOf(id); !ok {
				t.Errorf("%s names %s, which the class table lacks", pattern, id)
			}
		}
	}
	if checked < 55 {
		t.Fatalf("checked %d routes, so the source scan found too few", checked)
	}
}

// TestEveryOperationIsReachedByARoute holds the class table the other way: every operation it names is
// one some route runs, and every route the table names exists.
func TestEveryOperationIsReachedByARoute(t *testing.T) {
	routes := sourceRoutes(t)
	reachedBy := map[string]bool{}
	for pattern, route := range server.RouteOperations() {
		if _, ok := routes[pattern]; !ok {
			t.Errorf("the table names %s, which no handler registers", pattern)
		}
		for _, id := range route.IDs {
			reachedBy[id] = true
		}
	}
	for _, op := range authority.Operations() {
		if !reachedBy[op.ID] {
			t.Errorf("no route runs %s", op.ID)
		}
	}
}

func TestAMemberClaimNeedsASession(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	_, err := anonymous.Claim(t.Context(), "w1", api.ClaimRequest{ClaimID: "c1", WorkerID: "worker-a", TTLMs: 60_000})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

func TestAnAnswerNeedsAMemberSession(t *testing.T) {
	d := apitest.Start(t)
	if _, err := d.Client.RecordQuestion(t.Context(), api.RecordQuestion{ID: "q1", Text: "What is the retry budget?"}); err != nil {
		t.Fatal(err)
	}
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	_, err := anonymous.AnswerQuestion(t.Context(), "q1", api.AnswerQuestion{ID: "d1", Answer: "Two retries."})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	row, err := d.Client.Question(t.Context(), "q1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != questions.StatusOpen || row.DecisionID != "" {
		t.Fatalf("the refused answer moved the question: %+v", row)
	}
}

func TestAnAnsweredQuestionRefusesASecondAnswer(t *testing.T) {
	d := apitest.Start(t)
	if _, err := d.Client.RecordQuestion(t.Context(), api.RecordQuestion{ID: "q1", Text: "What is the retry budget?"}); err != nil {
		t.Fatal(err)
	}
	first, err := d.Client.AnswerQuestion(t.Context(), "q1", api.AnswerQuestion{ID: "d1", Answer: "Two retries."})
	if err != nil {
		t.Fatal(err)
	}
	if first.DecidedBy == "" || first.QuestionID != "q1" {
		t.Fatalf("decision %+v", first)
	}
	_, err = d.Client.AnswerQuestion(t.Context(), "q1", api.AnswerQuestion{ID: "d2", Answer: "Three retries."})
	refusedWith(t, err, http.StatusConflict, api.CodeResolved)
	_, err = d.Client.AnswerQuestion(t.Context(), "q1", api.AnswerQuestion{ID: "d3"})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}
