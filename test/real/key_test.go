//go:build realsession

package real

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An API-key lane is the one session in these suites that runs on a key. The key must reach that
// session and nothing else: no subscription session, no log, no evidence, and no error. The harness
// reads it from one variable, holds it where no format verb prints it, adds it to the environment of
// the one daemon that starts that lane, and scans everything the run leaves for it before the run
// ends. The lane's profile names the variable, so the production driver passes it to that lane alone.

const (
	// envTestKey is the one variable a run reads a key from, and only the API-key part reads it.
	envTestKey = "ROPESHIFT_TEST_ANTHROPIC_API_KEY"
	// envSessionKey is the variable the CLI reads a key from, which the lane's profile names.
	envSessionKey = "ANTHROPIC_API_KEY"
	// redactedKey is all any format verb prints of a key.
	redactedKey = "<redacted>"
	// subscriptionSource is what an init says of a session that runs on no key.
	subscriptionSource = "none"
)

// secretKey holds a key where no format verb can print it. The value sits behind a function, which
// fmt prints as an address however it reaches it, even inside a struct printed field by field with a
// verb that does not fit, where a pointer to a struct would show what it points to.
type secretKey struct{ value func() string }

func newSecretKey(value string) secretKey { return secretKey{value: func() string { return value }} }

// reveal is the one way to the value, for the environment that carries it and for the scan.
func (k secretKey) reveal() string {
	if k.value == nil {
		return ""
	}
	return k.value()
}

func (k secretKey) set() bool { return k.reveal() != "" }

// Format prints the same word for every verb.
func (k secretKey) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redactedKey)) }

func (k secretKey) String() string { return redactedKey }

func (k secretKey) GoString() string { return redactedKey }

// testKey reads the key for the API-key part, and skips that part when none is set.
func testKey(t *testing.T) secretKey {
	t.Helper()
	key := newSecretKey(os.Getenv(envTestKey))
	if !key.set() {
		t.Skipf("%s is unset, so the API-key lane does not run", envTestKey)
	}
	return key
}

// keyedEnvironment is a keyed daemon's environment: a clean one, plus the key by the name the lane's
// profile reads. It is the one place a key joins an environment. It refuses an empty key, and a base
// that already holds a vendor variable, which a clean environment never does.
func keyedEnvironment(base []string, key secretKey) ([]string, error) {
	if !key.set() {
		return nil, fmt.Errorf("refused: no key to pass")
	}
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "ANTHROPIC") || strings.HasPrefix(name, "CLAUDE") || name == envTestKey {
			return nil, fmt.Errorf("refused: the base environment holds %s", name)
		}
	}
	return append(slices.Clone(base), envSessionKey+"="+key.reveal()), nil
}

// withoutKeys is an environment with no key in it, for the toolchain this suite builds with.
func withoutKeys(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return name == envTestKey || name == envSessionKey
	})
}

// keyFindings names each place a run's output holds the key: a line the suite logged, a buffer a
// process wrote, or a file under the run's root, by its path under that root. A file it cannot read
// counts, because it could hold the key. A finding never holds the value.
func keyFindings(key secretKey, root string, lines []string, buffers map[string][]byte) []string {
	if !key.set() {
		return nil
	}
	needle := []byte(key.reveal())
	var found []string
	if slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, string(needle)) }) {
		found = append(found, "a logged line")
	}
	for _, name := range slices.Sorted(maps.Keys(buffers)) {
		if bytes.Contains(buffers[name], needle) {
			found = append(found, name)
		}
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			found = append(found, "an entry under the run's root, which could not be read")
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			name = entry.Name()
		}
		raw, err := os.ReadFile(path)
		switch {
		case err != nil:
			found = append(found, "file "+name+", which could not be read")
		case bytes.Contains(raw, needle):
			found = append(found, "file "+name)
		}
		return nil
	})
	if err != nil {
		found = append(found, "the run's root, which could not be read")
	}
	return found
}

