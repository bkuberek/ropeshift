package gitcli

import (
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
