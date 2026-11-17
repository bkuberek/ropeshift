package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
)

// Until `ropeshift spend set` ships, the spend policy is read and published through `policy`, as every other kind
// is, and the built-in version carries the defaults.
func TestSpendPolicyCommands(t *testing.T) {
	s := newSession(t)
	if text := s.must("policy", "list", "--kind", "spend"); !strings.Contains(text, "1\tbuilt-in\tsha256:") {
		t.Fatalf("policy list %q", text)
	}
	if text := s.must("policy", "show", "--kind", "spend", "1"); text != authority.BuiltInSpendPolicyBody {
		t.Fatalf("policy show %q", text)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if code, _, _ := s.run("policy", "publish", "--file", write("low.toml", "[spend_policy]\nversion = 2\nmax_live_wait = \"30s\"\n")); code != cli.ExitUsage {
		t.Errorf("a live wait under a minute: exit %d", code)
	}
	body := "[spend_policy]\nversion = 2\nmax_live_wait = \"10m\"\n"
	if text := s.must("policy", "publish", "--file", write("ok.toml", body)); !strings.HasPrefix(text, "2\tmember") {
		t.Fatalf("policy publish %q", text)
	}
	if text := s.must("policy", "show", "--kind", "spend", "2"); text != body {
		t.Fatalf("policy show %q", text)
	}
	if text := s.must("policy", "list"); strings.Contains(text, "2\tmember") {
		t.Fatalf("the decision policy lists %q", text)
	}
}
