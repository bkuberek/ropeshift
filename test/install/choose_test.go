//go:build install

package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The URLs install.sh reads with no mirror named: GitHub's list of releases, and each release's files.
const (
	releasesURL = "https://api.github.com/repos/bkuberek/ropeshift/releases?per_page=100"
	downloadURL = "https://github.com/bkuberek/ropeshift/releases/download/"
)

// listed is one release as GitHub's API lists it, with its keys in the API's order. A name and notes
// that quote the keys the script reads prove that it reads keys and not text.
type listed struct {
	URL        string      `json:"url"`
	HTMLURL    string      `json:"html_url"`
	ID         int         `json:"id"`
	Author     listedUser  `json:"author"`
	TagName    string      `json:"tag_name"`
	Target     string      `json:"target_commitish"`
	Name       string      `json:"name"`
	Draft      bool        `json:"draft"`
	Prerelease bool        `json:"prerelease"`
	CreatedAt  string      `json:"created_at"`
	Assets     []listedRef `json:"assets"`
	Body       string      `json:"body"`
}

type listedUser struct {
	Login string `json:"login"`
	ID    int    `json:"id"`
	Type  string `json:"type"`
}

type listedRef struct {
	Name     string     `json:"name"`
	Uploader listedUser `json:"uploader"`
	Size     int        `json:"size"`
}

func release(tag string, prerelease bool) listed {
	bot := listedUser{Login: "github-actions[bot]", ID: 41898282, Type: "Bot"}
	return listed{
		URL: "https://api.github.com/repos/bkuberek/ropeshift/releases/1", HTMLURL: "https://github.com/bkuberek/ropeshift/releases/tag/" + tag,
		ID: 1, Author: bot, TagName: tag, Target: "main", Name: `tag_name`, Prerelease: prerelease, CreatedAt: "2026-11-08T10:00:00Z",
		Assets: []listedRef{{Name: "checksums.txt", Uploader: bot, Size: 1}, {Name: "ropeshift_" + strings.TrimPrefix(tag, "v") + "_linux_arm64.tar.gz", Uploader: bot, Size: 1}},
		Body:   "Notes, with \"tag_name\": \"v99.0.0\", \"prerelease\": false, \"draft\": false, {braces}, and [brackets].\nA second line.",
	}
}

func draft(tag string) listed {
	r := release(tag, false)
	r.Draft = true
	return r
}

// releaseServer is a directory of release files and the curl that serves them, put first on PATH, so
// the script runs with no network. The fake curl serves only the routes a test adds, logs every URL it
// was asked for, and answers any other URL as a 404 does under --fail.
type releaseServer struct {
	dir, bin, routes, log string
}

