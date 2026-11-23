package gitcli

import (
	"os"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/test/gittest"
)

func TestHooklessGitDisablesEveryConfiguredHook(t *testing.T) {
	repo := gittest.New(t)
	repo.Git("config", "hook.format.command", "true")
	repo.Git("config", "--add", "hook.format.event", "post-checkout")
	repo.Git("config", "hook.audit.event", "reference-transaction")
	repo.Git("config", "hook.dotted.name.command", "true")
	repo.Git("config", "hook.audit.enabled", "true")
	git, err := Worktrees{Env: repo.Env()}.hookless(t.Context(), repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range append(NoHooks(), [2]string{"hook..enabled", "false"}, [2]string{"hook.format.enabled", "false"}, [2]string{"hook.audit.enabled", "false"}, [2]string{"hook.dotted.name.enabled", "false"}) {
		if !slices.Contains(git.Config, want) {
			t.Errorf("the settings are %v, want %v among them", git.Config, want)
		}
	}
	if len(git.Config) != len(NoHooks())+4 {
		t.Errorf("the settings are %v, want core.hooksPath, the unnamed hook, and one disabled setting for each of the three hooks", git.Config)
	}
}

func TestHooklessGitInARepositoryWithNoHookConfigurationOnlyRedirectsThePathAndDisablesTheUnnamedHook(t *testing.T) {
	repo := gittest.New(t)
	git, err := Worktrees{Env: repo.Env()}.hookless(t.Context(), repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	want := append(NoHooks(), [2]string{"hook..enabled", "false"})
	if !slices.Equal(git.Config, want) {
		t.Errorf("the settings are %v, want %v", git.Config, want)
	}
}

// NoHooks disables each event of githooks by its name, so git 2.55 and later runs no hook of those events, scanned or
// not: a hook behind an includeIf for a worktree not made yet, or written after the scan, is not among the scanned names.
func TestNoHooksDisablesEveryHookEvent(t *testing.T) {
	events := []string{
		"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg",
		"post-commit", "pre-rebase", "post-checkout", "post-merge", "pre-push", "pre-receive", "update", "proc-receive", "post-receive",
		"post-update", "reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite", "sendemail-validate", "fsmonitor-watchman",
		"p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit", "post-index-change",
	}
	if len(events) != 28 {
		t.Fatalf("the test lists %d events, want the 28 of githooks", len(events))
	}
	got := NoHooks()
	if !slices.Contains(got, [2]string{"core.hooksPath", os.DevNull}) {
		t.Errorf("the settings %v leave core.hooksPath alone", got)
	}
	for _, event := range events {
		if want := [2]string{"hook." + event + ".enabled", "false"}; !slices.Contains(got, want) {
			t.Errorf("the settings leave the %s hook on: want %v among them", event, want)
		}
	}
	if len(got) != len(events)+1 {
		t.Errorf("the settings are %v, want core.hooksPath and one disabled setting for each of %d events", got, len(events))
	}
}
