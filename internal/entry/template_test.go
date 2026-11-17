package entry

import (
	"os"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/templates"
)

// FR-TPL-1 and FR-TPL-3: through the binary's own router, with no daemon, no session, and no delegate
// credential in the environment, `template show` prints and lists, and touches no installation: the state and
// run directories it was pointed at stay empty.
func TestTemplateShowRunsThroughTheBinaryWithNoDaemonAndNoSession(t *testing.T) {
	isolate(t)
	for _, name := range []string{authority.EnvMemberToken, authority.EnvDelegateToken, api.EnvProject} {
		t.Setenv(name, "")
	}
	state, run := t.TempDir(), t.TempDir()
	t.Setenv(api.EnvStateDir, state)
	t.Setenv(api.EnvRunDir, run)
	want, err := templates.Render("workspace/plan/repositories.md", templates.Data{Example: true})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runMain(commandCLI, "template", "show", "workspace/plan/repositories.md")
	if code != exitOK || stdout != want || stderr != "" {
		t.Errorf("exit %d, stderr %q, stdout %d bytes, want %d", code, stderr, len(stdout), len(want))
	}
	if code, stdout, stderr := runMain(commandCLI, "template", "show"); code != exitOK || stderr != "" || stdout == "" {
		t.Errorf("the list: exit %d, stderr %q, stdout %q", code, stderr, stdout)
	}
	if code, _, stderr := runMain(commandCLI, "template", "show", "no/such"); code != exitUsage || stderr == "" {
		t.Errorf("an unknown name: exit %d, stderr %q", code, stderr)
	}
	for _, dir := range []string{state, run} {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("%s holds %v (%v) after template show", dir, entries, err)
		}
	}
}
