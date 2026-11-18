package practices

import (
	"slices"
	"strings"
	"testing"
)

func TestTableHoldsTheTwelvePracticesInOrder(t *testing.T) {
	want := []ID{EnvIgnored, Secrets, CI, PreCommit, Lockfile, DependencyUpdates, SecurityPolicy, Codeowners, License, Tests, BranchProtection, RequiredReview}
	var got []ID
	for _, p := range Table() {
		got = append(got, p.ID)
		if p.Why == "" || p.Fix == "" || p.Applies == "" {
			t.Errorf("%s has no why, fix, or applies text", p.ID)
		}
		switch p.Severity {
		case High, Medium, Low:
		default:
			t.Errorf("%s has severity %q", p.ID, p.Severity)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("table order = %v, want %v", got, want)
	}
	if TableVersion != 1 {
		t.Errorf("TableVersion = %d, want 1", TableVersion)
	}
}

func TestTableSeveritiesAndTemplates(t *testing.T) {
	want := map[ID]struct {
		severity Severity
		template string
	}{
		EnvIgnored:        {High, "repository/gitignore"},
		Secrets:           {High, ""},
		CI:                {Medium, "pre-commit-config.yaml"},
		PreCommit:         {Low, "pre-commit-config.yaml"},
		Lockfile:          {Low, ""},
		DependencyUpdates: {Low, "dependabot.yml"},
		SecurityPolicy:    {Low, "security.md"},
		Codeowners:        {Low, "codeowners"},
		License:           {Low, ""},
		Tests:             {Low, ""},
		BranchProtection:  {Medium, ""},
		RequiredReview:    {Medium, ""},
	}
	for _, p := range Table() {
		if w := want[p.ID]; p.Severity != w.severity || p.Template != w.template {
			t.Errorf("%s = %s %q, want %s %q", p.ID, p.Severity, p.Template, w.severity, w.template)
		}
	}
}

func TestCommandNamesTheTemplate(t *testing.T) {
	p, _ := Lookup(Codeowners)
	if got := p.Command(); got != "ropeshift template show codeowners" {
		t.Errorf("Command = %q", got)
	}
	p, _ = Lookup(Secrets)
	if got := p.Command(); got != "" {
		t.Errorf("a practice with no template has command %q", got)
	}
}

func TestTableReturnsACopy(t *testing.T) {
	Table()[0].Why = "changed"
	if Table()[0].Why == "changed" {
		t.Error("changing the returned table changed the table")
	}
}

func TestManagersTable(t *testing.T) {
	var names []string
	for _, m := range Managers() {
		names = append(names, m.Name)
		if len(m.Manifests) == 0 || m.Tests == "" || m.Ecosystem == "" || m.isTest == nil {
			t.Errorf("%s is missing a manifest, test convention, ecosystem, or test rule", m.Name)
		}
		if len(m.Lockfiles) == 0 && m.NoLockfile == "" {
			t.Errorf("%s has no lockfile and no reason", m.Name)
		}
		if len(m.Lockfiles) > 0 && m.LockCommand == "" {
			t.Errorf("%s has a lockfile and no lock command", m.Name)
		}
	}
	want := []string{"go", "npm", "python", "cargo", "bundler", "composer", "maven", "gradle", "swift"}
	if !slices.Equal(names, want) {
		t.Errorf("managers = %v, want %v", names, want)
	}
	if !slices.Equal(SkippedSegments(), []string{"node_modules", "vendor", "third_party", "testdata", ".git"}) {
		t.Errorf("skipped segments = %v", SkippedSegments())
	}
	m, _ := managerNamed("python")
	if m.LockCommandFor("Pipfile") != "pipenv lock" || m.LockCommandFor("pyproject.toml") != "uv lock" {
		t.Error("python lock commands by manifest are wrong")
	}
}

func TestIsSkipped(t *testing.T) {
	for path, want := range map[string]bool{
		"go.mod": false, "web/package.json": false, "node_modules/x/package.json": true,
		"a/vendor/b/go.mod": true, "testdata/go.mod": true, "x/third_party/y": true, ".git/config": true,
		"vendors/go.mod": false,
	} {
		if got := IsSkipped(path); got != want {
			t.Errorf("IsSkipped(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestFindingIDRoundTrips(t *testing.T) {
	for _, tt := range []struct {
		practice ID
		subject  []string
		want     string
	}{
		{EnvIgnored, []string{"ignore", "."}, "env-ignored:ignore:."},
		{EnvIgnored, []string{"tracked", "apps/web/.env"}, "env-ignored:tracked:apps/web/.env"},
		{Secrets, []string{"github_token", "config/dev.yaml", "1a2b3c4d5e6f"}, "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"},
		{Secrets, []string{"api_key", "a:b.txt", "1a2b3c4d5e6f"}, "secrets:api_key:a:b.txt:1a2b3c4d5e6f"},
		{Lockfile, []string{"npm"}, "lockfile:npm"},
		{Lockfile, []string{"npm", "web"}, "lockfile:npm:web"},
		{Tests, []string{"go"}, "tests:go"},
		{Codeowners, nil, "codeowners"},
		{RequiredReview, nil, "required-review"},
	} {
		id := FindingID(tt.practice, tt.subject...)
		if id != tt.want {
			t.Errorf("FindingID = %q, want %q", id, tt.want)
		}
		got, err := ParseFindingID(id)
		if err != nil || got != tt.practice {
			t.Errorf("ParseFindingID(%q) = %q, %v", id, got, err)
		}
	}
}

func TestParseFindingIDRefusals(t *testing.T) {
	for _, id := range []string{
		"", "nothing", "nothing:x", ":x", "Codeowners",
		"codeowners:extra", "ci:x",
		"env-ignored", "env-ignored:", "env-ignored:ignore", "env-ignored:ignore:", "env-ignored:other:x",
		"secrets", "secrets:github_token", "secrets:github_token:path", "secrets:github_token:path:short",
		"secrets:github_token:path:1A2B3C4D5E6F", "secrets::path:1a2b3c4d5e6f",
		"lockfile", "lockfile:", "lockfile:ruby", "lockfile:npm:",
		"tests", "tests:ruby", "tests:go:web",
	} {
		if got, err := ParseFindingID(id); err == nil {
			t.Errorf("ParseFindingID(%q) = %q, want an error", id, got)
		}
	}
}

func TestFindingsSortBySeverityThenTableOrderThenID(t *testing.T) {
	findings := []Finding{
		{ID: "tests:go", Practice: Tests, Severity: Low},
		{ID: "ci", Practice: CI, Severity: Medium},
		{ID: "env-ignored:tracked:b", Practice: EnvIgnored, Severity: High},
		{ID: "env-ignored:tracked:a", Practice: EnvIgnored, Severity: High},
		{ID: "secrets:r:p:aaaaaaaaaaaa", Practice: Secrets, Severity: High},
		{ID: "codeowners", Practice: Codeowners, Severity: Low},
	}
	sortFindings(findings)
	var got []string
	for _, f := range findings {
		got = append(got, f.ID)
	}
	want := "env-ignored:tracked:a env-ignored:tracked:b secrets:r:p:aaaaaaaaaaaa ci codeowners tests:go"
	if strings.Join(got, " ") != want {
		t.Errorf("order = %v", got)
	}
}