// laneInit says an init is a lane's: only a lane's session is given this suite's MCP server.
func laneInit(init initLine) bool { return len(init.Servers) != 0 }

// keyedInitFailures names what a keyed run's inits fail to show: each lane's session ran on the key,
// and every other session, each a subscription session of the qualification probe, ran on none.
// lanesOnKey false leaves the lanes' source unread, for a dry run whose scripted CLI reports none.
// A source is a vendor's name for where a key came from, never a key.
func keyedInitFailures(inits []initLine, lanesOnKey bool) []string {
	var found []string
	for _, init := range inits {
		switch {
		case laneInit(init) && lanesOnKey && init.KeySource != envSessionKey:
			found = append(found, fmt.Sprintf("the lane ran on %q, not on the key", init.KeySource))
		case !laneInit(init) && init.KeySource != subscriptionSource:
			found = append(found, fmt.Sprintf("a subscription session ran on %q", init.KeySource))
		}
	}
	return found
}

func TestLeftoverEntriesOfTheCLIAreListed(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "session-env", "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readEntries(home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"more":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "session-env", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := entriesReport(before, readEntries(home))
	want := []string{
		".claude.json changed",
		filepath.Join(".claude", "history.jsonl") + " absent",
		filepath.Join(".claude", "session-env") + " held 1 entries and now 2",
	}
	if !slices.Equal(got, want) {
		t.Errorf("report %q, want %q", got, want)
	}
	if got := entriesReport(before, before); got[0] != ".claude.json unchanged" {
		t.Errorf("an unchanged file reads %q", got[0])
	}
}

func TestEnvironmentOfAKeyedDaemon(t *testing.T) {
	const value = "sk-test-value-0123456789"
	base := []string{"HOME=/h", "PATH=/p", "ROPESHIFT_STATE_DIR=/s"}
	got, err := keyedEnvironment(base, newSecretKey(value))
	if err != nil {
		t.Fatal(err)
	}
	if want := append(slices.Clone(base), envSessionKey+"="+value); !slices.Equal(got, want) {
		t.Errorf("a keyed environment holds %d entries, want the base and the key by name", len(got))
	}
	refusals := []struct {
		name string
		base []string
		key  secretKey
	}{
		{"no key", base, newSecretKey("")},
		{"a key that was never read", base, secretKey{}},
		{"a base that holds a key already", append(slices.Clone(base), envSessionKey+"=other"), newSecretKey(value)},
		{"a base that holds a vendor variable", append(slices.Clone(base), "CLAUDE_CONFIG_DIR=/x"), newSecretKey(value)},
		{"a base that holds the test variable", append(slices.Clone(base), envTestKey+"="+value), newSecretKey(value)},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			env, err := keyedEnvironment(tt.base, tt.key)
			if err == nil {
				t.Fatalf("it was not refused: %d entries", len(env))
			}
			if strings.Contains(err.Error(), value) {
				t.Error("the refusal names the key")
			}
		})
	}
}

