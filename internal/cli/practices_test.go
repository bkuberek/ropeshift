package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/apitest"
)

const (
	widgetsID = "repo-widgets"
	gadgetsID = "repo-gadgets"
	aTip      = "1111111111111111111111111111111111111111"
)

// practicesSession serves a project whose worker `plan` keeps a checkout of each repository named, with the two
// repositories widgets and gadgets held, and the CLI polling every few milliseconds.
func practicesSession(t *testing.T, options apitest.Options, keeps ...string) *session {
	t.Helper()
	restore := cli.PracticesTiming(5 * time.Millisecond)
	t.Cleanup(restore)
	options.AssistantHosts = func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		host := control.AssistantHost{Checkouts: map[string]bool{}}
		for _, id := range keeps {
			host.Checkouts[id] = true
		}
		return map[string]control.AssistantHost{"plan": host}, nil
	}
	options.LocalWorker = func(context.Context) (string, error) { return "plan", nil }
	s := newSessionWith(t, options)
	token, err := s.daemon.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.daemon.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: "plan", Name: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	worker := api.NewUnixClient(s.daemon.Socket).Project(apitest.ProjectName).WithCredential(credential.Credential)
	t.Cleanup(worker.Close)
	if _, err := worker.OpenSession(t.Context(), "plan", ledger.SessionRequest{Protocol: ledger.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	s.daemon.HoldClone(t, widgetsID, "widgets")
	s.daemon.HoldClone(t, gadgetsID, "gadgets")
	return s
}

// aCheckResult is what a check of a repository came to: an env finding and a leaked token, a practice of each other
// status, and the reasons given.
func aCheckResult(repository string, checked time.Time) practices.Result {
	return practices.Result{
		Repository: repository, Commit: aTip, BaseBranch: "main", Worker: "plan", CheckedAt: checked,
		TableVersion: practices.TableVersion, RulesVersion: ledger.CredentialRulesVersion,
		Practices: []practices.PracticeStatus{
			{ID: practices.EnvIgnored, Status: practices.StatusFinding},
			{ID: practices.Secrets, Status: practices.StatusFinding},
			{ID: practices.CI, Status: practices.StatusMet},
			{ID: practices.PreCommit, Status: practices.StatusMet},
			{ID: practices.Lockfile, Status: practices.StatusNotApplicable, Reason: "no package manager is detected"},
			{ID: practices.License, Status: practices.StatusUnknown, Reason: "the forge could not be read"},
		},
		Findings: []practices.Finding{
			{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"},
			{
				ID: "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f", Practice: practices.Secrets, Severity: practices.High, Detail: "a github_token credential",
				Rule: "github_token", Path: "config/dev.yaml", Line: 3, Commit: "aaaaaaaaaaaa", Digest: "1a2b3c4d5e6f1a2b3c4d5e6f",
			},
		},
	}
}

// keepsResult records a result as a worker's report leaves it, answering the request given.
func (s *session) keepsResult(request string, result practices.Result) {
	s.t.Helper()
	err := s.daemon.Store.Write(s.t.Context(), apitest.Scope, "test.practices", func(tx ports.WriteTx) error {
		if request != "" {
			if _, err := tx.Practices().Settle(request, tx.Now()); err != nil {
				return err
			}
		}
		err := tx.Practices().SaveResult(ledger.PracticesRecord{Repository: result.Repository, Request: request, Commit: result.Commit, Result: result, CheckedAt: result.CheckedAt.UnixMilli()})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: ledger.EventPracticesChecked, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: "plan"}, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request}})
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

// whenRequested runs what once a request of the repository is open, as a worker would on its next beat, and returns a
// function that waits for it to have run.
func (s *session) whenRequested(repository string, what func(request ledger.PracticesRequest)) (wait func()) {
	s.t.Helper()
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var open ledger.PracticesRequest
			err := s.daemon.Store.Read(context.Background(), apitest.Scope, func(tx ports.ReadTx) (err error) {
				now, err := tx.Now()
				if err != nil {
					return err
				}
				open, err = tx.Practices().Open(repository, now)
				return err
			})
			if err == nil {
				what(open)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return group.Wait
}

// Without --latest the command asks for the check, waits for each request to be reported, and prints the result it
// made: the header, each open finding with why and fix, the practices it could not settle, and the ones met. It exits 0
// though the findings are high.
func TestPracticesAsksForTheCheckWaitsForTheReportAndPrintsTheResult(t *testing.T) {
	s := practicesSession(t, apitest.Options{}, widgetsID)
	wait := s.whenRequested(widgetsID, func(request ledger.PracticesRequest) {
		s.keepsResult(request.ID, aCheckResult(widgetsID, time.Now().Add(-2*time.Minute)))
	})
	code, stdout, stderr := s.run("practices", "--repository", "widgets")
	wait()
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"widgets at 1111111 (base main), checked 2m ago on plan\n",
		"high  env-ignored:ignore:.\n",
		"  `.env` is not ignored\n",
		"  Why: An env file holds credentials",
		"  Fix: Add `.env`",
		"  Template: ropeshift template show repository/gitignore\n",
		"high  secrets:github_token:config/dev.yaml:1a2b3c4d5e6f\n",
		"  at config/dev.yaml:3 (github_token, commit aaaaaaaaaaaa)\n",
		"unknown: license: the forge could not be read\n",
		"not applicable: lockfile: no package manager is detected\n",
		"met: ci, pre-commit\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "not checked yet") || strings.Contains(stdout, "gadgets") {
		t.Errorf("the output names a repository that was not asked of or one with no result:\n%s", stdout)
	}
	if strings.Index(stdout, "env-ignored:ignore:.") > strings.Index(stdout, "unknown: license") {
		t.Errorf("the open findings come after the practices it could not settle:\n%s", stdout)
	}
}

// A request that nobody reports before it expires ends the wait, says so, and still prints the latest result of the
// repository, which here is none, so the line says why.
func TestPracticesStopsWaitingForARequestThatExpiredAndPrintsWhatItHas(t *testing.T) {
	clock := apitest.NewClock()
	s := practicesSession(t, apitest.Options{Clock: clock.Dialect}, widgetsID)
	wait := s.whenRequested(widgetsID, func(ledger.PracticesRequest) { clock.Advance(31 * time.Minute) })
	code, stdout, stderr := s.run("practices", "--repository", "widgets")
	wait()
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"the check on plan did not report within 30 minutes; a later report still becomes the latest result\n",
		"widgets not checked yet: no check has reported\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, stdout)
		}
	}
}

