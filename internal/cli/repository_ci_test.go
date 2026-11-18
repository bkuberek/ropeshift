package cli_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
)

// `repository set <repository> ci.<key>=<value>` edits a repository's CI keys (ADR-0037). Each takes effect, shows
// beside the defaults it leaves, and a value outside its range is refused with the key named and changes nothing.
func TestRepositorySetEditsTheCIKeys(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	if text := s.must("repository", "show", aRepositoryID); !strings.Contains(text, "ci.ci_steady_interval\t5m") || !strings.Contains(text, "ci.ci_auto_rerun\t1") {
		t.Errorf("show does not say the defaults:\n%s", text)
	}
	text := s.must("repository", "set", aRepositoryID[:8], "ci.ci_steady_interval=2m", "ci.ci_auto_rerun=0")
	for _, want := range []string{"ci.ci_steady_interval\t2m", "ci.ci_auto_rerun\t0", "ci.ci_no_history_p50\t10m"} {
		if !strings.Contains(text, want) {
			t.Errorf("set printed no %q:\n%s", want, text)
		}
	}
	for name, setting := range map[string]string{
		"a value outside its range": "ci.ci_steady_interval=10s",
		"a rerun count past three":  "ci.ci_auto_rerun=4",
		"a key nobody offers":       "ci.ci_made_up=1",
		"no value":                  "ci.ci_steady_interval",
		"a duration that is none":   "ci.ci_steady_interval=soon",
	} {
		code, _, stderr := s.run("repository", "set", aRepositoryID, setting)
		if code != cli.ExitUsage || !strings.Contains(stderr, "ci_") {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
		}
	}
	if got := s.must("repository", "show", aRepositoryID); !strings.Contains(got, "ci.ci_steady_interval\t2m") || !strings.Contains(got, "ci.ci_auto_rerun\t0") {
		t.Errorf("a refused value changed the record:\n%s", got)
	}
	if code, _, stderr := s.run("repository", "set", aRepositoryID, "--merge-method", "squash", "ci.ci_min_interval=1m"); code != cli.ExitOK {
		t.Errorf("a flag and a CI key together: exit %d, %q", code, stderr)
	}
}
