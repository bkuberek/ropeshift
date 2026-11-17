package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/cli"
	domain "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/test/apitest"
)

var signedIn = claudeCode("api_key", "signed_in", true)

// profileText reads the profile init wrote in the state directory.
func (r *initRun) profileText() string {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.stateDir, profiles.UserFile))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw)
}

// A fresh state directory gets profile.toml from the user profile template with the root set to the path
// given. A path that names neither {project} nor {repo} gets the built-in root's tail, and init says so. The
// profile resolves with the product's own reader, and its root is the one written.
func TestInitWritesTheUserProfileWithTheWorktreeRootItIsGiven(t *testing.T) {
	const appended = "names neither {project} nor {repo}, so /{project}/{repo} is appended"
	tests := []struct {
		name     string
		given    string
		wantRoot string
		appended bool
	}{
		{"a root with both tokens", "/srv/lanes/{project}/{repo}", "/srv/lanes/{project}/{repo}", false},
		{"a root with the project token", "/srv/{project}/lanes", "/srv/{project}/lanes", false},
		{"a root with the repository token", "/srv/lanes/{repo}", "/srv/lanes/{repo}", false},
		{"a root with no token", "/srv/lanes", "/srv/lanes/{project}/{repo}", true},
		{"a root with no token and a trailing slash", "/srv/lanes/", "/srv/lanes/{project}/{repo}", true},
		{"the top of the disk", "/", "/{project}/{repo}", true},
		{"a path with a quote and a backslash", `/srv/a"b\c`, `/srv/a"b\c/{project}/{repo}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newInitRun(t, apitest.Options{})
			code, stdout, stderr := r.run(signedIn, nil, "--worktree-root", tt.given)
			if code != cli.ExitOK {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			text := r.profileText()
			profile, err := profiles.Resolve(text)
			if err != nil {
				t.Fatalf("the profile does not resolve: %v\n%s", err, text)
			}
			if profile.Worktree.Root != tt.wantRoot {
				t.Errorf("the root is %q, want %q", profile.Worktree.Root, tt.wantRoot)
			}
			path := filepath.Join(r.stateDir, profiles.UserFile)
			if !strings.Contains(stdout, "Profile: wrote "+path) || !strings.Contains(stdout, tt.wantRoot) {
				t.Errorf("did not say it wrote the profile with the root %q:\n%s", tt.wantRoot, stdout)
			}
			if got := strings.Contains(stdout, appended); got != tt.appended {
				t.Errorf("said the tail was appended: %v, want %v:\n%s", got, tt.appended, stdout)
			}
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("the profile's mode is %v (%v)", info, err)
			}
			// The rest of init ran as it does without the flag.
			for _, want := range []string{"Daemon: running.", "Passphrase: already set", "Worker: registered as", "Agent CLIs:", "Sample contract: wrote"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("did not say %q:\n%s", want, stdout)
				}
			}
		})
	}
}

// Without the flag init writes the profile with the root line commented out, so the built-in root holds.
func TestInitWritesTheUserProfileWithTheRootCommentedOut(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	code, stdout, stderr := r.run(signedIn, nil)
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	text := r.profileText()
	profile, err := profiles.Resolve(text)
	if err != nil {
		t.Fatalf("the profile does not resolve: %v\n%s", err, text)
	}
	if profile.Worktree.Root != "" {
		t.Errorf("the profile sets the root %q", profile.Worktree.Root)
	}
	if !strings.Contains(text, "\n# root = ") || strings.Contains(text, "\nroot = ") {
		t.Errorf("the root line is not commented out:\n%s", text)
	}
	for _, want := range []string{"Profile: wrote " + filepath.Join(r.stateDir, profiles.UserFile), "built-in root"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("did not say %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "neither {project}") {
		t.Errorf("said a tail was appended with no flag:\n%s", stdout)
	}
}

// A path that is not absolute is a usage error before anything is written or started, because a root with no
// anchor would resolve inside each repository.
func TestInitRefusesARelativeWorktreeRoot(t *testing.T) {
	for _, given := range []string{"lanes", "./lanes", "../lanes/{repo}", "~/lanes", ""} {
		t.Run(given, func(t *testing.T) {
			r := newInitRun(t, apitest.Options{})
			code, stdout, stderr := r.run(signedIn, nil, "--worktree-root", given)
			if code != cli.ExitUsage || !strings.Contains(stderr, "--worktree-root") || !strings.Contains(stderr, "absolute") {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			for _, name := range []string{profiles.UserFile, cli.SampleContractName} {
				if _, err := os.Stat(filepath.Join(r.stateDir, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a refused run wrote %s: %v", name, err)
				}
			}
			if r.started != 0 || stdout != "" {
				t.Errorf("a refused run started a daemon %d times and said %q", r.started, stdout)
			}
		})
	}
}

// A profile that exists is never written. init says it kept it, and with the flag says the root is the person's
// to set, by hand, in that file. Every other step of init is unchanged.
func TestInitKeepsAProfileThatExists(t *testing.T) {
	const mine = "[worktree]\nroot = \"/mine/{project}/{repo}\"\n"
	for _, args := range [][]string{nil, {"--worktree-root", "/srv/lanes"}} {
		r := newInitRun(t, apitest.Options{})
		path := filepath.Join(r.stateDir, profiles.UserFile)
		if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := r.run(signedIn, nil, args...)
		if code != cli.ExitOK {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr)
		}
		if raw, err := os.ReadFile(path); err != nil || string(raw) != mine {
			t.Errorf("%v: the profile reads %q (%v)", args, raw, err)
		}
		if !strings.Contains(stdout, "Profile: kept "+path) {
			t.Errorf("%v: did not say it kept the profile:\n%s", args, stdout)
		}
		byHand := "set [worktree] root in it by hand"
		if got := strings.Contains(stdout, byHand); got != (len(args) != 0) {
			t.Errorf("%v: said to set the root by hand: %v:\n%s", args, got, stdout)
		}
		for _, want := range []string{"Agent CLIs:", "Sample contract: wrote"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%v: did not say %q:\n%s", args, want, stdout)
			}
		}
	}
}

// A second run keeps what the first wrote, and the flag on it changes nothing.
func TestASecondInitKeepsTheProfileTheFirstWrote(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	if code, _, stderr := r.run(signedIn, nil, "--worktree-root", "/srv/first/{repo}"); code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	first := r.profileText()
	code, stdout, stderr := r.run(signedIn, nil, "--worktree-root", "/srv/second/{repo}")
	if code != cli.ExitOK || !strings.Contains(stdout, "Profile: kept") || !strings.Contains(stdout, "set [worktree] root in it by hand") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if r.profileText() != first {
		t.Errorf("the second run wrote the profile:\n%s", r.profileText())
	}
}

// init prints the doctor's worktree section after its agent CLIs, once the profile is written, so the root
// it prints is the one in force for the next lane.
func TestInitPrintsTheDoctorsWorktreeSectionAfterTheDoctor(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	wrote := false
	r.worktree = func(context.Context) (domain.WorktreeReport, error) {
		_, err := os.Stat(filepath.Join(r.stateDir, profiles.UserFile))
		wrote = err == nil
		return domain.WorktreeReport{
			Root: "/work/app/lanes/{project}/{repo}", Source: domain.RootFromUser,
			Inside: []domain.RootInsideClone{{Project: "prj-1", Checkout: "/work/app", Root: "/work/app/lanes/prj-1/app", Clone: "/work/app"}},
		}, nil
	}
	code, stdout, stderr := r.run(signedIn, nil, "--worktree-root", "/work/app/lanes")
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !wrote {
		t.Error("the section was read before the profile was written")
	}
	order := []string{
		"Agent CLIs:", "Ropeshift never reads", "Worktree root: /work/app/lanes/{project}/{repo} (" + domain.RootFromUser + ")",
		"prj-1 /work/app: the root resolves to /work/app/lanes/prj-1/app, inside the clone /work/app", domain.RootFix(domain.RootFromUser), "Sample contract:",
	}
	at := 0
	for _, want := range order {
		i := strings.Index(stdout[at:], want)
		if i < 0 {
			t.Fatalf("did not say %q after %q:\n%s", want, stdout[:at], stdout)
		}
		at += i + len(want)
	}
}

// A built-in root that lies in no clone adds one line and no finding, and a section that cannot be read ends the
// run as the doctor does.
func TestInitSaysTheBuiltInRootAndFailsWhenTheSectionCannotBeRead(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	r.worktree = func(context.Context) (domain.WorktreeReport, error) {
		return domain.WorktreeReport{Root: "/data/worktrees/{project}/{repo}", Source: domain.RootFromDefault}, nil
	}
	code, stdout, stderr := r.run(signedIn, nil)
	if code != cli.ExitOK || !strings.Contains(stdout, "Worktree root: /data/worktrees/{project}/{repo} ("+domain.RootFromDefault+")") ||
		strings.Contains(stdout, "inside the clone") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	r = newInitRun(t, apitest.Options{})
	r.worktree = func(context.Context) (domain.WorktreeReport, error) {
		return domain.WorktreeReport{}, errors.New("the profile is refused")
	}
	if code, _, stderr := r.run(signedIn, nil); code != cli.ExitError || !strings.Contains(stderr, "the profile is refused") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// With --json the profile and the root written, and the doctor's worktree section, are in the one document.
func TestInitPrintsTheProfileAndTheWorktreeSectionWithJSON(t *testing.T) {
	r := newInitRun(t, apitest.Options{})
	report := domain.WorktreeReport{
		Root: "/srv/lanes/{project}/{repo}", Source: domain.RootFromUser,
		Inside: []domain.RootInsideClone{{Project: "prj-1", Checkout: "/work/app", Root: "/srv/lanes/prj-1/app", Clone: "/srv"}},
	}
	r.worktree = func(context.Context) (domain.WorktreeReport, error) { return report, nil }
	code, stdout, stderr := r.run(signedIn, nil, "--json", "--worktree-root", "/srv/lanes")
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var result struct {
		Profile  string                 `json:"profile"`
		Written  bool                   `json:"profile_written"`
		Root     string                 `json:"worktree_root"`
		Worktree *domain.WorktreeReport `json:"worktree"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if result.Profile != filepath.Join(r.stateDir, profiles.UserFile) || !result.Written || result.Root != "/srv/lanes/{project}/{repo}" {
		t.Errorf("result %+v", result)
	}
	if result.Worktree == nil || result.Worktree.Root != report.Root || result.Worktree.Source != report.Source || len(result.Worktree.Inside) != 1 {
		t.Errorf("the worktree section is %+v", result.Worktree)
	}
}
