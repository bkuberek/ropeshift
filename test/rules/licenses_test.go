package rules

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLicenseClassification(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
		allowed bool
	}{
		{"MIT", "mit", "MIT", true},
		{"BSD with three clauses", "bsd3", "BSD-3-Clause", true},
		{"BSD with two clauses", "bsd2", "BSD-2-Clause", true},
		{"Apache 2.0", "apache2", "Apache-2.0", true},
		{"ISC", "isc", "ISC", true},
		{"MPL 2.0", "mpl2", "MPL-2.0", true},
		{"MPL 2.0 naming the AGPL as a secondary license", "mpl2-secondary", "MPL-2.0", true},
		{"a logo attribution beside a license", "logo", "MIT", true},
		{"AGPL bundled beside an MPL clause", "agpl-bundled", "AGPL", false},
		{"public domain", "public-domain", "Public-Domain", true},
		{"a public domain dedication", "dedication", "Public-Domain", true},
		{"a license with a hosted-use limit", "sspl", "SSPL", false},
		{"source-available terms", "bsl", "BUSL", false},
		{"AGPL", "agpl", "AGPL", false},
		{"text nothing recognizes", "unknown", "unknown", false},
		{"no license file", "missing", "none", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClassifyModule(filepath.Join("testdata", "licenses", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tt.want || got.Allowed != tt.allowed {
				t.Errorf("got %+v, want %s allowed=%v", got, tt.want, tt.allowed)
			}
		})
	}
}

func TestWebLicenseExpressions(t *testing.T) {
	tests := []struct {
		expression string
		allowed    bool
	}{
		{"MIT", true},
		{"(MIT OR Apache-2.0)", true},
		{"MIT OR GPL-3.0", true},
		{"(MIT AND ISC)", true},
		{"OFL-1.1", true},
		{"MIT AND GPL-3.0", false},
		{"GPL-3.0", false},
		{"AGPL-3.0", false},
		{"BUSL-1.1", false},
		{"SEE LICENSE IN LICENSE.md", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			if got := allowedExpression(tt.expression); got != tt.allowed {
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestWebLicenseCheckFailsOnADisallowedPackage(t *testing.T) {
	_, violations, err := CheckWebLicenses(filepath.Join("testdata", "licenses", "web-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, violation := range violations {
		names = append(names, violation.Package)
	}
	if want := []string{"viral", "silent"}; !slices.Equal(names, want) {
		t.Errorf("violations %v, want %v", names, want)
	}
}

// TestWebDependencyLicenses holds the web application's whole tree to the rule, with no Node.js.
func TestWebDependencyLicenses(t *testing.T) {
	report, violations, err := CheckWebLicenses(filepath.Join(repoRoot(t), "web", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report) == 0 {
		t.Fatal("the lockfile lists nothing")
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestLicenseCheckFailsOnADisallowedModule(t *testing.T) {
	modules := []Module{
		{Path: "example.com/good", Version: "v1.0.0", Dir: filepath.Join("testdata", "licenses", "mit")},
		{Path: "example.com/bad", Version: "v1.0.0", Dir: filepath.Join("testdata", "licenses", "sspl")},
		{Path: "example.com/none", Version: "v1.0.0", Dir: filepath.Join("testdata", "licenses", "missing")},
	}
	report, violations := CheckLicenses(modules)
	if len(report) != len(modules) {
		t.Errorf("the report lists %d of %d modules", len(report), len(modules))
	}
	if len(violations) != 2 {
		t.Fatalf("got %v, want the bad and the unlicensed module", violations)
	}
	for _, v := range violations {
		if v.Rule != "license" {
			t.Errorf("rule %q", v.Rule)
		}
	}
}

// TestDependencyLicenses is the license rule over the real build list. It runs in
// the default suite, so a new dependency cannot land without passing it.
func TestDependencyLicenses(t *testing.T) {
	modules, err := ListModules(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	report, violations := CheckLicenses(modules)
	for _, line := range report {
		t.Log(line)
	}
	for _, v := range violations {
		t.Error(v)
	}
	if len(report) != len(modules) {
		t.Errorf("the report lists %d of %d modules", len(report), len(modules))
	}
}

func TestReportLineNamesModuleVersionAndLicense(t *testing.T) {
	report, _ := CheckLicenses([]Module{
		{Path: "example.com/good", Version: "v1.2.3", Dir: filepath.Join("testdata", "licenses", "mit")},
	})
	if len(report) != 1 {
		t.Fatalf("report %v", report)
	}
	for _, want := range []string{"example.com/good", "v1.2.3", "MIT"} {
		if !strings.Contains(report[0], want) {
			t.Errorf("line %q lacks %q", report[0], want)
		}
	}
}
