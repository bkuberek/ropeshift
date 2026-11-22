package gitcli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// planted are fake values for the variables a git child of the daemon must not see.
var planted = map[string]string{
	"ROPESHIFT_HOOK_NOTIFY":              "planted-webhook-key",
	"ROPESHIFT_MEMBER_TOKEN":             "planted-member-token",
	"ROPESHIFT_DELEGATE_TOKEN":           "planted-delegate-token",
	"ROPESHIFT_DATABASE":                 "planted-database-address",
	"ROPESHIFT_JOIN":                     "planted-join-string",
	"ROPESHIFT_WEBHOOK_KEY":              "planted-old-webhook-key",
	"PGPASSWORD":                         "planted-database-password",
	"OTEL_EXPORTER_OTLP_HEADERS":         "planted-headers",
	"OTEL_EXPORTER_OTLP_TRACES_HEADERS":  "planted-trace-headers",
	"OTEL_EXPORTER_OTLP_METRICS_HEADERS": "planted-metric-headers",
	"OTEL_EXPORTER_OTLP_LOGS_HEADERS":    "planted-log-headers",
}

// envDump is a credential helper that writes its whole environment to the file named by its first argument.
func envDump(t *testing.T, dir string) (helper, dump string) {
	t.Helper()
	dump = filepath.Join(dir, "helper-env.txt")
	script := filepath.Join(dir, "helper.sh")
	body := "#!/bin/sh\nenv > '" + dump + "'\ncat >/dev/null\nprintf 'username=u\\npassword=p\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return "!" + script + " get", dump
}

func TestGitChildDoesNotSeeTheDaemonsSecrets(t *testing.T) {
	repo := gittest.New(t)
	for name, value := range planted {
		t.Setenv(name, value)
	}
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("SSH_AUTH_SOCK", "/planted/agent.sock")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(repo.Home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	helper, dump := envDump(t, t.TempDir())
	git := gitcli.Git{Dir: repo.Dir, Config: [][2]string{{"credential.helper", helper}}}
	if _, err := git.Feed(t.Context(), []byte("protocol=https\nhost=example.invalid\n\n"), "credential", "fill"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the helper did not run: %v", err)
	}
	seen := string(raw)
	for name, value := range planted {
		if strings.Contains(seen, name+"=") || strings.Contains(seen, value) {
			t.Errorf("the git child saw %s", name)
		}
	}
	for _, want := range []string{"PATH=", "SSH_AUTH_SOCK=/planted/agent.sock"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the git child lost %s", want)
		}
	}
}

func TestExplicitEnvironmentLosesTheDaemonsSecretsToo(t *testing.T) {
	repo := gittest.New(t)
	env := append(repo.Env(), "SSH_AUTH_SOCK=/planted/agent.sock")
	for name, value := range planted {
		env = append(env, name+"="+value)
	}
	helper, dump := envDump(t, t.TempDir())
	git := gitcli.Git{Dir: repo.Dir, Env: env, Config: [][2]string{{"credential.helper", helper}}}
	if _, err := git.Feed(t.Context(), []byte("protocol=https\nhost=example.invalid\n\n"), "credential", "fill"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	for name := range planted {
		if strings.Contains(string(raw), name+"=") {
			t.Errorf("the git child saw %s", name)
		}
	}
	if !strings.Contains(string(raw), "SSH_AUTH_SOCK=/planted/agent.sock") {
		t.Error("the git child lost SSH_AUTH_SOCK")
	}
}

func TestWorktreeCreateRunsNoRepositoryHook(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(repo.Dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\necho ran > '" + marker + "'\nenv >> '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	for _, detached := range []bool{false, true} {
		spec := ports.WorktreeSpec{Repository: repo.Dir, Path: filepath.Join(t.TempDir(), "tree"), Branch: "lane/hooks", Detached: detached}
		if detached {
			spec.Branch = ""
		}
		if _, err := worktrees.Create(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("a post-checkout hook ran during the create (detached=%v)", detached)
		}
	}
}

// plantReferenceHook writes a reference-transaction hook, which git runs whenever it updates a ref, and returns
// the file it writes to when it runs.
func plantReferenceHook(t *testing.T, repo *gittest.Repo) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "reference-hook-ran")
	hooks := filepath.Join(repo.Dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ncat >/dev/null\necho \"$1\" >> '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestWorktreeCreateFetchesWithNoRepositoryHook(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "seed\n")
	local := origin.Clone("local")
	head := origin.Commit("next.txt", "next\n")
	marker := plantReferenceHook(t, local)
	worktrees := gitcli.Worktrees{Env: local.Env()}
	spec := ports.WorktreeSpec{Repository: local.Dir, Path: filepath.Join(t.TempDir(), "tree"), Base: head, Detached: true}
	if _, err := worktrees.Create(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a reference-transaction hook ran in the fetch of a create")
	}
}

func TestWorktreesFetchRunsNoRepositoryHook(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "seed\n")
	origin.Git("branch", "lane/resumed")
	local := origin.Clone("local")
	origin.Commit("next.txt", "next\n")
	origin.Git("checkout", "--quiet", "lane/resumed")
	origin.Commit("lane.txt", "lane\n")
	marker := plantReferenceHook(t, local)
	worktrees := gitcli.Worktrees{Env: local.Env()}
	if err := worktrees.Fetch(t.Context(), local.Dir, []string{"refs/heads/lane/resumed"}); err != nil {
		t.Fatal(err)
	}
	if got, want := local.Git("rev-parse", "refs/heads/lane/resumed"), origin.Git("rev-parse", "refs/heads/lane/resumed"); got != want {
		t.Fatalf("the fetch left the ref at %s, want %s", got, want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a reference-transaction hook ran in the fetch")
	}
}

func TestWorktreesFetchCommitRunsNoRepositoryHook(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "seed\n")
	local := origin.Clone("local")
	head := origin.Commit("next.txt", "next\n")
	marker := plantReferenceHook(t, local)
	if err := (gitcli.Worktrees{Env: local.Env()}).FetchCommit(t.Context(), local.Dir, head); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Try("cat-file", "-e", head+"^{commit}"); err != nil {
		t.Fatalf("the commit was not fetched: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a reference-transaction hook ran in the fetch of a commit")
	}
}
