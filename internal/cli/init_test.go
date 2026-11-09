package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// initRun runs ropeshift init against a daemon that already answers, with the doctor's report given.
type initRun struct {
	t        *testing.T
	daemon   *apitest.Daemon
	stateDir string
	started  int
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
		Doctor: func(context.Context) (ledger.Manifest, error) { return manifest, nil },
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
			[]string{"Anthropic's Consumer Terms restrict automated use of a subscription, and no vendor has approved this use.", "This version is not in the qualification matrix"},
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
		len(result.Drivers) != 1 || result.Sample != want || !result.Written {
		t.Errorf("result %+v", result)
	}
	if code, stdout, _ := r.run(absent, nil, "--json"); code != cli.ExitError || !strings.Contains(stdout, `"sample_written":false`) {
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
