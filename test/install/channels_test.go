//go:build install

package install

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// extractions records each run of tar, so a test can tell whether the install script reached the
// step that extracts an archive.
type extractions struct{ dir, marker string }

func newExtractions(t *testing.T) extractions {
	t.Helper()
	real, err := exec.LookPath("tar")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "tar-ran")
	shim := "#!/bin/sh\n: > '" + marker + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tar"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	return extractions{dir: dir, marker: marker}
}

func (e extractions) happened() bool {
	_, err := os.Stat(e.marker)
	return err == nil
}

// installScript runs install.sh on this machine with a home of its own and tar behind the shim.
func installScript(t *testing.T, tar extractions, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(repoRoot(t), "install.sh")}, args...)...)
	cmd.Env = []string{
		"HOME=" + t.TempDir(), "TMPDIR=" + t.TempDir(), "PATH=" + tar.dir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func TestTheInstallScriptOnThisMachine(t *testing.T) {
	snap := loadSnapshot(t)
	hostPlatform(t)
	prefix := filepath.Join(t.TempDir(), "chosen prefix")
	tar := newExtractions(t)
	out, code := installScript(t, tar, "--from", snap.dir, "--version", snap.version, "--prefix", prefix)
	if code != 0 {
		t.Fatalf("install.sh exited %d:\n%s", code, out)
	}
	t.Logf("install.sh:\n%s", out)
	bin := filepath.Join(prefix, "bin")
	var report strings.Builder
	for _, name := range []string{"ropeshift", "ropeshiftd"} {
		raw, err := exec.Command(filepath.Join(bin, name), "version", "--json").Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		report.WriteString("VERSION " + name + " " + strings.TrimSpace(string(raw)) + "\n")
	}
	assertVersions(t, report.String(), snap.version, true)
	if target, err := os.Readlink(filepath.Join(bin, "ropeshiftd")); err != nil || target != "ropeshift" {
		t.Errorf("ropeshiftd is not a link to ropeshift: %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "share", "doc", "ropeshift", "LICENSE.md")); err != nil {
		t.Errorf("the license was not installed: %v", err)
	}
	assertBuiltUI(t, filepath.Join(bin, "ropeshift"))
	if !tar.happened() {
		t.Error("the shim saw no extraction, so the refusal tests prove nothing")
	}
}

// Each case changes what the script downloads, and the script must stop before it extracts anything.
func TestTheInstallScriptRefusesWhatDoesNotMatchItsChecksum(t *testing.T) {
	snap := loadSnapshot(t)
	goos, goarch := hostPlatform(t)
	name := snap.archive(goos, goarch)
	tests := []struct {
		name   string
		change func(t *testing.T, checksums, archive string)
		says   string
	}{
		{"a modified checksum", func(t *testing.T, checksums, _ string) { editLine(t, checksums, name, flipFirstDigit) }, "checksum"},
		{"a modified archive", func(t *testing.T, _, archive string) { appendTo(t, archive, "tampered") }, "checksum"},
		{"an archive the checksums do not list", func(t *testing.T, checksums, _ string) {
			editLine(t, checksums, name, func(string) string { return "" })
		}, "not listed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := t.TempDir()
			copyFile(t, filepath.Join(snap.dir, "checksums.txt"), filepath.Join(from, "checksums.txt"))
			copyFile(t, filepath.Join(snap.dir, name), filepath.Join(from, name))
			tt.change(t, filepath.Join(from, "checksums.txt"), filepath.Join(from, name))
			prefix := filepath.Join(t.TempDir(), "prefix")
			tar := newExtractions(t)
			out, code := installScript(t, tar, "--from", from, "--version", snap.version, "--prefix", prefix)
			t.Logf("install.sh exited %d:\n%s", code, out)
			if code == 0 {
				t.Fatal("install.sh accepted it")
			}
			if !strings.Contains(out, tt.says) {
				t.Errorf("the refusal does not say %q", tt.says)
			}
			if tar.happened() {
				t.Error("install.sh extracted the archive before refusing it")
			}
			if _, err := os.Stat(filepath.Join(prefix, "bin", "ropeshift")); err == nil {
				t.Error("install.sh installed a binary it refused")
			}
		})
	}
}

func TestTheInstallScriptOnLinux(t *testing.T) {
	snap := loadSnapshot(t)
	arch := containerArch(t)
	script := filepath.Join(repoRoot(t), "install.sh")
	// A bind mount of a missing file would make docker create a directory in its place.
	if _, err := os.Stat(script); err != nil {
		t.Fatal(err)
	}
	// Debian's sh is dash and Alpine's is BusyBox, so the script runs on two shells that are not bash.
	for _, image := range []string{"debian:bookworm-slim", "alpine:3.22"} {
		t.Run(image, func(t *testing.T) {
			c := container{image: image, mounts: []mount{{snap.dir, "/dist"}, {script, "/src/install.sh"}}, env: []string{"HOME=/root"}}
			out, _ := c.run(t, `set -e
sh /src/install.sh --from /dist --version `+snap.version+`
BIN=/root/.local/bin
`+versionScript+`
`+uiScript+`
test "$(readlink "$BIN/ropeshiftd")" = ropeshift && echo 'LINK ok'`, false)
			assertVersions(t, out, snap.version, true)
			assertUILine(t, out)
			if !strings.Contains(out, "LINK ok") {
				t.Errorf("ropeshiftd is not a link to ropeshift:\n%s", out)
			}
		})
	}
	t.Run("a modified checksum on debian", func(t *testing.T) {
		name := snap.archive("linux", arch)
		from := t.TempDir()
		copyFile(t, filepath.Join(snap.dir, "checksums.txt"), filepath.Join(from, "checksums.txt"))
		copyFile(t, filepath.Join(snap.dir, name), filepath.Join(from, name))
		editLine(t, filepath.Join(from, "checksums.txt"), name, flipFirstDigit)
		c := container{image: "debian:bookworm-slim", mounts: []mount{{from, "/dist"}, {script, "/src/install.sh"}}, env: []string{"HOME=/root"}}
		out, code := c.run(t, `sh /src/install.sh --from /dist --version `+snap.version+`; code=$?
test -e /root/.local/bin/ropeshift && echo 'INSTALLED anyway'
exit $code`, true)
		t.Logf("exited %d:\n%s", code, out)
		if code == 0 || !strings.Contains(out, "checksum") || strings.Contains(out, "INSTALLED anyway") {
			t.Error("install.sh did not refuse a modified checksum on Linux")
		}
	})
}

// packageScript installs a package, asks both commands their version, and removes it again. No
// package installs a service: `ropeshift service install` does that on these channels. The license is
// read from the package's own file list, because Debian's slim image skips /usr/share/doc on install.
const packageScript = `
BIN=/usr/bin
` + versionScript + `
` + uiScript + `
test "$(readlink /usr/bin/ropeshiftd)" = ropeshift && echo 'LINK ok'
$CONTENTS | grep -q 'usr/share/doc/ropeshift/LICENSE.md' && echo 'LICENSE ok'
if $LIST ropeshift | grep -q systemd; then echo 'UNIT installed'; else echo 'UNIT none'; fi
$REMOVE ropeshift >/dev/null
test ! -e /usr/bin/ropeshift && test ! -e /usr/bin/ropeshiftd && echo 'REMOVED ok'`

func assertPackage(t *testing.T, out, want string) {
	t.Helper()
	assertVersions(t, out, want, true)
	assertUILine(t, out)
	for _, line := range []string{"LINK ok", "LICENSE ok", "UNIT none", "REMOVED ok"} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q:\n%s", line, out)
		}
	}
}

