package lanes

import (
	"errors"
	"fmt"
)

// MinimumGit is the oldest git ropeshift runs on. Two features the adapter relies on arrived in git 2.31.0, and
// nothing it uses needs a later one:
//
//   - the GIT_CONFIG_COUNT, GIT_CONFIG_KEY_<n>, and GIT_CONFIG_VALUE_<n> variables, which keep a merge from running
//     a repository's hooks and keep a commit from being signed;
//   - rev-parse --path-format=absolute, which names the directories a worktree's lock and snapshot index live in.
//
// Both are in https://github.com/git/git/blob/master/Documentation/RelNotes/2.31.0.adoc.
var MinimumGit = GitVersion{Major: 2, Minor: 31, Patch: 0}

// GitVersion is the release of a git build, as the three numbers its behavior follows. A suffix a build adds, such as
// a vendor's name or a release candidate's, is no part of it.
type GitVersion struct {
	Major int
	Minor int
	Patch int
}

// String states the version as git numbers its releases.
func (v GitVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// before says whether v is an older release than other.
func (v GitVersion) before(other GitVersion) bool {
	switch {
	case v.Major != other.Major:
		return v.Major < other.Major
	case v.Minor != other.Minor:
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

// GitReport is what the doctor says of git: the oldest release ropeshift runs on, the release found, and the problem
// when this machine's git cannot run ropeshift.
type GitReport struct {
	// Minimum is MinimumGit.
	Minimum string `json:"minimum"`
	// Found is the release this machine's git is, and is empty when git could not be read.
	Found string `json:"found,omitempty"`
	// Problem says why git cannot run ropeshift, and how to fix it. It is empty when git can.
	Problem string `json:"problem,omitempty"`
}

// CheckGit holds what a probe of this machine's git found to the minimum. A probe that failed is err, and says git
// could not be read.
func CheckGit(found GitVersion, err error) GitReport {
	report := GitReport{Minimum: MinimumGit.String()}
	if err != nil {
		report.Problem = fmt.Sprintf("git could not be run (%v): ropeshift needs git %s or newer", err, report.Minimum)
		return report
	}
	report.Found = found.String()
	if found.before(MinimumGit) {
		report.Problem = fmt.Sprintf("git %s is older than %s, the oldest git ropeshift runs on: install git %s or newer",
			report.Found, report.Minimum, report.Minimum)
	}
	return report
}

// Usable says git can run ropeshift.
func (r GitReport) Usable() bool { return r.Problem == "" }

// Err is the problem as an error, and nil when git is usable.
func (r GitReport) Err() error {
	if r.Usable() {
		return nil
	}
	return errors.New(r.Problem)
}
