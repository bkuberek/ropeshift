package projectfolders_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// committed is a repository on main with one commit, and the Folders that read it with the repository's environment.
func committed(t *testing.T) (projectfolders.Folders, *gittest.Repo) {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit("a.txt", "a")
	return projectfolders.Folders{Env: repo.Env()}, repo
}

// bare makes an empty bare repository, as a remote that a push reaches over a local path.
func bare(t *testing.T, repo *gittest.Repo) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	cmd := exec.Command("git", "init", "--bare", "--quiet", "--initial-branch=main", dir)
	cmd.Env = repo.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	return dir
}

func TestAddRemoteNamesOriginAndRefusesASecond(t *testing.T) {
	f, repo := committed(t)
	url := bare(t, repo)
	if err := f.AddRemote(t.Context(), repo.Dir, url); err != nil {
		t.Fatal(err)
	}
	if got := repo.Git("remote", "get-url", "origin"); got != url {
		t.Errorf("origin = %q, want %q", got, url)
	}
	err := f.AddRemote(t.Context(), repo.Dir, url)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second origin: %v, want git's refusal without its command line", err)
	}
	if err != nil && strings.Contains(err.Error(), "exit status") {
		t.Errorf("the refusal keeps the exit status: %v", err)
	}
}

func TestRemoteReadsOriginTheBranchAndWhatOriginHolds(t *testing.T) {
	f, repo := committed(t)
	head := repo.Git("rev-parse", "HEAD")

	state, err := f.Remote(t.Context(), repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.RemoteState{Branch: "main", Head: head}); state != want {
		t.Errorf("before a remote: %+v, want %+v", state, want)
	}

	url := bare(t, repo)
	if err := f.AddRemote(t.Context(), repo.Dir, url); err != nil {
		t.Fatal(err)
	}
	if state, err = f.Remote(t.Context(), repo.Dir); err != nil {
		t.Fatal(err)
	}
	if want := (ports.RemoteState{Origin: url, Branch: "main", Head: head}); state != want {
		t.Errorf("before a push: %+v, want %+v: origin holds nothing", state, want)
	}

	if err := f.Push(t.Context(), repo.Dir, ports.PushSpec{Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if state, err = f.Remote(t.Context(), repo.Dir); err != nil {
		t.Fatal(err)
	}
	if want := (ports.RemoteState{Origin: url, Branch: "main", Head: head, RemoteHead: head}); state != want {
		t.Errorf("after a push: %+v, want %+v", state, want)
	}
}

func TestRemoteOfAnUnreachableOriginHasNoRemoteHead(t *testing.T) {
	f, repo := committed(t)
	missing := filepath.Join(t.TempDir(), "missing.git")
	if err := f.AddRemote(t.Context(), repo.Dir, missing); err != nil {
		t.Fatal(err)
	}
	state, err := f.Remote(t.Context(), repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Origin != missing || state.RemoteHead != "" || state.Head == "" {
		t.Errorf("state = %+v", state)
	}
}

func TestPushSendsTheBranchAndSetsItsUpstream(t *testing.T) {
	f, repo := committed(t)
	url := bare(t, repo)
	if err := f.AddRemote(t.Context(), repo.Dir, url); err != nil {
		t.Fatal(err)
	}
	if err := f.Push(t.Context(), repo.Dir, ports.PushSpec{Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if got, want := repo.RemoteHead("origin", "refs/heads/main"), repo.Git("rev-parse", "HEAD"); got != want {
		t.Errorf("origin holds %q, want %q", got, want)
	}
	if remote, merge := repo.Git("config", "branch.main.remote"), repo.Git("config", "branch.main.merge"); remote != "origin" || merge != "refs/heads/main" {
		t.Errorf("upstream = %q %q, want origin refs/heads/main", remote, merge)
	}
}

func TestAFailedPushSaysWhatGitSaid(t *testing.T) {
	f, repo := committed(t)
	if err := f.AddRemote(t.Context(), repo.Dir, filepath.Join(t.TempDir(), "missing.git")); err != nil {
		t.Fatal(err)
	}
	err := f.Push(t.Context(), repo.Dir, ports.PushSpec{Branch: "main"})
	if err == nil || strings.TrimSpace(err.Error()) == "" || strings.Contains(err.Error(), "exit status") {
		t.Errorf("err = %v, want git's own message", err)
	}
}

// A push that names a credential helper sets git's credential configuration for that one command in its
// environment, which a pre-push hook reads, and leaves no credential key in the repository's configuration.
func TestAPushThatNamesACredentialHelperWritesNoConfiguration(t *testing.T) {
	f, repo := committed(t)
	if err := f.AddRemote(t.Context(), repo.Dir, bare(t, repo)); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(t.TempDir(), "env")
	hook := "#!/bin/sh\nenv > '" + seen + "'\n"
	if err := os.MkdirAll(filepath.Join(repo.Dir, ".git", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Dir, ".git", "hooks", "pre-push"), []byte(hook), 0o700); err != nil { //nolint:gosec // a test hook must be executable
		t.Fatal(err)
	}

	const helper = "/opt/gh/bin/gh"
	if err := f.Push(t.Context(), repo.Dir, ports.PushSpec{Branch: "main", CredentialHelper: helper}); err != nil {
		t.Fatal(err)
	}

	text, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the pre-push hook did not run: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(text), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	const key = "credential.https://github.com.helper"
	var values []string
	for name, value := range env {
		if strings.HasPrefix(name, "GIT_CONFIG_KEY_") && value == key {
			values = append(values, env[strings.Replace(name, "KEY", "VALUE", 1)])
		}
	}
	if len(values) != 2 || !slices.Contains(values, "") || !slices.Contains(values, `!"`+helper+`" auth git-credential`) {
		t.Errorf("the push's configuration for %s = %q, want the helpers cleared and gh named", key, values)
	}

	config, err := os.ReadFile(filepath.Join(repo.Dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "credential") || strings.Contains(string(config), helper) {
		t.Errorf(".git/config holds a credential setting after the push:\n%s", config)
	}
	if out, err := exec.Command("git", "-C", repo.Dir, "config", "--local", "--get-regexp", `^credential\.`).CombinedOutput(); err == nil {
		t.Errorf("git reads local credential keys after the push: %s", out)
	}
}

// A push that names no credential helper sets no credential configuration, so the person's own credentials serve.
func TestAPushThatNamesNoCredentialHelperSetsNoCredentialConfiguration(t *testing.T) {
	f, repo := committed(t)
	if err := f.AddRemote(t.Context(), repo.Dir, bare(t, repo)); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(t.TempDir(), "env")
	if err := os.MkdirAll(filepath.Join(repo.Dir, ".git", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\nenv > '" + seen + "'\n"
	if err := os.WriteFile(filepath.Join(repo.Dir, ".git", "hooks", "pre-push"), []byte(hook), 0o700); err != nil { //nolint:gosec // a test hook must be executable
		t.Fatal(err)
	}
	if err := f.Push(t.Context(), repo.Dir, ports.PushSpec{Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "credential") {
		t.Errorf("the push's environment names a credential setting:\n%s", text)
	}
}

// Remote asks origin for the branch with `--` before the remote, so no name is read as an option.
func TestRemoteListsOriginsBranchAfterTheEndOfOptions(t *testing.T) {
	f, repo := committed(t)
	if err := f.AddRemote(t.Context(), repo.Dir, bare(t, repo)); err != nil {
		t.Fatal(err)
	}
	ran := gittest.RecordGit(t)
	if _, err := f.Remote(t.Context(), repo.Dir); err != nil {
		t.Fatal(err)
	}
	want := []string{"ls-remote -- origin refs/heads/main"}
	if got := ran("ls-remote"); !slices.Equal(got, want) {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A push that names a credential helper adds its settings after the ones the environment already carries, so the count
// grows and none of the environment's own is replaced.
func TestAPushThatNamesACredentialHelperKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.txt", "a")
	f := projectfolders.Folders{Env: append(repo.Env(), "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=test.first", "GIT_CONFIG_VALUE_0=one", "GIT_CONFIG_KEY_1=test.second", "GIT_CONFIG_VALUE_1=two")}
	if err := f.AddRemote(t.Context(), repo.Dir, bare(t, repo)); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(t.TempDir(), "env")
	if err := os.MkdirAll(filepath.Join(repo.Dir, ".git", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\nenv > '" + seen + "'\n"
	if err := os.WriteFile(filepath.Join(repo.Dir, ".git", "hooks", "pre-push"), []byte(hook), 0o700); err != nil { //nolint:gosec // a test hook must be executable
		t.Fatal(err)
	}
	const helper = "/opt/gh/bin/gh"
	if err := f.Push(t.Context(), repo.Dir, ports.PushSpec{Branch: "main", CredentialHelper: helper}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the pre-push hook did not run: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(text), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	count, err := strconv.Atoi(env["GIT_CONFIG_COUNT"])
	if err != nil {
		t.Fatalf("GIT_CONFIG_COUNT = %q", env["GIT_CONFIG_COUNT"])
	}
	var settings []string
	for at := range count {
		n := strconv.Itoa(at)
		settings = append(settings, env["GIT_CONFIG_KEY_"+n]+"="+env["GIT_CONFIG_VALUE_"+n])
	}
	const key = "credential.https://github.com.helper"
	for _, want := range []string{"test.first=one", "test.second=two", key + "=", key + `=!"` + helper + `" auth git-credential`} {
		if !slices.Contains(settings, want) {
			t.Errorf("the push's %d settings = %q, lack %q", count, settings, want)
		}
	}
}
