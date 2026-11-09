//go:build install

package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRelease is the version the release step tags, in a scratch clone only.
const testRelease = "v0.0.1-rc.1"

// releaseFeature is the entry the simulated release pull request adds to CHANGELOG.md.
const releaseFeature = "a feature the release pull request lists"

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
// resolves. The clone's branch is main, whatever this checkout is on, because a release is tagged on
// main and a pull request is checked out as a detached merge commit.
//
// The clone takes no tags. The tests make every tag they read, so no release this repository has
// published can change what a test sees.
//
// ROPESHIFT_RELEASE_SOURCE names another repository to clone in place of this one, such as a copy
// with a deliberate defect, to prove that a test fails.
func scratchClone(t *testing.T) string {
	t.Helper()
	source := os.Getenv("ROPESHIFT_RELEASE_SOURCE")
	if source == "" {
		source = repoRoot(t)
	}
	dir := filepath.Join(t.TempDir(), "ropeshift")
	git(t, "", "clone", "--quiet", "--no-hardlinks", "--no-tags", source, dir)
	git(t, dir, "remote", "set-url", "origin", "https://unreachable.invalid/ropeshift/ropeshift.git")
	for _, kv := range [][2]string{
		{"user.name", "Release Test"}, {"user.email", "release-test@example.invalid"},
		{"commit.gpgsign", "false"}, {"tag.gpgsign", "false"}, {"tag.forceSignAnnotated", "false"},
		{"core.hooksPath", t.TempDir()},
	} {
		git(t, dir, "config", "--local", kv[0], kv[1])
	}
	git(t, dir, "checkout", "--quiet", "-B", "main")
	return dir
}

// mergeReleasePullRequest does in the scratch clone what merging release-please's pull request does on
// GitHub. A commit on release-please's branch sets the manifest to version and, when section is set,
// adds the version's section to CHANGELOG.md, and main merges that branch with a merge commit, which it
// returns.
func mergeReleasePullRequest(t *testing.T, dir, version string, section bool) string {
	t.Helper()
	plain := strings.TrimPrefix(version, "v")
	git(t, dir, "switch", "--quiet", "--force-create", "release-please--branches--main", "main")
	manifest := "{\n  \".\": \"" + plain + "\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, ".release-please-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if section {
		path := filepath.Join(dir, "CHANGELOG.md")
		changelog, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		entry := "## [" + plain + "](https://github.com/bkuberek/ropeshift/compare/v0.0.1-rc.0...v" + plain + ") (2026-11-08)\n\n\n" +
			"### Features\n\n* " + releaseFeature + "\n\n"
		before, after, found := strings.Cut(string(changelog), "\n## ")
		updated := before + "\n" + entry + "## " + after
		if !found {
			updated = string(changelog) + "\n" + entry
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "commit", "--quiet", "--all", "--message", "chore: release "+plain)
	git(t, dir, "switch", "--quiet", "main")
	git(t, dir, "merge", "--quiet", "--no-ff", "--message", "Merge pull request #2 from bkuberek/release-please--branches--main",
		"release-please--branches--main")
	return git(t, dir, "rev-parse", "HEAD")
}

// releaseScript runs the release step and returns its output, its exit code, and the files it left in
// its temporary directory, which is where it writes the release notes. A tool's cache there is not one
// of them.
func releaseScript(t *testing.T, dir, version string) (string, int, []string) {
	t.Helper()
	tmp := t.TempDir()
	cmd := exec.Command("sh", "scripts/release.sh", version)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	out, err := cmd.CombinedOutput()
	entries, readErr := os.ReadDir(tmp)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var left []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ropeshift-") {
			left = append(left, filepath.Join(tmp, e.Name()))
		}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode(), left
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0, left
}

