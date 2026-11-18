package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	domain "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// doctorRun runs `ropeshift worker doctor` with the doctor's report given, and no daemon.
func doctorRun(manifest ledger.Manifest) (int, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"worker", "doctor"}, &stdout, &stderr, cli.Options{
		Doctor: func(context.Context) (ledger.Manifest, error) { return manifest, nil },
	})
	return code, stdout.String()
}

// TestTheDoctorSaysEachDriverInTheWordsTheDomainWrites holds each line of `worker doctor` to the result the
// domain gives for the same driver, which the web UI shows too, so the two cannot differ.
func TestTheDoctorSaysEachDriverInTheWordsTheDomainWrites(t *testing.T) {
	usable := ledger.DriverManifest{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}
	unqualified := usable
	unqualified.Qualified = false
	unqualified.SignIn = ledger.SignIn{Status: ledger.SignInUnknown}
	signedOut := usable
	signedOut.SignIn = ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedOut}
	tests := []struct {
		name   string
		driver ledger.DriverManifest
		line   string
		code   int
	}{
		{"usable", usable, "claude-code\t2.1.316\tqualified\tsign-in subscription\tsigned_in\n", cli.ExitOK},
		{"usable, unqualified, and its sign-in unread", unqualified, "claude-code\t2.1.316\tunqualified\tsign-in -\tunknown\n", cli.ExitOK},
		{"absent", ledger.DriverManifest{Driver: "claude-code", Detail: "claude is not on PATH"}, "claude-code\tabsent\tclaude is not on PATH\n", cli.ExitError},
		{
			"a failed version check", ledger.DriverManifest{Driver: "claude-code", Detail: "claude --version exited with status 1"},
			"claude-code\tunusable\tclaude --version exited with status 1. Run claude --version to see why, and repair or reinstall Claude Code.\n", cli.ExitError,
		},
		{"signed out", signedOut, "claude-code\tsigned out\tsign-in subscription\t2.1.316\tqualified\n", cli.ExitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout := doctorRun(ledger.Manifest{Drivers: []ledger.DriverManifest{tt.driver}})
			if code != tt.code || stdout != tt.line {
				t.Errorf("exit %d and %q, want %d and %q", code, stdout, tt.code, tt.line)
			}
			if result := tt.driver.Result(); !result.Usable && result.Word == "" {
				t.Errorf("a driver that is not usable has no word: %+v", result)
			}
		})
	}
}

