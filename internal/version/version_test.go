package version

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const revision = "dd81c214342bad4bb98b4907f65871cf6607be59"

// built is what the toolchain records for a build of the main module at version, with the given
// version control settings.
func built(version string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Path: "ropeshift.com/ropeshift", Version: version}, Settings: settings}
}

func vcs(revision, modified string) []debug.BuildSetting {
	return []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.time", Value: "2026-11-07T23:09:36Z"},
		{Key: "vcs.modified", Value: modified},
	}
}

func TestABuildWithNoReleaseReportsADevelopmentVersion(t *testing.T) {
	tests := []struct {
		name       string
		build      *debug.BuildInfo
		wantCommit string
		modified   bool
	}{
		{"a build outside version control", built("(devel)"), "", false},
		{"go build at a commit no tag names", built("v0.0.0-20261107230936-dd81c214342b", vcs(revision, "false")...), revision, false},
		{"go build after a release", built("v0.1.1-0.20261107230936-dd81c214342b", vcs(revision, "false")...), revision, false},
		{"go build after a pre-release", built("v0.2.0-alpha.1.0.20261107230936-dd81c214342b", vcs(revision, "false")...), revision, false},
		{"go build at a tag with changes", built("v0.1.0+dirty", vcs(revision, "true")...), revision, true},
		{"go build at a commit with changes", built("v0.0.0-20261107230936-dd81c214342b+dirty", vcs(revision, "true")...), revision, true},
		{"go install at a revision", built("v0.0.0-20261107230936-dd81c214342b"), "dd81c214342b", false},
		{"a version that is not semantic", built("v1.2", vcs(revision, "false")...), revision, false},
		{"no version at all", built(""), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve("ropeshift", stamp{}, tt.build, true)
			if got.Version != Development {
				t.Errorf("version %q, want %q", got.Version, Development)
			}
			if got.Commit != tt.wantCommit {
				t.Errorf("commit %q, want %q", got.Commit, tt.wantCommit)
			}
			if got.Modified != tt.modified {
				t.Errorf("modified %v, want %v", got.Modified, tt.modified)
			}
			if got.Date != "" {
				t.Errorf("date %q: no build that skipped the release stamp knows when it was built", got.Date)
			}
		})
	}
}

func TestABinaryWithNoBuildInformationIsADevelopmentBuild(t *testing.T) {
	got := resolve("ropeshiftd", stamp{}, nil, false)
	if got.Version != Development || got.Command != "ropeshiftd" {
		t.Errorf("got %#v", got)
	}
}

// go install module@vX.Y.Z builds from the module cache, which holds no version control data, and
// records the exact version it fetched.
func TestAnExactModuleVersionIsTheRelease(t *testing.T) {
	for _, release := range []string{"v0.1.0", "v0.1.0-alpha.1", "v0.3.2-rc.10", "v1.0.0"} {
		t.Run(release, func(t *testing.T) {
			got := resolve("ropeshift", stamp{}, built(release), true)
			if got.Version != release {
				t.Errorf("version %q, want %q", got.Version, release)
			}
			if got.Commit != "" || got.Modified {
				t.Errorf("the module cache knows no commit or change: %+v", got)
			}
		})
	}
}

func TestTheReleaseStampWins(t *testing.T) {
	stamped := stamp{version: "v0.2.0-beta.3", commit: revision, date: "2026-11-08T10:00:00Z", modified: "false"}
	got := resolve("ropeshift", stamped, built("v0.0.0-20261107230936-dd81c214342b+dirty", vcs("0000", "true")...), true)
	want := Info{
		Command: "ropeshift", Version: "v0.2.0-beta.3", Commit: revision, Date: "2026-11-08T10:00:00Z", Modified: false,
		Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
	if got != want {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

// A stamp can carry the version alone. What it leaves out comes from the toolchain, and a modified
// flag that is neither true nor false is no answer.
func TestAPartialStampTakesTheRestFromTheToolchain(t *testing.T) {
	got := resolve("ropeshift", stamp{version: "v0.2.0", modified: "maybe"}, built("(devel)", vcs(revision, "true")...), true)
	if got.Version != "v0.2.0" || got.Commit != revision || !got.Modified {
		t.Errorf("got %#v", got)
	}
}

// Consumers read these keys by name, so each is present on every build, in this order, even when
// its value is empty.
func TestTheJSONShapeIsStable(t *testing.T) {
	raw, err := json.Marshal(resolve("ropeshift", stamp{}, built("(devel)"), true))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"command", "version", "commit", "date", "modified", "go", "os", "arch"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("keys %v, want %v", keys, want)
	}
}

func TestTheLineNamesWhatIsKnown(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			"a release",
			Info{Command: "ropeshift", Version: "v0.1.0", Commit: revision, Date: "2026-11-08T10:00:00Z", Go: "go1.27.3", OS: "linux", Arch: "arm64"},
			"ropeshift v0.1.0 (commit dd81c214342b, built 2026-11-08T10:00:00Z, go1.27.3, linux/arm64)",
		},
		{
			"a development build with changes",
			Info{Command: "ropeshiftd", Version: "dev", Commit: revision, Modified: true, Go: "go1.27.3", OS: "darwin", Arch: "amd64"},
			"ropeshiftd dev (commit dd81c214342b, modified, go1.27.3, darwin/amd64)",
		},
		{
			"a module install",
			Info{Command: "ropeshift", Version: "v0.1.0", Go: "go1.27.3", OS: "darwin", Arch: "arm64"},
			"ropeshift v0.1.0 (go1.27.3, darwin/arm64)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.String(); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// The test binary carries no release stamp, so Read reports what the toolchain recorded.
func TestReadDescribesThisBinary(t *testing.T) {
	got := Read("ropeshift")
	if got.Command != "ropeshift" || got.Go != runtime.Version() || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
		t.Errorf("got %#v", got)
	}
	if got.Version != Development {
		t.Errorf("a test binary reports %q, want %q", got.Version, Development)
	}
}
