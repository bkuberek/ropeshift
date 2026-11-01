package entry

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/questions"
)

func TestDispatchOnStartedName(t *testing.T) {
	tests := []struct {
		name string
		arg0 string
		want string
	}{
		{"the CLI by bare name", "ropeshift", "ropeshift"},
		{"the CLI by path", "/opt/ropeshift/ropeshift", "ropeshift"},
		{"the daemon by link", "/opt/ropeshift/ropeshiftd", "ropeshiftd"},
		{"a renamed binary acts as the CLI", "/tmp/rs", "ropeshift"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Main([]string{tt.arg0, "version", "--json"}, &stdout, &stderr)
			if code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}
			var got versionInfo
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not JSON: %v: %q", err, stdout.String())
			}
			if got.Command != tt.want {
				t.Errorf("command %q, want %q", got.Command, tt.want)
			}
			if got.Go != runtime.Version() || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
				t.Errorf("build fields do not describe this binary: %+v", got)
			}
		})
	}
}

func TestVersionPlainText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Main([]string{"ropeshift", "version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "ropeshift ") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no program name", nil},
		{"no command", []string{"ropeshift"}},
		{"unknown command", []string{"ropeshift", "launch"}},
		{"unknown flag", []string{"ropeshift", "version", "--xml"}},
		{"stray argument", []string{"ropeshift", "version", "now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Main(tt.args, &stdout, &stderr); code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
			if stderr.Len() == 0 {
				t.Error("a usage error wrote nothing to stderr")
			}
		})
	}
}

// TestOneBinaryTwoNames builds the CLI with no cgo and starts it through a link,
// as a release package does.
func TestOneBinaryTwoNames(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real binary")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "ropeshift")
	build := exec.Command("go", "build", "-o", binary, "ropeshift.dev/ropeshift/cmd/ropeshift")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	link := filepath.Join(dir, "ropeshiftd")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{binary: "ropeshift", link: "ropeshiftd"} {
		out, err := exec.Command(path, "version", "--json").Output()
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		var got versionInfo
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: %v: %q", want, err, out)
		}
		if got.Command != want {
			t.Errorf("started as %s, ran as %s", want, got.Command)
		}
	}
}

func TestBatchLimitsComeFromTheUserLayer(t *testing.T) {
	dir := t.TempDir()
	paths := api.Paths{StateDir: dir}
	tests := []struct {
		name    string
		profile string
		want    questions.BatchPolicy
	}{
		{"no layer leaves the built-in limits", "", questions.BatchPolicy{WindowMs: 15 * 60_000, MaxWaitMs: 5 * 60_000}},
		{"the user layer sets them", "[lifecycle]\nhuman_batch_window = \"1h\"\nhuman_batch_max_wait = \"10m\"\n", questions.BatchPolicy{WindowMs: 3_600_000, MaxWaitMs: 600_000}},
		{"a layer that does not parse leaves zero, which means the defaults", "[lifecycle", questions.BatchPolicy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, profiles.UserFile), []byte(tt.profile), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := soloBatches(paths); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTheDefaultLimitsLetABlockingQuestionHurry holds the rule the two limits exist for: a
// fixed window leaves a blocking question nothing to shorten unless its wait is the shorter
// limit.
func TestTheDefaultLimitsLetABlockingQuestionHurry(t *testing.T) {
	limits := soloBatches(api.Paths{StateDir: t.TempDir()}).Filled()
	if limits.MaxWaitMs >= limits.WindowMs {
		t.Fatalf("limits %+v: a blocking question could never close a batch early", limits)
	}
}
