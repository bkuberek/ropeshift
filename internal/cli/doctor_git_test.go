package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	domain "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestTheDoctorNamesTheGitMinimumAndTheVersionFound holds the git section to the report the check gives: a line
// after the agent CLIs that names the minimum and the version found, and an exit of 1 when git cannot run
// ropeshift, as it is when no agent CLI can. A build with no check leaves the section out.
func TestTheDoctorNamesTheGitMinimumAndTheVersionFound(t *testing.T) {
	manifest := ledger.Manifest{Drivers: []ledger.DriverManifest{{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}}}
	const drivers = "claude-code\t2.1.316\tqualified\tsign-in subscription\tsigned_in\n"
	run := func(report domain.GitReport, args ...string) (int, string) {
		var stdout, stderr bytes.Buffer
		code := cli.Run(append([]string{"worker", "doctor"}, args...), &stdout, &stderr, cli.Options{
			Doctor: func(context.Context) (ledger.Manifest, error) { return manifest, nil },
			Git:    func(context.Context) domain.GitReport { return report },
		})
		return code, stdout.String()
	}

	usable := domain.CheckGit(domain.GitVersion{Major: 2, Minor: 45, Patch: 1}, nil)
	if code, stdout := run(usable); code != cli.ExitOK || stdout != drivers+"git\t2.45.1\tminimum 2.31.0\n" {
		t.Errorf("a usable git: exit %d, stdout %q", code, stdout)
	}

	old := domain.CheckGit(domain.GitVersion{Major: 2, Minor: 25, Patch: 1}, nil)
	code, stdout := run(old)
	if want := drivers + "git\tunusable\t" + old.Problem + "\n"; code != cli.ExitError || stdout != want {
		t.Errorf("a git below the minimum: exit %d, stdout %q, want %q", code, stdout, want)
	}

	missing := domain.CheckGit(domain.GitVersion{}, context.DeadlineExceeded)
	if code, stdout := run(missing); code != cli.ExitError || stdout != drivers+"git\tunusable\t"+missing.Problem+"\n" {
		t.Errorf("a git that cannot run: exit %d, stdout %q", code, stdout)
	}

	code, stdout = run(old, "--json")
	var document struct {
		Git *domain.GitReport `json:"git"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != cli.ExitError {
		t.Fatalf("exit %d, stdout %q: %v", code, stdout, err)
	}
	if document.Git == nil || *document.Git != old {
		t.Errorf("the document's git section is %+v, want %+v", document.Git, old)
	}

	var plain, stderr bytes.Buffer
	code = cli.Run([]string{"worker", "doctor"}, &plain, &stderr, cli.Options{
		Doctor: func(context.Context) (ledger.Manifest, error) { return manifest, nil },
	})
	if code != cli.ExitOK || plain.String() != drivers {
		t.Errorf("a build with no check: exit %d, stdout %q", code, plain.String())
	}
}
