//go:build install

package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRelease is the version the release step tags, in a scratch clone only.
const testRelease = "v0.0.1-rc.1"

// git runs git in dir and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// scratchClone copies this repository's committed history into a temporary directory with no hooks
// and no signing, so nothing the release step does there can reach this repository or any other. A
// tag build reads the origin's URL, so origin names a host in the reserved .invalid domain, which never
// resolves.
//
// ROPESHIFT_RELEASE_SOURCE names another repository to clone in place of this one, such as a copy
// with a deliberate defect, to prove that a test fails.
func scratchClone(t *testing.T) (string, string) {
	t.Helper()
	source := os.Getenv("ROPESHIFT_RELEASE_SOURCE")
	if source == "" {
		source = repoRoot(t)
	}
	dir := filepath.Join(t.TempDir(), "ropeshift")
	git(t, "", "clone", "--quiet", "--no-hardlinks", source, dir)
	git(t, dir, "remote", "set-url", "origin", "https://unreachable.invalid/ropeshift/ropeshift.git")
	for _, kv := range [][2]string{
		{"user.name", "Release Test"}, {"user.email", "release-test@example.invalid"},
		{"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"tag.forceSignAnnotated", "false"},
		{"core.hooksPath", t.TempDir()},
	} {
		git(t, dir, "config", "--local", kv[0], kv[1])
	}
	return dir, git(t, dir, "symbolic-ref", "--short", "HEAD")
}

func releaseScript(t *testing.T, dir, branch, version string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", "scripts/release.sh", version)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ROPESHIFT_RELEASE_BRANCH="+branch)
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

// TestTheReleaseStep proves D28 in a scratch clone: a plain checkout builds with no Node.js and
// embeds the placeholder, the release step refuses what the policy forbids, its commit adds the built
// UI on the tag and nowhere else, and go install at that tag, through a module proxy made of files,
// gets the real UI and reports the tag's version.
func TestTheReleaseStep(t *testing.T) {
	dir, branch := scratchClone(t)
	head := git(t, dir, "rev-parse", "HEAD")

	t.Run("a plain checkout builds with no Node.js", func(t *testing.T) {
		bin := t.TempDir()
		for _, tool := range []string{"go", "git"} {
			path, err := exec.LookPath(tool)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
		binary := filepath.Join(t.TempDir(), "ropeshift")
		build := exec.Command("go", "build", "-o", binary, "./cmd/ropeshift")
		build.Dir = dir
		build.Env = []string{
			"PATH=" + bin, "HOME=" + os.Getenv("HOME"), "GOCACHE=" + goEnv(t, "GOCACHE"),
			"GOMODCACHE=" + goEnv(t, "GOMODCACHE"), "GOROOT=" + goEnv(t, "GOROOT"),
			"GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "GOTOOLCHAIN=local",
		}
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build with only go and git on PATH: %v\n%s", err, out)
		}
		raw, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), placeholderUI) || strings.Contains(string(raw), builtUI) {
			t.Error("a plain checkout does not embed the placeholder alone")
		}
		out, err := exec.Command(binary, "version", "--json").Output()
		if err != nil || !strings.Contains(string(out), `"version":"dev"`) {
			t.Errorf("a plain build reports %s (%v), want a development version", out, err)
		}
	})

	t.Run("the release step refuses", func(t *testing.T) {
		git(t, dir, "tag", "v0.0.1-beta.1")
		tests := []struct {
			name, version, branch, says string
			dirty                       bool
		}{
			{"a version with no v", "0.1.0", branch, "vMAJOR.MINOR.PATCH", false},
			{"a version with two parts", "v0.1", branch, "vMAJOR.MINOR.PATCH", false},
			{"a pre-release the policy does not name", "v0.1.0-snapshot.1", branch, "vMAJOR.MINOR.PATCH", false},
			{"build metadata", "v0.1.0+build.1", branch, "vMAJOR.MINOR.PATCH", false},
			{"a leading zero", "v0.01.0", branch, "vMAJOR.MINOR.PATCH", false},
			{"a pre-release numbered from zero", "v0.1.0-rc.0", branch, "vMAJOR.MINOR.PATCH", false},
			{"a tag that exists", "v0.0.1-beta.1", branch, "exists", false},
			{"another branch", testRelease, "main", "main", false},
			{"a tree with changes", testRelease, branch, "changes", true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tt.dirty {
					appendTo(t, filepath.Join(dir, "README.md"), "\nchanged\n")
					t.Cleanup(func() { git(t, dir, "checkout", "--", "README.md") })
				}
				out, code := releaseScript(t, dir, tt.branch, tt.version)
				t.Logf("exited %d: %s", code, strings.TrimSpace(out))
				if code == 0 || !strings.Contains(out, tt.says) {
					t.Errorf("want a refusal that says %q", tt.says)
				}
				if git(t, dir, "rev-parse", "HEAD") != head || git(t, dir, "tag", "--list", testRelease) != "" {
					t.Error("a refused release changed the repository")
				}
			})
		}
		git(t, dir, "tag", "--delete", "v0.0.1-beta.1")
	})

	// A tag on a commit with no built UI would give go install the placeholder, so its build stops.
	t.Run("a tag build refuses a commit with no built UI", func(t *testing.T) {
		const bare = "v0.0.1-alpha.1"
		git(t, dir, "tag", bare)
		defer git(t, dir, "tag", "--delete", bare)
		git(t, dir, "checkout", "--quiet", bare)
		defer git(t, dir, "checkout", "--quiet", branch)
		out, err := goreleaserFromTag(dir, bare, "").CombinedOutput()
		t.Logf("goreleaser: %v\n%s", err, out)
		if err == nil || !strings.Contains(string(out), "holds no built web UI") {
			t.Error("a tag build of a commit with no built UI did not stop")
		}
	})

	t.Run("the release commit holds the built UI on the tag only", func(t *testing.T) {
		out, code := releaseScript(t, dir, branch, testRelease)
		t.Logf("release.sh exited %d:\n%s", code, out)
		if code != 0 {
			t.FailNow()
		}
		if kind := git(t, dir, "cat-file", "-t", testRelease); kind != "tag" {
			t.Errorf("the tag is a %s, want an annotated tag", kind)
		}
		if parent := git(t, dir, "rev-parse", testRelease+"^{commit}^"); parent != head {
			t.Errorf("the release commit's parent is %s, want the branch head %s", parent, head)
		}
		if subject := git(t, dir, "log", "-1", "--format=%s", testRelease+"^{commit}"); subject != "chore(release): "+testRelease {
			t.Errorf("release commit subject %q", subject)
		}
		tagged := git(t, dir, "ls-tree", "-r", "--name-only", testRelease, "--", "web/static")
		if !strings.Contains(tagged, "web/static/index.html") || !strings.Contains(tagged, "web/static/assets/") {
			t.Errorf("the tag does not hold the built UI:\n%s", tagged)
		}
		if got := git(t, dir, "symbolic-ref", "--short", "HEAD"); got != branch {
			t.Errorf("the release step left the clone on %s, want %s", got, branch)
		}
		if git(t, dir, "rev-parse", "HEAD") != head {
			t.Error("the release commit landed on the branch")
		}
		if tracked := git(t, dir, "ls-files", "web/static"); tracked != "web/static/placeholder.html" {
			t.Errorf("the branch tracks %q under web/static", tracked)
		}
		if status := git(t, dir, "status", "--porcelain"); status != "" {
			t.Errorf("the release step left changes:\n%s", status)
		}
	})

	t.Run("go install at the tag gets the real UI", func(t *testing.T) {
		proxy := moduleProxy(t, dir, testRelease)
		c := container{
			image:  "golang:1.27",
			mounts: []mount{{proxy, "/proxy"}, {filepath.Join(goEnv(t, "GOMODCACHE"), "cache", "download"), "/modcache"}},
			env:    []string{"GOPROXY=file:///proxy,file:///modcache", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0"},
		}
		out, _ := c.run(t, `set -e
go install ropeshift.dev/ropeshift/cmd/...@`+testRelease+`
BIN=/go/bin
`+versionScript+`
`+uiScript, false)
		t.Logf("go install:\n%s", out)
		assertVersions(t, out, testRelease, false)
		assertUILine(t, out)
	})

	// GoReleaser writes no release notes for a snapshot, so the notes come from tag builds here. The
	// first release has no earlier tag, so its notes cover the whole history.
	t.Run("the first release's notes cover the history by type", func(t *testing.T) {
		notes := tagBuild(t, dir, branch, testRelease, "")
		assertSections(t, notes, map[string]string{
			"### Features": "run the daemon as a launchd agent or a systemd user unit",
			"### Fixes":    "keep a problem an accepted plan's commit found until a later commit fixes the file",
			"### Others":   "add make linux-bundle, the Linux check for a machine with no Go",
		})
		for _, absent := range []string{"docs:", "test:", "chore(release)", "start the pty helper's program"} {
			if strings.Contains(notes, absent) {
				t.Errorf("the notes hold %q", absent)
			}
		}
	})

	// A later release's notes hold only the user-facing commits since the release before it.
	t.Run("a later release's notes start at the release before it", func(t *testing.T) {
		const second = "v0.0.1-rc.2"
		git(t, dir, "commit", "--quiet", "--allow-empty", "--message", "feat: a change after the first release")
		git(t, dir, "commit", "--quiet", "--allow-empty", "--message", "docs: a change the notes leave out")
		notes := tagBuild(t, dir, branch, second, testRelease)
		assertSections(t, notes, map[string]string{"### Features": "a change after the first release"})
		for _, absent := range []string{"a change the notes leave out", "chore(release)", "run the daemon as a launchd agent"} {
			if strings.Contains(notes, absent) {
				t.Errorf("the notes hold %q", absent)
			}
		}
	})
}