// manifestVersion is the version .release-please-manifest.json names at rev.
func manifestVersion(t *testing.T, dir, rev string) string {
	t.Helper()
	var manifest map[string]string
	if err := json.Unmarshal([]byte(git(t, dir, "show", rev+":.release-please-manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest["."]
}

// TestTheReleaseStep proves the manual release in a scratch clone: a plain checkout builds with no
// Node.js and embeds the placeholder, the release step refuses what the policy forbids and any commit
// but the release pull request's merge commit, it tags that commit on main and adds nothing to it, and
// GoReleaser's build from the tag embeds the web UI it builds and reports the tag's version.
func TestTheReleaseStep(t *testing.T) {
	dir := scratchClone(t)

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

	before := git(t, dir, "rev-parse", "main")
	merge := mergeReleasePullRequest(t, dir, testRelease, true)

	t.Run("the release step refuses", func(t *testing.T) {
		tests := []struct {
			name, version, says string
			setup               func(t *testing.T)
		}{
			{"a version with no v", "0.1.0", "vMAJOR.MINOR.PATCH", nil},
			{"a version with two parts", "v0.1", "vMAJOR.MINOR.PATCH", nil},
			{"a pre-release the policy does not name", "v0.1.0-snapshot.1", "vMAJOR.MINOR.PATCH", nil},
			{"build metadata", "v0.1.0+build.1", "vMAJOR.MINOR.PATCH", nil},
			{"a leading zero", "v0.01.0", "vMAJOR.MINOR.PATCH", nil},
			{"a pre-release numbered from zero", "v0.1.0-rc.0", "vMAJOR.MINOR.PATCH", nil},
			{"a tag that exists", testRelease, "exists", func(t *testing.T) {
				git(t, dir, "tag", testRelease, before)
				t.Cleanup(func() { git(t, dir, "tag", "--delete", testRelease) })
			}},
			{"a tree with changes", testRelease, "changes", func(t *testing.T) {
				appendTo(t, filepath.Join(dir, "README.md"), "\nchanged\n")
				t.Cleanup(func() { git(t, dir, "checkout", "--", "README.md") })
			}},
			{"a version the manifest does not name", "v0.0.1-rc.2", "its manifest names 0.0.1-rc.1", nil},
			{"the commit before the release pull request merged", testRelease, "its manifest names " + manifestVersion(t, dir, before), func(t *testing.T) {
				git(t, dir, "checkout", "--quiet", "--detach", before)
				t.Cleanup(func() { git(t, dir, "checkout", "--quiet", "main") })
			}},
			{"the release pull request's own commit", testRelease, "not on main's first-parent history", func(t *testing.T) {
				git(t, dir, "checkout", "--quiet", "release-please--branches--main")
				t.Cleanup(func() { git(t, dir, "checkout", "--quiet", "main") })
			}},
			{"a commit after the release pull request merged", testRelease, "the commit before it on main already names", func(t *testing.T) {
				git(t, dir, "commit", "--quiet", "--allow-empty", "--message", "docs: a change after the release")
				t.Cleanup(func() { git(t, dir, "reset", "--quiet", "--hard", merge) })
			}},
			{"a release with no section in CHANGELOG.md", "v0.0.1-rc.2", "no section for 0.0.1-rc.2", func(t *testing.T) {
				mergeReleasePullRequest(t, dir, "v0.0.1-rc.2", false)
				t.Cleanup(func() { git(t, dir, "reset", "--quiet", "--hard", merge) })
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tt.setup != nil {
					tt.setup(t)
				}
				head := git(t, dir, "rev-parse", "HEAD")
				tags := git(t, dir, "tag", "--list")
				out, code, left := releaseScript(t, dir, tt.version)
				t.Logf("exited %d: %s", code, strings.TrimSpace(out))
				if code == 0 || !strings.Contains(out, tt.says) {
					t.Errorf("want a refusal that says %q", tt.says)
				}
				if len(left) != 0 {
					t.Errorf("a refused release left files: %v", left)
				}
				if git(t, dir, "rev-parse", "HEAD") != head || git(t, dir, "tag", "--list") != tags {
					t.Error("a refused release changed the repository")
				}
			})
		}
	})

	t.Run("the release step tags the release pull request's merge commit", func(t *testing.T) {
		out, code, left := releaseScript(t, dir, testRelease)
		t.Logf("release.sh exited %d:\n%s", code, out)
		if code != 0 {
			t.FailNow()
		}
		if kind := git(t, dir, "cat-file", "-t", testRelease); kind != "tag" {
			t.Errorf("the tag is a %s, want an annotated tag", kind)
		}
		if tagged := git(t, dir, "rev-parse", testRelease+"^{commit}"); tagged != merge {
			t.Errorf("the tag is on %s, want the merge commit %s", tagged, merge)
		}
		if got := git(t, dir, "symbolic-ref", "--short", "HEAD"); got != "main" || git(t, dir, "rev-parse", "HEAD") != merge {
			t.Errorf("the release step left the clone on %s at %s, want main at the merge commit", got, git(t, dir, "rev-parse", "HEAD"))
		}
		if tracked := git(t, dir, "ls-tree", "-r", "--name-only", testRelease, "--", "web/static"); tracked != "web/static/placeholder.html" {
			t.Errorf("the tag holds %q under web/static, want the placeholder alone", tracked)
		}
		if status := git(t, dir, "status", "--porcelain"); status != "" {
			t.Errorf("the release step left changes:\n%s", status)
		}
		for _, command := range []string{
			"git push origin " + testRelease,
			"gh release create " + testRelease + " --verify-tag",
			"--prerelease",
			"GORELEASER_CURRENT_TAG=" + testRelease + " goreleaser release --clean",
			"scripts/homebrew-formula.sh dist/checksums.txt",
		} {
			if !strings.Contains(out, command) {
				t.Errorf("the release step does not print %q", command)
			}
		}
		if len(left) != 1 || !strings.Contains(out, "--notes-file "+left[0]) {
			t.Fatalf("the release step left %v, want one notes file that its gh command names", left)
		}
		notes, err := os.ReadFile(left[0])
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("release notes:\n%s", notes)
		plain := strings.TrimPrefix(testRelease, "v")
		if !strings.HasPrefix(string(notes), "## ["+plain+"](") || !strings.Contains(string(notes), "* "+releaseFeature+"\n") {
			t.Error("the notes are not the version's section of CHANGELOG.md")
		}
		if strings.Count(string(notes), "\n## ") != 0 {
			t.Error("the notes hold more than the version's section of CHANGELOG.md")
		}
	})

	// The tag holds no built UI, so the build embeds the UI GoReleaser builds, in every archive, and no
	// file an earlier build left in web/static, which git ignores.
	t.Run("GoReleaser builds the release from the tag", func(t *testing.T) {
		const leftover = "a file an earlier build left in web/static"
		if err := os.WriteFile(filepath.Join(dir, "web", "static", "leftover.txt"), []byte(leftover), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := goreleaserFromTag(dir, testRelease).CombinedOutput(); err != nil {
			t.Fatalf("goreleaser release from %s: %v\n%s", testRelease, err, out)
		}
		goos, goarch := hostPlatform(t)
		binaries, _ := filepath.Glob(filepath.Join(dir, "dist", "ropeshift_"+goos+"_"+goarch+"*", "ropeshift"))
		if len(binaries) != 1 {
			t.Fatalf("no binary for this machine in the build of %s: %v", testRelease, binaries)
		}
		raw, err := exec.Command(binaries[0], "version", "--json").Output()
		if err != nil {
			t.Fatal(err)
		}
		var got versionOf
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Version != testRelease || got.Modified || got.Commit != merge || got.Date == "" {
			t.Errorf("the build of %s reports %+v, want the clean merge commit %s", testRelease, got, merge)
		}
		archives, _ := filepath.Glob(filepath.Join(dir, "dist", "ropeshift_*.tar.gz"))
		if len(archives) != 4 {
			t.Errorf("the build of %s wrote %d archives, want 4: %v", testRelease, len(archives), archives)
		}
		for _, archive := range archives {
			binary := archiveBinary(t, archive)
			if !bytes.Contains(binary, []byte(builtUI)) {
				t.Errorf("%s does not embed the built web UI", filepath.Base(archive))
			}
			if bytes.Contains(binary, []byte(leftover)) {
				t.Errorf("%s embeds a file an earlier build left in web/static", filepath.Base(archive))
			}
		}
		// The release's notes are the ones release-please wrote, so GoReleaser writes none.
		if _, err := os.Stat(filepath.Join(dir, "dist", "CHANGELOG.md")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("GoReleaser wrote release notes of its own: %v", err)
		}
		if status := git(t, dir, "status", "--porcelain"); status != "" {
			t.Errorf("the build left changes:\n%s", status)
		}
	})
}

// goreleaserFromTag is the release build a maintainer runs at tag, with publishing skipped and no token
// in its environment.
func goreleaserFromTag(dir, tag string) *exec.Cmd {
	build := exec.Command("goreleaser", "release", "--clean", "--skip=publish,announce")
	build.Dir = dir
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !strings.HasSuffix(name, "_TOKEN") && !strings.HasPrefix(name, "GORELEASER_") {
			build.Env = append(build.Env, kv)
		}
	}
	build.Env = append(build.Env, "GORELEASER_CURRENT_TAG="+tag)
	return build
}

// archiveBinary returns the ropeshift binary a release archive holds.
func archiveBinary(t *testing.T, archive string) []byte {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("%s holds no ropeshift binary", filepath.Base(archive))
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "ropeshift" {
			raw, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
	}
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
