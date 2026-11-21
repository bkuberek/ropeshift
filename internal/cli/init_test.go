package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	domain "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// initRun runs ropeshift init against a daemon that already answers, with the doctor's report given.
type initRun struct {
	t        *testing.T
	daemon   *apitest.Daemon
	stateDir string
	started  int
	// worktree is the doctor's worktree section, or nil for a build that gives none.
	worktree func(context.Context) (domain.WorktreeReport, error)
}

func newInitRun(t *testing.T, options apitest.Options) *initRun {
	t.Helper()
	daemon := apitest.StartWith(t, options)
	apitest.NewWorld(t, daemon)
	return &initRun{t: t, daemon: daemon, stateDir: t.TempDir()}
}

func (r *initRun) run(manifest ledger.Manifest, terminal func(string) (string, error), args ...string) (int, string, string) {
	r.t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(append([]string{"init"}, args...), &stdout, &stderr, cli.Options{
		Socket: r.daemon.Socket, ReadPassphrase: terminal, StateDir: r.stateDir,
		Doctor: func(context.Context) (ledger.Manifest, error) { return manifest, nil }, Worktree: r.worktree,
		StartDaemon: func(context.Context) (func() error, error) {
			r.started++
			return nil, errors.New("a daemon already answers, so none starts")
		},
	})
	return code, stdout.String(), stderr.String()
}

func claudeCode(kind, status string, qualified bool) ledger.Manifest {
	return ledger.Manifest{Drivers: []ledger.DriverManifest{{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: qualified, SignIn: ledger.SignIn{Kind: kind, Status: status},
	}}}
}

// TestInitSaysWhereEachSignInStands covers the sign-ins the first run on a real machine does not
// meet: an API key, a sign-in nobody could read, and a version the matrix does not hold.
func TestInitSaysWhereEachSignInStands(t *testing.T) {
	tests := []struct {
		name     string
		manifest ledger.Manifest
		say      []string
		never    []string
	}{
		{
			"an API key starts unattended", claudeCode("api_key", "signed_in", true),
			[]string{"claude-code 2.1.316: signed in with an API key, which the CLI reads itself. This worker starts at unattended."},
			[]string{"Consumer Terms", "qualification matrix"},
		},
		{
			"a sign-in nobody could read counts as a subscription", claudeCode("", "unknown", true),
			[]string{"its sign-in could not be read, so it counts as a subscription and this worker starts at attended", "Run claude auth status"},
			[]string{"not signed in"},
		},
		{
			"a subscription names the terms, and an unqualified version its probe", claudeCode("subscription", "signed_in", false),
			[]string{"Anthropic's Consumer Terms restrict automated use of a subscription, and no vendor has approved this use.", "This version is not in the qualification matrix, so the worker probes it in the background as it starts, and a lane that starts before the probe passes runs on documented surfaces."},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newInitRun(t, apitest.Options{})
			code, stdout, stderr := r.run(tt.manifest, nil)
			if code != cli.ExitOK {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			for _, want := range append(tt.say, "Daemon: running.", "Passphrase: already set", "Worker: registered as "+apitest.WorldWorker, "Ropeshift never reads") {
				if !strings.Contains(stdout, want) {
					t.Errorf("did not say %q:\n%s", want, stdout)
				}
			}
			for _, unwanted := range tt.never {
				if strings.Contains(stdout, unwanted) {
					t.Errorf("said %q:\n%s", unwanted, stdout)
				}
			}
			if r.started != 0 {
				t.Errorf("started a daemon %d times beside one that answers", r.started)
			}
		})
	}
}

// TestInitThatStartedTheDaemonNamesHowToRunItNext holds the hint after a run that started a daemon of its
// own: it names the service, the Homebrew service, and the foreground command.
func TestInitThatStartedTheDaemonNamesHowToRunItNext(t *testing.T) {
	var armed, refused atomic.Bool
	r := newInitRun(t, apitest.Options{Wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if armed.Load() && !refused.Swap(true) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			next.ServeHTTP(w, req)
		})
	}})
	armed.Store(true)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"init"}, &stdout, &stderr, cli.Options{
		Socket: r.daemon.Socket, StateDir: r.stateDir,
		Doctor: func(context.Context) (ledger.Manifest, error) { return claudeCode("api_key", "signed_in", true), nil },
		StartDaemon: func(context.Context) (func() error, error) {
			r.started++
			return func() error { return nil }, nil
		},
	})
	if code != cli.ExitOK || r.started != 1 {
		t.Fatalf("exit %d after %d daemon starts, stderr %q", code, r.started, stderr.String())
	}
	for _, want := range []string{"Daemon: started for this run", "ropeshift service install", "brew services start ropeshift", "ropeshiftd solo"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("did not say %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "start the daemon with ropeshiftd solo --source") {
		t.Errorf("named the foreground command as the only way to start the daemon:\n%s", stdout.String())
	}
}