// A clean environment holds no key whatever its parent holds, so no session but the keyed lane can
// receive one.
func TestEnvironmentOfASubscriptionHoldsNoKey(t *testing.T) {
	parent := map[string]string{"HOME": "/h", "PATH": "/p", envSessionKey: "k", envTestKey: "k"}
	got, err := cleanEnvironment(func(name string) (string, bool) { value, ok := parent[name]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range got {
		if strings.Contains(entry, "=k") {
			t.Errorf("a clean environment holds %s", strings.SplitN(entry, "=", 2)[0])
		}
	}
	if _, err := cleanEnvironment(func(string) (string, bool) { return "", false }, envSessionKey+"=k"); err == nil {
		t.Error("a tag passed the key")
	}
}

func TestEnvironmentOfTheBuildHoldsNoKey(t *testing.T) {
	got := withoutKeys([]string{"HOME=/h", envTestKey + "=k", "PATH=/p", envSessionKey + "=k", "ANTHROPIC_API_KEY_OTHER=v"})
	if want := []string{"HOME=/h", "PATH=/p", "ANTHROPIC_API_KEY_OTHER=v"}; !slices.Equal(got, want) {
		t.Errorf("the build's environment %q", got)
	}
}

func TestLeftoverKeyNeverPrints(t *testing.T) {
	const value = "sk-test-value-0123456789"
	key := newSecretKey(value)
	holder := struct {
		name string
		key  secretKey
	}{"keyed", key}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%T %v", "%d"} {
		for _, arg := range []any{key, &key, holder, &holder} {
			if got := fmt.Sprintf(verb, arg); strings.Contains(got, value) || strings.Contains(got, fmt.Sprintf("%x", value)) {
				t.Errorf("%s of %T printed the key", verb, arg)
			}
		}
	}
	if !key.set() || (secretKey{}).set() || newSecretKey("").set() {
		t.Error("set")
	}
}

func TestLeftoverKeyIsFoundWhereverARunLeftIt(t *testing.T) {
	const value = "sk-test-value-0123456789"
	key := newSecretKey(value)
	root := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("stream.log", `{"type":"system","subtype":"init","apiKeySource":"ANTHROPIC_API_KEY"}`)
	write("state/ledger.db", "no key here")
	if got := keyFindings(key, root, []string{"rs15: key: state=acceptance"}, map[string][]byte{"daemon 1": []byte("clean")}); len(got) != 0 {
		t.Fatalf("a clean run reads %q", got)
	}

	write("state/lanes/rs15-key/stderr-1.log", "env "+envSessionKey+"="+value+"\n")
	if err := os.Symlink(filepath.Join(root, "state", "lanes"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	got := keyFindings(key, root, []string{"a line", "a line with " + value}, map[string][]byte{"daemon 1": []byte("x" + value + "x"), "daemon 2": nil})
	want := []string{"a logged line", "daemon 1", "file " + filepath.Join("state", "lanes", "rs15-key", "stderr-1.log")}
	if !slices.Equal(got, want) {
		t.Errorf("findings %q, want %q", got, want)
	}
	for _, finding := range got {
		if strings.Contains(finding, value) || strings.Contains(finding, root) {
			t.Errorf("a finding names the key or a path: %s", finding)
		}
	}

	locked := filepath.Join(root, "locked")
	write("locked", "unread")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
	if got := keyFindings(key, root, nil, nil); !slices.Contains(got, "file locked, which could not be read") {
		t.Errorf("an unreadable file passed: %q", got)
	}
	if got := keyFindings(key, filepath.Join(root, "absent"), nil, nil); len(got) != 1 {
		t.Errorf("a root that cannot be read passed: %q", got)
	}
}

func TestInitOfAKeyedRun(t *testing.T) {
	lane := initLine{KeySource: envSessionKey, Servers: []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}{{Name: "ropeshift", Status: "connected"}}}
	probe := initLine{KeySource: subscriptionSource}
	tests := []struct {
		name       string
		inits      []initLine
		lanesOnKey bool
		failures   int
	}{
		{"a lane on the key and probes on none", []initLine{probe, probe, lane}, true, 0},
		{"a probe on the key", []initLine{{KeySource: envSessionKey}, lane}, true, 1},
		{"a probe on another source", []initLine{{KeySource: "apiKeyHelper"}, lane}, true, 1},
		{"a lane on the subscription", []initLine{probe, func() initLine { l := lane; l.KeySource = subscriptionSource; return l }()}, true, 1},
		{"a dry run's lane on none", []initLine{probe, func() initLine { l := lane; l.KeySource = subscriptionSource; return l }()}, false, 0},
		{"a dry run's probe on the key", []initLine{{KeySource: envSessionKey}}, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyedInitFailures(tt.inits, tt.lanesOnKey); len(got) != tt.failures {
				t.Errorf("failures %q, want %d", got, tt.failures)
			}
		})
	}
	if !laneInit(lane) || laneInit(probe) {
		t.Error("laneInit")
	}
}