func newReleaseServer(t *testing.T) releaseServer {
	t.Helper()
	s := releaseServer{dir: t.TempDir(), bin: t.TempDir()}
	s.routes = filepath.Join(s.dir, "routes")
	s.log = filepath.Join(s.dir, "requests")
	if err := os.WriteFile(s.routes, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	curl := `#!/bin/sh
out= url=
while [ $# -gt 0 ]; do
	case $1 in
	--output) out=$2; shift 2 ;;
	--proto) shift 2 ;;
	-*) shift ;;
	*) url=$1; shift ;;
	esac
done
printf '%s\n' "$url" >>'` + s.log + `'
[ -n "$out" ] || { echo 'the fake curl serves files with --output only' >&2; exit 2; }
while read -r route file; do
	if [ "$route" = "$url" ]; then
		cp "$file" "$out"
		exit 0
	fi
done <'` + s.routes + `'
echo "curl: (22) The requested URL returned error: 404" >&2
exit 22
`
	if err := os.WriteFile(filepath.Join(s.bin, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s releaseServer) serve(t *testing.T, url string, content []byte) {
	t.Helper()
	key := sha256.Sum256([]byte(url))
	file := filepath.Join(s.dir, "file-"+hex.EncodeToString(key[:8]))
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}
	appendTo(t, s.routes, url+" "+file+"\n")
}

// list serves GitHub's list of releases, pretty-printed as the API sends it or compact.
func (s releaseServer) list(t *testing.T, releases []listed, compact bool) {
	t.Helper()
	if releases == nil {
		releases = []listed{}
	}
	var raw []byte
	var err error
	if compact {
		raw, err = json.Marshal(releases)
	} else {
		raw, err = json.MarshalIndent(releases, "", "  ")
	}
	if err != nil {
		t.Fatal(err)
	}
	s.serve(t, releasesURL, raw)
}

// publish serves one release's checksums and its archive for this machine. The archive's ropeshift is
// a script that reports the release's version, so the install shows which release it installed.
// tamper changes the archive after its checksum is listed.
func (s releaseServer) publish(t *testing.T, tag string, tamper bool) {
	t.Helper()
	goos, goarch := hostPlatform(t)
	name := "ropeshift_" + strings.TrimPrefix(tag, "v") + "_" + goos + "_" + goarch + ".tar.gz"
	archive := fakeArchive(t, "#!/bin/sh\nprintf 'ropeshift %s (a fake release)\\n' '"+tag+"'\n")
	sum := sha256.Sum256(archive)
	s.serve(t, downloadURL+tag+"/checksums.txt", []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"))
	if tamper {
		archive = append(archive, []byte("tampered")...)
	}
	s.serve(t, downloadURL+tag+"/"+name, archive)
}

func (s releaseServer) requests(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(s.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func fakeArchive(t *testing.T, program string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{{"LICENSE.md", "license\n", 0o644}, {"README.md", "readme\n", 0o644}, {"ropeshift", program, 0o755}} {
		if err := w.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// shells are the POSIX shells on this machine that the script must run on: sh, and dash, which is
// Debian's sh, when it is installed.
func shells() []string {
	found := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		found = append(found, "dash")
	}
	return found
}

// runAgainst runs install.sh under shell with the server's curl and the tar shim first on PATH.
func runAgainst(t *testing.T, shell string, s releaseServer, tar extractions, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(shell, append([]string{filepath.Join(repoRoot(t), "install.sh")}, args...)...)
	cmd.Env = []string{
		"HOME=" + t.TempDir(), "TMPDIR=" + t.TempDir(),
		"PATH=" + s.bin + string(os.PathListSeparator) + tar.dir + string(os.PathListSeparator) + os.Getenv("PATH"),
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

// With no --version, the script installs the newest stable release, or the newest pre-release when no
// release is stable, by version precedence and whatever order the list holds, and says which it chose.
func TestTheInstallScriptChoosesTheNewestRelease(t *testing.T) {
	hostPlatform(t)
	tests := []struct {
		name     string
		releases []listed
		want     string
		kind     string
	}{
		{"only pre-releases, numbered past nine", []listed{
			release("v0.1.0-alpha.2", true), release("v0.1.0-alpha.10", true), release("v0.1.0-alpha.9", true),
		}, "v0.1.0-alpha.10", "pre-release"},
		{"pre-releases of every stage", []listed{
			release("v0.2.0-alpha.3", true), release("v0.2.0-rc.1", true), release("v0.2.0-beta.2", true), release("v0.1.0-rc.4", true),
		}, "v0.2.0-rc.1", "pre-release"},
		{"stable releases beside newer pre-releases", []listed{
			release("v0.11.0-alpha.1", true), release("v0.9.0", false), release("v0.10.0", false), release("v0.2.0", false), release("v0.10.1-rc.1", true),
		}, "v0.10.0", "stable"},
		{"a draft is passed over", []listed{draft("v0.5.0"), release("v0.4.0-alpha.1", true)}, "v0.4.0-alpha.1", "pre-release"},
		{"a tag outside the versioning policy is passed over", []listed{
			release("v9.0.0-preview.1", true), release("nightly", true), release("v0.1", false), release("v0.1.0-alpha.1", true),
		}, "v0.1.0-alpha.1", "pre-release"},
	}
	for _, shell := range shells() {
		for _, tt := range tests {
			for _, compact := range []bool{false, true} {
				encoding := "pretty"
				if compact {
					encoding = "compact"
				}
				t.Run(shell+"/"+tt.name+"/"+encoding, func(t *testing.T) {
					s := newReleaseServer(t)
					s.list(t, tt.releases, compact)
					for _, r := range tt.releases {
						s.publish(t, r.TagName, false)
					}
					prefix := filepath.Join(t.TempDir(), "prefix")
					tar := newExtractions(t)
					out, code := runAgainst(t, shell, s, tar, "--prefix", prefix)
					t.Logf("install.sh exited %d:\n%s", code, out)
					if code != 0 {
						t.FailNow()
					}
					if !strings.Contains(out, "installing "+tt.want+", the newest "+tt.kind) {
						t.Errorf("the script does not say it chose %s, the newest %s", tt.want, tt.kind)
					}
					if !strings.Contains(out, "installed ropeshift "+tt.want+" ") {
						t.Errorf("the script did not install %s", tt.want)
					}
					got, err := exec.Command(filepath.Join(prefix, "bin", "ropeshift")).Output()
					if err != nil || !strings.HasPrefix(string(got), "ropeshift "+tt.want+" ") {
						t.Errorf("the installed binary reports %q (%v), want %s", got, err, tt.want)
					}
					want := []string{releasesURL, downloadURL + tt.want + "/checksums.txt"}
					if requests := s.requests(t); len(requests) != 3 || requests[0] != want[0] || requests[1] != want[1] || !strings.HasPrefix(requests[2], downloadURL+tt.want+"/") {
						t.Errorf("the script asked for %q, want the list, then %s's checksums and archive", requests, tt.want)
					}
				})
			}
		}
	}
}

// When no release can be chosen, or the chosen one fails its checksum, the script stops before it
// extracts or installs anything.
func TestTheInstallScriptStopsWhenNoReleaseCanBeChosen(t *testing.T) {
	hostPlatform(t)
	tests := []struct {
		name  string
		setup func(t *testing.T, s releaseServer)
		says  string
	}{
		{"no release is published", func(t *testing.T, s releaseServer) { s.list(t, nil, false) }, "no published release"},
		{"only drafts and other tags", func(t *testing.T, s releaseServer) {
			s.list(t, []listed{draft("v0.1.0"), release("nightly", true)}, false)
		}, "no published release"},
		{"the list cannot be read", func(t *testing.T, s releaseServer) {}, "--version"},
		{"the chosen release fails its checksum", func(t *testing.T, s releaseServer) {
			s.list(t, []listed{release("v0.1.0-alpha.1", true)}, false)
			s.publish(t, "v0.1.0-alpha.1", true)
		}, "checksum mismatch"},
	}
	for _, shell := range shells() {
		for _, tt := range tests {
			t.Run(shell+"/"+tt.name, func(t *testing.T) {
				s := newReleaseServer(t)
				tt.setup(t, s)
				prefix := filepath.Join(t.TempDir(), "prefix")
				tar := newExtractions(t)
				out, code := runAgainst(t, shell, s, tar, "--prefix", prefix)
				t.Logf("install.sh exited %d:\n%s", code, out)
				if code == 0 {
					t.Fatal("install.sh succeeded")
				}
				if !strings.Contains(out, tt.says) {
					t.Errorf("the refusal does not say %q", tt.says)
				}
				if tar.happened() {
					t.Error("install.sh extracted an archive before it stopped")
				}
				if _, err := os.Stat(filepath.Join(prefix, "bin", "ropeshift")); err == nil {
					t.Error("install.sh installed a binary")
				}
			})
		}
	}
}

// A named version is downloaded directly, and the list of releases is never read.
func TestANamedVersionReadsNoReleaseList(t *testing.T) {
	hostPlatform(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			s := newReleaseServer(t)
			s.publish(t, "v0.1.0-alpha.1", false)
			prefix := filepath.Join(t.TempDir(), "prefix")
			out, code := runAgainst(t, shell, s, newExtractions(t), "--version", "v0.1.0-alpha.1", "--prefix", prefix)
			t.Logf("install.sh exited %d:\n%s", code, out)
			if code != 0 || !strings.Contains(out, "installed ropeshift v0.1.0-alpha.1 ") {
				t.Fatal("install.sh did not install the named version")
			}
			for _, url := range s.requests(t) {
				if !strings.HasPrefix(url, downloadURL+"v0.1.0-alpha.1/") {
					t.Errorf("install.sh asked for %s", url)
				}
			}
		})
	}
}
