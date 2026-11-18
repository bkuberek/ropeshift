package lanes

import (
	"errors"
	"strings"
	"testing"
)

func TestTheMinimumGitIsThe231ThatReadsConfigFromTheEnvironment(t *testing.T) {
	if MinimumGit != (GitVersion{Major: 2, Minor: 31, Patch: 0}) {
		t.Errorf("the minimum is %v, and GIT_CONFIG_COUNT and rev-parse --path-format need 2.31.0", MinimumGit)
	}
}

func TestGitVersionStatesItsNumbers(t *testing.T) {
	if got := (GitVersion{Major: 2, Minor: 45, Patch: 1}).String(); got != "2.45.1" {
		t.Errorf("String is %q", got)
	}
}

func TestCheckGitHoldsABuildToTheMinimum(t *testing.T) {
	tests := []struct {
		name   string
		found  GitVersion
		usable bool
	}{
		{"a major before", GitVersion{Major: 1, Minor: 99, Patch: 9}, false},
		{"a minor before", GitVersion{Major: 2, Minor: 30, Patch: 9}, false},
		{"the one before the minimum", GitVersion{Major: 2, Minor: 25, Patch: 1}, false},
		{"exactly the minimum", GitVersion{Major: 2, Minor: 31, Patch: 0}, true},
		{"a patch above", GitVersion{Major: 2, Minor: 31, Patch: 1}, true},
		{"a minor above", GitVersion{Major: 2, Minor: 45, Patch: 1}, true},
		{"a major above", GitVersion{Major: 3, Minor: 0, Patch: 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := CheckGit(tt.found, nil)
			if report.Usable() != tt.usable {
				t.Fatalf("usable is %v for %v: %+v", report.Usable(), tt.found, report)
			}
			if report.Minimum != "2.31.0" || report.Found != tt.found.String() {
				t.Errorf("the report names minimum %q and found %q: %+v", report.Minimum, report.Found, report)
			}
			if tt.usable && (report.Problem != "" || report.Err() != nil) {
				t.Errorf("a usable git has a problem: %+v", report)
			}
			if !tt.usable {
				for _, name := range []string{"2.31.0", tt.found.String()} {
					if !strings.Contains(report.Problem, name) {
						t.Errorf("the problem %q does not name %s", report.Problem, name)
					}
				}
				if report.Err() == nil || report.Err().Error() != report.Problem {
					t.Errorf("the error is %v, want the problem %q", report.Err(), report.Problem)
				}
			}
		})
	}
}

func TestCheckGitSaysWhenGitCouldNotBeRead(t *testing.T) {
	report := CheckGit(GitVersion{}, errors.New(`exec: "git": executable file not found in $PATH`))
	if report.Usable() || report.Found != "" || report.Minimum != "2.31.0" {
		t.Fatalf("the report is %+v", report)
	}
	for _, want := range []string{"2.31.0", "executable file not found"} {
		if !strings.Contains(report.Problem, want) {
			t.Errorf("the problem %q does not name %q", report.Problem, want)
		}
	}
}