// An expired request does not hide an earlier result: it is printed as the latest one, under the note.
func TestPracticesPrintsTheEarlierResultWhenTheRequestExpired(t *testing.T) {
	clock := apitest.NewClock()
	s := practicesSession(t, apitest.Options{Clock: clock.Dialect}, widgetsID)
	s.keepsResult("request-earlier", aCheckResult(widgetsID, time.Now().Add(-3*time.Hour)))
	wait := s.whenRequested(widgetsID, func(ledger.PracticesRequest) { clock.Advance(31 * time.Minute) })
	_, stdout, _ := s.run("practices", "--repository", "widgets")
	wait()
	if !strings.Contains(stdout, "did not report within 30 minutes") || !strings.Contains(stdout, "widgets at 1111111 (base main), checked 3h ago on plan\n") {
		t.Errorf("the output is:\n%s", stdout)
	}
}

// A repository no connected worker keeps a checkout of is asked of with no request, and its line says why, beside the
// repository that did get a result.
func TestPracticesSaysWhyARepositoryWasNotChecked(t *testing.T) {
	s := practicesSession(t, apitest.Options{}, widgetsID)
	wait := s.whenRequested(widgetsID, func(request ledger.PracticesRequest) {
		s.keepsResult(request.ID, aCheckResult(widgetsID, time.Now()))
	})
	code, stdout, stderr := s.run("practices")
	wait()
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "gadgets not checked yet: no connected worker keeps a checkout of gadgets\n") ||
		!strings.Contains(stdout, "widgets at 1111111 (base main)") {
		t.Errorf("the output is:\n%s", stdout)
	}
}

// With --latest the command asks for no check: it prints what the project holds, a repository with no result as not
// checked yet, and the exit is 0 whatever the findings.
func TestPracticesLatestRequestsNothingAndPrintsWhatIsHeld(t *testing.T) {
	s := practicesSession(t, apitest.Options{}, widgetsID)
	s.keepsResult("request-earlier", aCheckResult(widgetsID, time.Now().Add(-5*time.Second)))
	code, stdout, stderr := s.run("practices", "--latest")
	if code != cli.ExitOK {
		t.Fatalf("exit %d with high findings: %s", code, stderr)
	}
	for _, want := range []string{"gadgets not checked yet: no check has reported\n", "widgets at 1111111 (base main), checked 5s ago on plan\n", "high  env-ignored:ignore:."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, stdout)
		}
	}
	list, err := s.daemon.Client.Practices(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range list.Repositories {
		if view.Request != nil {
			t.Errorf("--latest asked for a check of %s: %+v", view.Name, view.Request)
		}
	}
}

