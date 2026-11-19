package cli_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/practices"
)

const adoptTip = "1111111111111111111111111111111111111111"

// aLibraryWithFindings gives the api library a Go module with no lockfile, no tests, and no other practice's file.
func (a *adoption) aLibraryWithFindings() {
	a.t.Helper()
	a.in.Practices.SetSnapshot(apiClone.Path, practices.Snapshot{Commit: adoptTip, Paths: []string{"go.mod", "main.go"}, Read: func(string) (string, bool) {
		return "module acme.test/api\n\nrequire example.test/dep v1.0.0\n", true
	}})
}

// The dry run prints, under each repository, the practices block of `ropeshift practices`: the findings by severity
// with why and fix, and the practices that were met. It names no acknowledgement, no worker, and no age: the check ran
// as the plan was read.
func TestAdoptDryRunPrintsThePracticesOfEachRepository(t *testing.T) {
	a := newAdoption(t)
	a.aLibraryWithFindings()
	code, stdout, stderr := a.runIn(shopClone, "", "adopt", "--dry-run", "/work/libs")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	_, api, found := strings.Cut(stdout, "would register\tapi\t")
	if !found {
		t.Fatalf("the dry run lists no api repository:\n%s", stdout)
	}
	block, _, _ := strings.Cut(api, "would register\tweb")
	for _, want := range []string{
		"Practices\n", "  api at 1111111 (base main)\n", "  medium  ci\n", "  low  lockfile:go\n", "    Fix: Run the manager's lock command",
		"    Template: ropeshift template show pre-commit-config.yaml\n", "  met: ",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the api block printed no %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "acknowledged") || strings.Contains(block, " on \n") || strings.Contains(block, "checked") || strings.Contains(block, " ago") {
		t.Errorf("the block names an acknowledgement or a worker:\n%s", block)
	}
}

// --json carries each repository's result, and the plan's digest is the one the plan has without them.
func TestAdoptDryRunJSONCarriesThePractices(t *testing.T) {
	a := newAdoption(t)
	a.aLibraryWithFindings()
	plan := a.plan(shopClone, "/work/libs")
	var ids []string
	for _, repository := range plan.Repositories {
		if repository.Path == apiClone.Path {
			if repository.Practices == nil {
				t.Fatalf("the api repository holds no practices: %q", repository.PracticesNote)
			}
			for _, finding := range repository.Practices.Findings {
				ids = append(ids, finding.ID)
			}
		}
	}
	if !slices.Contains(ids, "lockfile:go") || !slices.Contains(ids, "tests:go") {
		t.Errorf("the api repository's findings are %v", ids)
	}
	if plan.Digest != plan.ComputeDigest() {
		t.Errorf("the digest %s is not the plan's without its practices, %s", plan.Digest, plan.ComputeDigest())
	}
}

// A repository the check could not read prints the note that says why, and the note names no path.
func TestAdoptDryRunNamesWhyAClonesPracticesWereNotRead(t *testing.T) {
	a := newAdoption(t)
	a.in.Practices.Fail = errors.New("git -C " + shopClone.Path + " rev-parse main: fatal: not a git repository")
	code, stdout, stderr := a.runIn(shopClone, "", "adopt", "--dry-run")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Practices\tthe practices were not read: read <checkout> at main: git -C <checkout> rev-parse main: fatal: not a git repository\n") {
		t.Errorf("the dry run names no reason:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Practices") && strings.Contains(line, "/work") {
			t.Errorf("a practices line names a path: %q", line)
		}
	}
}

// An adoption that is no dry run reads no practices: the registration that follows asks for the recorded check.
func TestAdoptThatRecordsReadsNoPractices(t *testing.T) {
	a := newAdoption(t)
	a.aLibraryWithFindings()
	if code, _, stderr := a.runIn(shopClone, "", "adopt", "/work/libs", "--planning", "Shop_App", "--worker", "worker-a", "--no-attach"); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(a.in.Practices.SnapshotCalls) != 0 {
		t.Errorf("the adoption read the practices of %v before it registered", a.in.Practices.SnapshotCalls)
	}
}