// TestInitAsksForAPassphraseOnlyOnATerminal holds D26 for the first passphrase too.
func TestInitAsksForAPassphraseOnlyOnATerminal(t *testing.T) {
	signedIn := claudeCode("subscription", "signed_in", true)
	r := newInitRun(t, apitest.Options{NoMember: true})
	if code, _, stderr := r.run(signedIn, nil); code != cli.ExitForbidden || !strings.Contains(stderr, "terminal") {
		t.Fatalf("no terminal: exit %d, stderr %q", code, stderr)
	}
	answers := []string{apitest.Passphrase, "something else"}
	differ := func(string) (string, error) {
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
	if code, _, stderr := r.run(signedIn, differ); code != cli.ExitUsage || !strings.Contains(stderr, "differ") {
		t.Fatalf("two that differ: exit %d, stderr %q", code, stderr)
	}
	if state, err := r.daemon.Client.Passphrase(t.Context()); err != nil || state.Set {
		t.Fatalf("a refused run set a passphrase: %+v, %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(r.stateDir, cli.SampleContractName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused run went on to the sample: %v", err)
	}
	same := &asked{passphrase: apitest.Passphrase}
	if code, stdout, stderr := r.run(signedIn, same.read); code != cli.ExitOK || !strings.Contains(stdout, "Passphrase: set.") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if len(same.prompts) != 2 {
		t.Fatalf("asked %v", same.prompts)
	}
	if state, err := r.daemon.Client.Passphrase(t.Context()); err != nil || !state.Set {
		t.Fatalf("after a run: %+v, %v", state, err)
	}
}

// TestInitPrintsOneDocumentWithJSON is what every command does with --json, and the exit code still
// says whether an agent CLI is usable.
func TestInitPrintsOneDocumentWithJSON(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	absent := ledger.Manifest{Drivers: []ledger.DriverManifest{{Driver: "claude-code", Detail: "no claude on PATH"}}}
	code, stdout, stderr := r.run(absent, nil, "--json")
	if code != cli.ExitError {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var result struct {
		Daemon     string                  `json:"daemon"`
		Passphrase string                  `json:"passphrase"`
		Worker     ledger.Worker           `json:"worker"`
		Drivers    []ledger.DriverManifest `json:"drivers"`
		Usable     bool                    `json:"usable"`
		Sample     string                  `json:"sample"`
		Written    bool                    `json:"sample_written"`
		Profile    string                  `json:"profile"`
		ProfileNew bool                    `json:"profile_written"`
		Root       string                  `json:"worktree_root"`
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if decoder.More() {
		t.Fatalf("more than one document: %q", stdout)
	}
	want := filepath.Join(r.stateDir, cli.SampleContractName)
	if result.Daemon != "running" || result.Passphrase != "kept" || result.Worker.ID != apitest.WorldWorker || result.Usable ||
		len(result.Drivers) != 1 || result.Sample != want || !result.Written ||
		result.Profile != filepath.Join(r.stateDir, "profile.toml") || !result.ProfileNew || result.Root != "" {
		t.Errorf("result %+v", result)
	}
	if code, stdout, _ := r.run(absent, nil, "--json"); code != cli.ExitError || !strings.Contains(stdout, `"sample_written":false`) ||
		!strings.Contains(stdout, `"profile_written":false`) {
		t.Errorf("a second run: exit %d, %q", code, stdout)
	}
}

// TestInitThatFailsPrintsOnlyItsError is a step that fails with --json: the failure is the one
// document, on stderr, and nothing reads as a result.
func TestInitThatFailsPrintsOnlyItsError(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"init", "--json"}, &stdout, &stderr, cli.Options{
		Socket: r.daemon.Socket, StateDir: r.stateDir,
		Doctor: func(context.Context) (ledger.Manifest, error) {
			return ledger.Manifest{}, errors.New("the doctor could not run")
		},
		StartDaemon: func(context.Context) (func() error, error) { return nil, errors.New("none starts") },
	})
	if code != cli.ExitError || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"error"`) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

// startingRun runs init where its first request finds no daemon, as a first run does, so init starts one of its
// own. The daemon it starts is a stand-in that writes the log a real one writes, and stops without error.
func startingRun(t *testing.T, doctor func(context.Context) (ledger.Manifest, error), log string, args ...string) (int, string, string) {
	t.Helper()
	var armed, refused atomic.Bool
	r := newInitRun(t, apitest.Options{Wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if armed.Load() && !refused.Swap(true) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			next.ServeHTTP(w, req)
		})
	}})
	armed.Store(true)
	var stdout, stderr bytes.Buffer
	code := cli.Run(append([]string{"init"}, args...), &stdout, &stderr, cli.Options{
		Socket: r.daemon.Socket, StateDir: r.stateDir, DaemonLog: log, Doctor: doctor,
		StartDaemon: func(context.Context) (func() error, error) {
			if err := os.WriteFile(log, []byte("ropeshiftd: serving\n"), 0o600); err != nil {
				return nil, err
			}
			return func() error { return nil }, nil
		},
	})
	return code, stdout.String(), stderr.String()
}

func failingDoctor(context.Context) (ledger.Manifest, error) {
	return ledger.Manifest{}, errors.New("the doctor could not run")
}

// An init that fails after it started a daemon of its own keeps that daemon's log and says where it is, so a run
// that was slow or failed can be read afterwards, in the failure's own message, which is the one document with --json.
func TestInitThatFailsAfterStartingADaemonKeepsItsLogAndSaysWhere(t *testing.T) {
	log := filepath.Join(t.TempDir(), "init-daemon.log")
	code, _, stderr := startingRun(t, failingDoctor, log)
	if code != cli.ExitError || !strings.Contains(stderr, "the doctor could not run") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, log) {
		t.Errorf("the failure does not say where the daemon's log is:\n%s", stderr)
	}
	if raw, err := os.ReadFile(log); err != nil || !strings.Contains(string(raw), "serving") {
		t.Errorf("the failed run did not keep the daemon's log: %q, %v", raw, err)
	}
	t.Run("with --json", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "init-daemon.log")
		code, stdout, stderr := startingRun(t, failingDoctor, log, "--json")
		if code != cli.ExitError || stdout != "" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		var body api.ErrorBody
		decoder := json.NewDecoder(strings.NewReader(stderr))
		if err := decoder.Decode(&body); err != nil || decoder.More() {
			t.Fatalf("stderr is not one document: %q (%v)", stderr, err)
		}
		if !strings.Contains(body.Error.Message, log) {
			t.Errorf("the error %q does not say where the daemon's log is", body.Error.Message)
		}
	})
}

// An init that succeeds has no use for the daemon's log, so it removes it. A run that started no daemon leaves the
// log of an earlier run alone, because that log is not its own.
func TestInitThatSucceedsRemovesTheLogOfTheDaemonItStarted(t *testing.T) {
	log := filepath.Join(t.TempDir(), "init-daemon.log")
	doctor := func(context.Context) (ledger.Manifest, error) { return claudeCode("api_key", "signed_in", true), nil }
	if code, _, stderr := startingRun(t, doctor, log); code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a run that succeeded kept the daemon's log: %v", err)
	}

	earlier := filepath.Join(t.TempDir(), "init-daemon.log")
	if err := os.WriteFile(earlier, []byte("an earlier run\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newInitRun(t, apitest.Options{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"init"}, &stdout, &stderr, cli.Options{
		Socket: r.daemon.Socket, StateDir: r.stateDir, DaemonLog: earlier, Doctor: doctor,
		StartDaemon: func(context.Context) (func() error, error) {
			return nil, errors.New("a daemon already answers, so none starts")
		},
	})
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if raw, err := os.ReadFile(earlier); err != nil || string(raw) != "an earlier run\n" {
		t.Errorf("a run that started no daemon changed the log of an earlier run: %q, %v", raw, err)
	}
}
