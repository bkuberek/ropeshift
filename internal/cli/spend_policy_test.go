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
	versionTwo := authority.DefaultSpendPolicy()
	versionTwo.Version = 2
	low := strings.Replace(versionTwo.Body(), `max_live_wait = "5m"`, `max_live_wait = "30s"`, 1)
	if code, _, stderr := s.run("policy", "publish", "--file", write("low.toml", low)); code != cli.ExitUsage || !strings.Contains(stderr, "max_live_wait") {
		t.Errorf("a live wait under a minute: exit %d, %q", code, stderr)
	}
	if code, _, stderr := s.run("policy", "publish", "--file", write("partial.toml", "[spend_policy]\nversion = 2\nmax_live_wait = \"10m\"\n")); code != cli.ExitUsage || !strings.Contains(stderr, "states max_tool_sleep") {
		t.Errorf("a policy that leaves keys out: exit %d, %q", code, stderr)
	}
	body := strings.Replace(versionTwo.Body(), `max_live_wait = "5m"`, `max_live_wait = "10m"`, 1)
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