// TestTheDoctorPrintsTheWorktreeSectionAfterTheDrivers holds the section to the report the worker's check
// gives: the root in force with its layer, and a line for each checkout whose root lies inside a clone, with
// how to fix it. A build with no check, and a built-in root outside every clone, add no finding.
func TestTheDoctorPrintsTheWorktreeSectionAfterTheDrivers(t *testing.T) {
	manifest := ledger.Manifest{Drivers: []ledger.DriverManifest{{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}}}
	const drivers = "claude-code\t2.1.316\tqualified\tsign-in subscription\tsigned_in\n"
	run := func(report domain.WorktreeReport, err error, args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := cli.Run(append([]string{"worker", "doctor"}, args...), &stdout, &stderr, cli.Options{
			Doctor:   func(context.Context) (ledger.Manifest, error) { return manifest, nil },
			Worktree: func(context.Context) (domain.WorktreeReport, error) { return report, err },
		})
		return code, stdout.String(), stderr.String()
	}

	inside := domain.WorktreeReport{
		Root: "/work/app/lanes/{project}/{repo}", Source: domain.RootFromUser,
		Inside: []domain.RootInsideClone{
			{Project: "prj-1", Checkout: "/work/app", Root: "/work/app/lanes/prj-1/app", Clone: "/work/app"},
			{Project: "prj-2", Checkout: "/work/web", Root: "/work/app/lanes/prj-2/web", Clone: "/work/app"},
		},
	}
	want := drivers + "Worktree root: /work/app/lanes/{project}/{repo} (" + domain.RootFromUser + ")\n" +
		"  prj-1 /work/app: the root resolves to /work/app/lanes/prj-1/app, inside the clone /work/app. A lane there fails with worktree_inside_clone: " + domain.RootFix(domain.RootFromUser) + "\n" +
		"  prj-2 /work/web: the root resolves to /work/app/lanes/prj-2/web, inside the clone /work/app. A lane there fails with worktree_inside_clone: " + domain.RootFix(domain.RootFromUser) + "\n"
	if code, stdout, stderr := run(inside, nil); code != cli.ExitOK || stdout != want {
		t.Errorf("exit %d, stdout %q, stderr %q, want %q", code, stdout, stderr, want)
	}

	builtIn := domain.WorktreeReport{Root: "/data/worktrees/{project}/{repo}", Source: domain.RootFromDefault}
	want = drivers + "Worktree root: /data/worktrees/{project}/{repo} (" + domain.RootFromDefault + ")\n"
	if code, stdout, stderr := run(builtIn, nil); code != cli.ExitOK || stdout != want {
		t.Errorf("the built-in root: exit %d, stdout %q, stderr %q, want %q", code, stdout, stderr, want)
	}

	if code, _, stderr := run(domain.WorktreeReport{}, errors.New("the profile is refused")); code != cli.ExitError || !strings.Contains(stderr, "the profile is refused") {
		t.Errorf("a check that fails: exit %d, stderr %q", code, stderr)
	}

	code, stdout, _ := run(inside, nil, "--json")
	var document struct {
		Drivers  []ledger.DriverManifest `json:"drivers"`
		Worktree *domain.WorktreeReport  `json:"worktree"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != cli.ExitOK {
		t.Fatalf("exit %d, stdout %q: %v", code, stdout, err)
	}
	if len(document.Drivers) != 1 || document.Worktree == nil || document.Worktree.Root != inside.Root || len(document.Worktree.Inside) != 2 {
		t.Errorf("the document is %+v", document)
	}
}

// TestTheDoctorPrintsEachProjectsWorkspace holds the doctor's workspace lines: where the workspace is, how it was
// found, whether its .ropeshift/worktrees/ folder is ignored with the line to add when it is not, and the root each
// checkout of the project gets.
func TestTheDoctorPrintsEachProjectsWorkspace(t *testing.T) {
	manifest := ledger.Manifest{Drivers: []ledger.DriverManifest{{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}}}
	report := domain.WorktreeReport{
		Root: "/data/worktrees/{project}/{repo}", Source: domain.RootFromDefault,
		Workspaces: []domain.WorkspaceReport{
			{
				Project: "prj-1", Path: "/work/ws", Found: domain.WorkspaceFromProfile, Clone: true, Ignored: true,
				Roots: []domain.CheckoutRoot{{Checkout: "/work/ws/repos/api", Root: "/work/ws/.ropeshift/worktrees/api"}, {Checkout: "/work/ws", Root: "/data/worktrees/prj-1/ws"}},
			},
			{
				Project: "prj-2", Path: "/work/ws2", Found: domain.WorkspaceFromProfile, Clone: true, Line: "/.ropeshift/worktrees/",
				Roots: []domain.CheckoutRoot{{Checkout: "/work/ws2/repos/web", Root: "/data/worktrees/prj-2/web"}},
			},
			{Project: "prj-3", Path: "/work/plain", Found: domain.WorkspaceFromProfile, Ignored: true, Roots: []domain.CheckoutRoot{}},
		},
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"worker", "doctor"}, &stdout, &stderr, cli.Options{
		Doctor:   func(context.Context) (ledger.Manifest, error) { return manifest, nil },
		Worktree: func(context.Context) (domain.WorktreeReport, error) { return report, nil },
	})
	want := "claude-code\t2.1.316\tqualified\tsign-in subscription\tsigned_in\n" +
		"Worktree root: /data/worktrees/{project}/{repo} (" + domain.RootFromDefault + ")\n" +
		"Workspace prj-1: /work/ws (" + domain.WorkspaceFromProfile + "), which ignores .ropeshift/worktrees/\n" +
		"  /work/ws/repos/api: lane worktrees go in /work/ws/.ropeshift/worktrees/api\n" +
		"  /work/ws: lane worktrees go in /data/worktrees/prj-1/ws\n" +
		"Workspace prj-2: /work/ws2 (" + domain.WorkspaceFromProfile + "), which does not ignore .ropeshift/worktrees/: add /.ropeshift/worktrees/ to its .gitignore so lanes of its clones run inside it\n" +
		"  /work/ws2/repos/web: lane worktrees go in /data/worktrees/prj-2/web\n" +
		"Workspace prj-3: /work/plain (" + domain.WorkspaceFromProfile + "), a plain folder that needs no ignore line\n"
	if code != cli.ExitOK || stdout.String() != want {
		t.Errorf("exit %d, stdout %q, stderr %q, want %q", code, stdout.String(), stderr.String(), want)
	}
}

// TestTheDoctorGoldenPrintsAsTheWebTestsRead writes what `worker doctor` prints for the manifest of each worker in
// the golden of a shop floor whose workers reported different results, so a web test holds the Workers view to the
// command's own words. Each worker's lines follow a `worker` line naming it.
func TestTheDoctorGoldenPrintsAsTheWebTestsRead(t *testing.T) {
	var floor struct {
		Workers []struct {
			Worker struct {
				ID       string          `json:"id"`
				Manifest ledger.Manifest `json:"manifest"`
			} `json:"worker"`
		} `json:"workers"`
	}
	readGolden(t, "shop-floor-drivers.json", &floor)
	if len(floor.Workers) < 6 {
		t.Fatalf("the golden holds %d workers", len(floor.Workers))
	}
	var text strings.Builder
	for _, line := range floor.Workers {
		_, stdout := doctorRun(line.Worker.Manifest)
		fmt.Fprintf(&text, "worker\t%s\n%s", line.Worker.ID, stdout)
	}
	holdsText(t, "doctor.cli.txt", text.String())
}