func TestTheDebianPackage(t *testing.T) {
	snap := loadSnapshot(t)
	deb := filepath.Base(snap.only(t, "*_"+containerArch(t)+".deb"))
	c := container{image: "debian:bookworm-slim", mounts: []mount{{snap.dir, "/dist"}}, env: []string{"LIST=dpkg -L", "REMOVE=dpkg -r", "CONTENTS=dpkg-deb -c /dist/" + deb}}
	out, _ := c.run(t, "set -e\ndpkg -i /dist/"+deb+packageScript, false)
	assertPackage(t, out, snap.version)
}

func TestTheRPMPackage(t *testing.T) {
	snap := loadSnapshot(t)
	rpmArch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[containerArch(t)]
	rpm := filepath.Base(snap.only(t, "*."+rpmArch+".rpm"))
	c := container{image: "fedora:42", mounts: []mount{{snap.dir, "/dist"}}, env: []string{"LIST=rpm -ql", "REMOVE=rpm -e", "CONTENTS=rpm -qlp /dist/" + rpm}}
	out, _ := c.run(t, "set -e\nrpm -i /dist/"+rpm+packageScript, false)
	assertPackage(t, out, snap.version)
}

// The formula is rendered from the snapshot's checksums with file URLs, put in a tap made in the
// container, and installed from there, as `brew install` does from the project's tap.
func TestTheHomebrewFormula(t *testing.T) {
	snap := loadSnapshot(t)
	formula := t.TempDir()
	render := exec.Command("sh", filepath.Join(repoRoot(t), "scripts", "homebrew-formula.sh"),
		filepath.Join(snap.dir, "checksums.txt"), "file:///dist", filepath.Join(formula, "ropeshift.rb"))
	if out, err := render.CombinedOutput(); err != nil {
		t.Fatalf("render the formula: %v\n%s", err, out)
	}
	c := container{
		image:  "homebrew/brew:latest",
		mounts: []mount{{snap.dir, "/dist"}, {formula, "/formula"}},
		env: []string{
			"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1",
			"HOMEBREW_NO_INSTALL_CLEANUP=1", "HOMEBREW_NO_INSTALL_FROM_API=1",
		},
	}
	out, _ := c.run(t, `set -e
brew tap-new --no-git local/ropeshift >/dev/null
cp /formula/ropeshift.rb "$(brew --repository local/ropeshift)/Formula/ropeshift.rb"
brew install --formula local/ropeshift/ropeshift
BIN="$(brew --prefix)/bin"
`+versionScript+`
`+uiScript+`
brew test local/ropeshift/ropeshift && echo 'FORMULA TEST ok'
brew ruby -e 'service = Formula["local/ropeshift/ropeshift"].service; puts service.to_plist; puts service.to_systemd_unit'`, false)
	t.Logf("brew:\n%s", out)
	assertVersions(t, out, snap.version, true)
	assertUILine(t, out)
	for _, want := range []string{"FORMULA TEST ok", "<key>ProcessType</key>", "<string>Interactive</string>", "ropeshiftd", "<string>solo</string>", "ExecStart="} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// A release is public, so its archives must not carry the name of the account that built them.
func TestTheArchivesRecordNoLocalNames(t *testing.T) {
	snap := loadSnapshot(t)
	archives, err := filepath.Glob(filepath.Join(snap.dir, "*.tar.gz"))
	if err != nil || len(archives) != 4 {
		t.Fatalf("want the four archives, found %v (%v)", archives, err)
	}
	for _, path := range archives {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(gz)
		var names []string
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, header.Name)
			if header.Uname != "root" || header.Gname != "root" || header.Uid != 0 || header.Gid != 0 {
				t.Errorf("%s: %s is owned by %s:%s (%d:%d), want root", filepath.Base(path), header.Name, header.Uname, header.Gname, header.Uid, header.Gid)
			}
		}
		_ = f.Close()
		if strings.Join(names, " ") != "LICENSE.md README.md ropeshift" {
			t.Errorf("%s holds %v, want the binary, the license, and the readme", filepath.Base(path), names)
		}
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// editLine rewrites the one line of a checksums file that names file.
func editLine(t *testing.T, path, file string, edit func(string) string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	found := false
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if strings.HasSuffix(line, "  "+file) {
			found = true
			if line = edit(line); line == "" {
				continue
			}
		}
		lines = append(lines, line)
	}
	if !found {
		t.Fatalf("%s lists no %s", path, file)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func flipFirstDigit(line string) string {
	if line[0] == '0' {
		return "1" + line[1:]
	}
	return "0" + line[1:]
}