// With --json the command prints the views the route answers, and with --latest a repository with no result reads
// with none.
func TestPracticesJSONPrintsTheViews(t *testing.T) {
	s := practicesSession(t, apitest.Options{}, widgetsID)
	s.keepsResult("request-earlier", aCheckResult(widgetsID, time.Now()))
	var list api.PracticesList
	if err := json.Unmarshal([]byte(s.must("practices", "--latest", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Repositories) != 2 || list.Repositories[0].Name != "gadgets" || list.Repositories[0].Result != nil ||
		list.Repositories[1].Result == nil || len(list.Repositories[1].Result.Findings) != 2 {
		t.Errorf("the views are %+v", list)
	}
	var one api.PracticesList
	if err := json.Unmarshal([]byte(s.must("practices", "--latest", "--json", "--repository", "widgets")), &one); err != nil || len(one.Repositories) != 1 {
		t.Errorf("a named repository printed %+v (%v)", one, err)
	}
}

// A repository no project holds is not found, and the command keeps the exit code of that refusal.
func TestPracticesKeepsTheExitCodeOfAnAPIError(t *testing.T) {
	s := practicesSession(t, apitest.Options{})
	for _, args := range [][]string{{"practices", "--repository", "nothing"}, {"practices", "--latest", "--repository", "nothing"}} {
		if code, _, _ := s.run(args...); code != cli.ExitNotFound {
			t.Errorf("%v exited %d, want %d", args, code, cli.ExitNotFound)
		}
	}
	if code, _, _ := s.run("practices", "extra"); code != cli.ExitUsage {
		t.Errorf("an unexpected argument exited %d, want %d", code, cli.ExitUsage)
	}
}

// Every text the check read from a repository is printed through the strip of control characters, so a reason, a
// detail, or a path cannot move the cursor or recolor the terminal.
func TestPracticesStripsControlCharactersFromWhatItPrints(t *testing.T) {
	s := practicesSession(t, apitest.Options{}, widgetsID)
	result := aCheckResult(widgetsID, time.Now())
	result.Findings[0].Detail = "\x1b[31mred\x1b[0m detail\x07"
	result.Findings[1].Path = "config/\x1b]0;title\x07dev.yaml"
	result.Practices[5].Reason = "the forge said \x1b[2Jnothing"
	s.keepsResult("request-earlier", result)
	stdout := s.must("practices", "--latest", "--repository", "widgets")
	if strings.ContainsAny(stdout, "\x1b\x07") {
		t.Errorf("the output holds a control character: %q", stdout)
	}
	if !strings.Contains(stdout, "red detail") || !strings.Contains(stdout, "the forge said nothing") {
		t.Errorf("the output lost the text around the control characters:\n%s", stdout)
	}
}

// An acknowledged finding is listed apart from the open ones with its reason, and a view with none acknowledged prints
// nothing for them.
func TestPracticesTextListsAcknowledgedFindingsApartAndOmitsThemWhenThereAreNone(t *testing.T) {
	checked := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	record := ledger.PracticesRecord{Repository: widgetsID, Request: "r", Commit: aTip, Result: aCheckResult(widgetsID, checked)}
	view := api.PracticesViewOf(ledger.Repository{ID: widgetsID, Name: "widgets"}, &record, nil, 0, nil)
	plain := cli.PracticesText(view, "", checked.Add(time.Hour))
	if strings.Contains(plain, "acknowledged") {
		t.Errorf("a view with none acknowledged prints:\n%s", plain)
	}
	view.Acknowledgement = []api.PracticesAcknowledgement{{Finding: "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f", Reason: "a test fixture", Member: "ada", AcknowledgedAt: checked}}
	got := cli.PracticesText(view, "", checked.Add(time.Hour))
	open, acknowledged, _ := strings.Cut(got, "acknowledged:\n")
	if strings.Contains(open, "secrets:github_token") || !strings.Contains(acknowledged, "high  secrets:github_token:config/dev.yaml:1a2b3c4d5e6f: a test fixture (ada,") ||
		!strings.Contains(open, "env-ignored:ignore:.") {
		t.Errorf("the acknowledged finding is not apart from the open ones:\n%s", got)
	}
}

// A request whose worker reported a failure leaves the latest result as it was, and the command says the check
// produced none.
func TestPracticesSaysWhenAReportedCheckLeftNoResult(t *testing.T) {
	s := practicesSession(t, apitest.Options{}, widgetsID)
	wait := s.whenRequested(widgetsID, func(request ledger.PracticesRequest) {
		err := s.daemon.Store.Write(context.Background(), apitest.Scope, "test.practices", func(tx ports.WriteTx) error {
			if _, err := tx.Practices().Settle(request.ID, tx.Now()); err != nil {
				return err
			}
			_, err := tx.Append(ledger.EventDraft{Type: ledger.EventPracticesFailed, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: "plan"}, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request.ID}})
			return err
		})
		if err != nil {
			t.Error(err)
		}
	})
	code, stdout, _ := s.run("practices", "--repository", "widgets")
	wait()
	if code != cli.ExitOK || !strings.Contains(stdout, "the check on plan reported no result for widgets\n") || !strings.Contains(stdout, "widgets not checked yet: no check has reported\n") {
		t.Errorf("exit %d, output:\n%s", code, stdout)
	}
}
