package gitcli

import (
	"strconv"
	"strings"
	"testing"
)

// configured reads the settings a command's environment passes to git, the way git does, with a later
// variable replacing an earlier one of the same name.
func configured(t *testing.T, env []string) map[string]string {
	t.Helper()
	last := map[string]string{}
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			last[name] = value
		}
	}
	count, err := strconv.Atoi(last["GIT_CONFIG_COUNT"])
	if err != nil {
		t.Fatalf("GIT_CONFIG_COUNT is %q", last["GIT_CONFIG_COUNT"])
	}
	settings := map[string]string{}
	for i := range count {
		n := strconv.Itoa(i)
		settings[last["GIT_CONFIG_KEY_"+n]] = last["GIT_CONFIG_VALUE_"+n]
	}
	return settings
}

func TestEveryCommandRunsWithAutoMaintenanceOff(t *testing.T) {
	got := configured(t, Git{Env: []string{"PATH=/usr/bin"}}.environment())
	if got["maintenance.auto"] != "false" {
		t.Errorf("a command's settings are %v, want maintenance.auto=false", got)
	}
}

func TestAutoMaintenanceIsOffBesideTheSettingsACallerAdds(t *testing.T) {
	for name, config := range map[string][][2]string{
		"no hooks":            NoHooks(),
		"no hooks no signing": NoHooksNoSigning(),
	} {
		t.Run(name, func(t *testing.T) {
			got := configured(t, Git{Env: []string{"PATH=/usr/bin"}, Config: config}.environment())
			if got["maintenance.auto"] != "false" || got["core.hooksPath"] == "" {
				t.Errorf("a command's settings are %v, want both maintenance.auto=false and the caller's own", got)
			}
		})
	}
}
