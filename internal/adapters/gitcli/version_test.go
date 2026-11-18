package gitcli_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/lanes"
)

// TestParseVersionReadsWhatEveryBuildOfGitPrints holds the parse to the lines real builds print: a plain release,
// Apple's, Windows', a release candidate, and the suffixes distributions add. Only the numbers git's behavior
// follows are kept.
func TestParseVersionReadsWhatEveryBuildOfGitPrints(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   lanes.GitVersion
	}{
		{"a plain release", "git version 2.45.1\n", lanes.GitVersion{Major: 2, Minor: 45, Patch: 1}},
		{"no trailing newline", "git version 2.45.1", lanes.GitVersion{Major: 2, Minor: 45, Patch: 1}},
		{"Apple Git", "git version 2.39.5 (Apple Git-154)\n", lanes.GitVersion{Major: 2, Minor: 39, Patch: 5}},
		{"Windows", "git version 2.45.1.windows.1\n", lanes.GitVersion{Major: 2, Minor: 45, Patch: 1}},
		{"a release candidate", "git version 2.46.0.rc0\n", lanes.GitVersion{Major: 2, Minor: 46, Patch: 0}},
		{"a dashed release candidate", "git version 2.46.0-rc1\n", lanes.GitVersion{Major: 2, Minor: 46, Patch: 0}},
		{"a distribution suffix", "git version 2.39.2+ds1\n", lanes.GitVersion{Major: 2, Minor: 39, Patch: 2}},
		{"a vendor suffix", "git version 2.43.0 (Ubuntu 2.43.0-1ubuntu7)\n", lanes.GitVersion{Major: 2, Minor: 43, Patch: 0}},
		{"a build from a checkout", "git version 2.47.0.141.g0a1b2c3\n", lanes.GitVersion{Major: 2, Minor: 47, Patch: 0}},
		{"no patch number", "git version 2.31\n", lanes.GitVersion{Major: 2, Minor: 31, Patch: 0}},
		{"a first line before other output", "git version 2.40.1\nbuilt from commit abc\n", lanes.GitVersion{Major: 2, Minor: 40, Patch: 1}},
		{"surrounding space", "  git version 2.31.0  \n", lanes.GitVersion{Major: 2, Minor: 31, Patch: 0}},
		{"a two digit patch number", "git version 2.31.10\n", lanes.GitVersion{Major: 2, Minor: 31, Patch: 10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitcli.ParseVersion(tt.output)
			if err != nil || got != tt.want {
				t.Errorf("ParseVersion(%q) = %v, %v; want %v", tt.output, got, err, tt.want)
			}
		})
	}
}

// TestProbeReadsTheGitOnPath runs the real git the tests need, which is at the minimum or newer, and a PATH with
// no git, which is an error and no guessed version.
func TestProbeReadsTheGitOnPath(t *testing.T) {
	got, err := gitcli.Probe{}.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !lanes.CheckGit(got, nil).Usable() {
		t.Errorf("the git on PATH is %v, below the minimum %v the suite needs", got, lanes.MinimumGit)
	}

	t.Setenv("PATH", t.TempDir())
	if got, err := (gitcli.Probe{}).Version(t.Context()); err == nil {
		t.Errorf("a PATH with no git gave version %v", got)
	}
}

// TestParseVersionRefusesWhatNamesNoVersion holds the parse to an error for output that is not a version, so a
// program on PATH that is not git, or an empty reply, is never read as one.
func TestParseVersionRefusesWhatNamesNoVersion(t *testing.T) {
	for _, output := range []string{
		"", "\n", "   ", "garbage", "git", "git version", "git version \n", "git version unknown", "git version x.y.z",
		"git version 2", "git version 2.x", "git version 2.31x", "git version .31.0", "git version -2.31.0",
		"version 2.45.1", "tig version 2.5.8", "git version 99999999999999999999.1.0",
	} {
		if got, err := gitcli.ParseVersion(output); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", output, got)
		}
	}
}