// tagBuild cuts a release with the release step and builds it from its tag as a maintainer does, with
// the command the step prints, publishing skipped, and no token in the environment. It checks the
// build's binary for this machine and returns the release notes.
func tagBuild(t *testing.T, dir, branch, tag, previous string) string {
	t.Helper()
	if git(t, dir, "tag", "--list", tag) == "" {
		out, code := releaseScript(t, dir, branch, tag)
		if code != 0 {
			t.Fatalf("release.sh exited %d:\n%s", code, out)
		}
		if previous != "" && !strings.Contains(out, "GORELEASER_PREVIOUS_TAG="+previous) {
			t.Errorf("the release step does not name the previous release:\n%s", out)
		}
	}
	git(t, dir, "checkout", "--quiet", tag)
	defer git(t, dir, "checkout", "--quiet", branch)
	if out, err := goreleaserFromTag(dir, tag, previous).CombinedOutput(); err != nil {
		t.Fatalf("goreleaser release from %s: %v\n%s", tag, err, out)
	}
	goos, goarch := hostPlatform(t)
	binaries, _ := filepath.Glob(filepath.Join(dir, "dist", "ropeshift_"+goos+"_"+goarch+"*", "ropeshift"))
	if len(binaries) != 1 {
		t.Fatalf("no binary for this machine in the build of %s: %v", tag, binaries)
	}
	raw, err := exec.Command(binaries[0], "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var got versionOf
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != tag || got.Modified || got.Commit != git(t, dir, "rev-parse", tag+"^{commit}") || got.Date == "" {
		t.Errorf("the build of %s reports %+v, want its clean release commit", tag, got)
	}
	assertBuiltUI(t, binaries[0])
	notes, err := os.ReadFile(filepath.Join(dir, "dist", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("release notes of %s:\n%s", tag, notes)
	return string(notes)
}

// goreleaserFromTag is the release build a maintainer runs at tag, with publishing skipped and no token
// in its environment.
func goreleaserFromTag(dir, tag, previous string) *exec.Cmd {
	build := exec.Command("goreleaser", "release", "--clean", "--skip=publish,announce")
	build.Dir = dir
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !strings.HasSuffix(name, "_TOKEN") && !strings.HasPrefix(name, "GORELEASER_") {
			build.Env = append(build.Env, kv)
		}
	}
	build.Env = append(build.Env, "GORELEASER_CURRENT_TAG="+tag)
	if previous != "" {
		build.Env = append(build.Env, "GORELEASER_PREVIOUS_TAG="+previous)
	}
	return build
}

// assertSections checks that each entry is listed under its heading.
func assertSections(t *testing.T, notes string, entries map[string]string) {
	t.Helper()
	for heading, entry := range entries {
		at := strings.Index(notes, heading+"\n")
		if at < 0 {
			t.Errorf("the notes have no %q section", heading)
			continue
		}
		section := notes[at+len(heading):]
		if next := strings.Index(section, "\n### "); next >= 0 {
			section = section[:next]
		}
		if !strings.Contains(section, entry) {
			t.Errorf("%q does not list %q", heading, entry)
		}
	}
}

// moduleProxy lays out a GOPROXY of files that serves the module at version, from the tag's tree.
func moduleProxy(t *testing.T, repo, version string) string {
	t.Helper()
	proxy := t.TempDir()
	at := filepath.Join(proxy, "ropeshift.dev", "ropeshift", "@v")
	if err := os.MkdirAll(at, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "archive", "--format=zip", "--prefix=ropeshift.dev/ropeshift@"+version+"/",
		"--output="+filepath.Join(at, version+".zip"), version)
	files := map[string]string{
		"list":            version + "\n",
		version + ".mod":  git(t, repo, "show", version+":go.mod") + "\n",
		version + ".info": `{"Version":"` + version + `","Time":"` + git(t, repo, "log", "-1", "--format=%cI", version) + `"}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(at, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return proxy
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
