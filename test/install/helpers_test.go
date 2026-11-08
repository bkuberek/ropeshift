//go:build install

// Package install installs a local snapshot through every channel a release offers, each in a clean
// environment, and asks the installed binary its version. `make install-test` builds the snapshot
// with `make snapshot` and runs it; ROPESHIFT_DIST names a snapshot built earlier. The Linux channels
// run in containers, and each container is removed by its exact ID.
package install

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// builtUI is in the page a web build writes. The placeholder page stays beside that page in every
// build, and the binary serves the built page whenever it holds one, so a binary that holds builtUI
// serves the real UI.
const builtUI = `src="/assets/index-`

// placeholderUI is in the placeholder page only.
const placeholderUI = "built from a tree with no web build"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// snapshot is the GoReleaser output under test.
type snapshot struct {
	dir     string
	version string // as the binary reports it, with the leading v
}

func loadSnapshot(t *testing.T) snapshot {
	t.Helper()
	dir := os.Getenv("ROPESHIFT_DIST")
	if dir == "" {
		dir = filepath.Join(repoRoot(t), "dist")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatalf("no snapshot at %s, run make snapshot first: %v", dir, err)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Version == "" {
		t.Fatalf("metadata.json names no version: %v", err)
	}
	return snapshot{dir: dir, version: "v" + meta.Version}
}

// archive is the snapshot's archive for one platform.
func (s snapshot) archive(goos, goarch string) string {
	return "ropeshift_" + strings.TrimPrefix(s.version, "v") + "_" + goos + "_" + goarch + ".tar.gz"
}

func (s snapshot) only(t *testing.T, pattern string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(s.dir, pattern))
	if err != nil || len(matches) != 1 {
		t.Fatalf("want one %s in %s, found %v (%v)", pattern, s.dir, matches, err)
	}
	return matches[0]
}

// containerArch is the architecture the container engine runs natively.
func containerArch(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Arch}}").Output()
	if err != nil {
		t.Fatalf("docker is not available: %v", err)
	}
	switch arch := strings.TrimSpace(string(out)); arch {
	case "arm64", "aarch64":
		return "arm64"
	case "amd64", "x86_64":
		return "amd64"
	default:
		t.Fatalf("no release targets the container engine's architecture %q", arch)
		return ""
	}
}

type mount struct{ host, container string }

type container struct {
	image   string
	mounts  []mount
	env     []string
	user    string
	network bool
}

// run starts script under sh in a new container and returns its combined output. The container is
// created, started, and removed by the ID docker printed for it. A script that exits non-zero fails
// the test unless allowFailure is set, and the exit code is returned either way.
func (c container) run(t *testing.T, script string, allowFailure bool) (string, int) {
	t.Helper()
	args := []string{"create"}
	if !c.network {
		args = append(args, "--network", "none")
	}
	if c.user != "" {
		args = append(args, "--user", c.user)
	}
	for _, m := range c.mounts {
		args = append(args, "--volume", m.host+":"+m.container+":ro")
	}
	for _, e := range c.env {
		args = append(args, "--env", e)
	}
	args = append(args, "--entrypoint", "/bin/sh", c.image, "-c", script)
	created, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker create %s: %v", c.image, err)
	}
	id := strings.TrimSpace(string(created))
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "--force", id).CombinedOutput(); err != nil {
			t.Errorf("remove container %s: %v: %s", id, err, out)
		}
	})
	start := exec.Command("docker", "start", "--attach", id)
	var out bytes.Buffer
	start.Stdout, start.Stderr = &out, &out
	err = start.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("docker start %s: %v", id, err)
	}
	if code != 0 && !allowFailure {
		t.Fatalf("%s exited %d:\n%s", c.image, code, out.String())
	}
	return out.String(), code
}

// versionOf is what one installed command reports, read from a "VERSION <command> <json>" line a
// script printed.
type versionOf struct {
	Command  string `json:"command"`
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Modified bool   `json:"modified"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

const versionScript = `for c in ropeshift ropeshiftd; do printf 'VERSION %s %s\n' "$c" "$("$BIN/$c" version --json)"; done`

// uiScript prints whether the installed binary embeds the built web UI or the placeholder.
const uiScript = `if grep -q 'src="/assets/index-' "$BIN/ropeshift"; then echo 'UI built'; else echo 'UI placeholder'; fi`

func assertUILine(t *testing.T, output string) {
	t.Helper()
	if !strings.Contains(output, "UI built") {
		t.Errorf("the installed binary does not embed the built web UI:\n%s", output)
	}
}

// assertVersions checks that both commands ran, each under its own name, and report want. A binary
// GoReleaser built also carries the commit and build date of its stamp.
func assertVersions(t *testing.T, output, want string, stamped bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		rest, ok := strings.CutPrefix(line, "VERSION ")
		if !ok {
			continue
		}
		name, raw, _ := strings.Cut(rest, " ")
		var got versionOf
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Errorf("%s printed no JSON: %v: %q", name, err, raw)
			continue
		}
		if got.Command != name || got.Version != want {
			t.Errorf("%s reports %+v, want command %s at %s", name, got, name, want)
		}
		if stamped && (got.Commit == "" || got.Date == "") {
			t.Errorf("%s carries no release stamp: %+v", name, got)
		}
		seen[name] = true
	}
	if !seen["ropeshift"] || !seen["ropeshiftd"] {
		t.Fatalf("not both commands reported a version:\n%s", output)
	}
	t.Logf("both commands report %s", want)
}

func assertBuiltUI(t *testing.T, binary string) {
	t.Helper()
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(builtUI)) {
		t.Errorf("%s does not embed the built web UI", binary)
	}
}

func hostPlatform(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("no release targets %s", runtime.GOOS)
	}
	return runtime.GOOS, runtime.GOARCH
}
