package practices

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// input builds an Input over files, whose text is the map's value, with every env probe ignored.
func input(files map[string]string) Input {
	var paths []string
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	ignored := map[string]bool{}
	for _, probe := range Probes(paths) {
		ignored[probe] = true
	}
	return Input{
		Repository: "r1", Commit: "c0ffee", BaseBranch: "main", Worker: "w1",
		CheckedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), RulesVersion: 1,
		Paths: paths, Ignored: ignored,
		Read: func(path string) (string, bool) { text, ok := files[path]; return text, ok },
	}
}

// clean holds each file practice that applies with no manager detected.
func clean() map[string]string {
	return map[string]string{
		".pre-commit-config.yaml": "repos: []\n",
		"SECURITY.md":             "x",
		"CODEOWNERS":              "* @a",
	}
}

func statusOf(t *testing.T, r Result, id ID) PracticeStatus {
	t.Helper()
	for _, s := range r.Practices {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("result has no status for %s", id)
	return PracticeStatus{}
}

func detailOf(r Result, id ID) string {
	for _, f := range r.Findings {
		if f.Practice == id {
			return f.Detail
		}
	}
	return ""
}

func findingIDs(r Result, id ID) []string {
	var ids []string
	for _, f := range r.Findings {
		if f.Practice == id {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

func TestEvaluateFillsTheResult(t *testing.T) {
	in := input(clean())
	in.ScannedFrom = "abc"
	in.Skipped = 3
	r := Evaluate(in)
	if r.Repository != "r1" || r.Commit != "c0ffee" || r.BaseBranch != "main" || r.Worker != "w1" ||
		r.TableVersion != TableVersion || r.RulesVersion != 1 || r.ScannedFrom != "abc" || r.Skipped != 3 ||
		!r.CheckedAt.Equal(in.CheckedAt) {
		t.Errorf("result = %+v", r)
	}
	if len(r.Practices) != 12 {
		t.Fatalf("practices = %d, want 12", len(r.Practices))
	}
	for i, p := range Table() {
		if r.Practices[i].ID != p.ID {
			t.Errorf("practice %d = %s, want %s", i, r.Practices[i].ID, p.ID)
		}
	}
	if r.Findings == nil || len(r.Findings) != 0 {
		t.Errorf("findings = %#v, want an empty list", r.Findings)
	}
}

func TestEvaluateACleanRepositoryWithNoForgeBinding(t *testing.T) {
	r := Evaluate(input(clean()))
	want := map[ID]Status{
		EnvIgnored: StatusMet, Secrets: StatusMet, CI: StatusMet, PreCommit: StatusMet, Lockfile: StatusNotApplicable,
		DependencyUpdates: StatusNotApplicable, SecurityPolicy: StatusMet, Codeowners: StatusMet, License: StatusNotApplicable,
		Tests: StatusNotApplicable, BranchProtection: StatusNotApplicable, RequiredReview: StatusNotApplicable,
	}
	for id, status := range want {
		if got := statusOf(t, r, id); got.Status != status {
			t.Errorf("%s = %s (%s), want %s", id, got.Status, got.Reason, status)
		}
	}
	if got := statusOf(t, r, License).Reason; got != "no forge binding" {
		t.Errorf("license reason = %q", got)
	}
	if got := statusOf(t, r, Lockfile).Reason; got != "no package manager is detected" {
		t.Errorf("lockfile reason = %q", got)
	}
	if len(r.Findings) != 0 {
		t.Errorf("findings = %v", r.Findings)
	}
}

func TestEvaluateAnEmptyRepositoryFindsTheFileGaps(t *testing.T) {
	in := input(nil)
	r := Evaluate(in)
	for _, id := range []ID{PreCommit, CI, SecurityPolicy, Codeowners} {
		if got := statusOf(t, r, id).Status; got != StatusFinding {
			t.Errorf("%s = %s, want finding", id, got)
		}
	}
	for _, f := range r.Findings {
		p, _ := Lookup(f.Practice)
		if f.Severity != p.Severity || f.Detail == "" || f.ID != string(f.Practice) {
			t.Errorf("finding = %+v", f)
		}
	}
	if got := r.Findings[0].ID; got != "ci" {
		t.Errorf("first finding = %s, want the medium one first", got)
	}
}

func TestEnvIgnored(t *testing.T) {
	t.Run("met when every probe is ignored and none is tracked", func(t *testing.T) {
		files := clean()
		files[".env.example"] = "A="
		if got := statusOf(t, Evaluate(input(files)), EnvIgnored); got.Status != StatusMet {
			t.Errorf("status = %v", got)
		}
	})
	t.Run("a root probe not ignored", func(t *testing.T) {
		in := input(clean())
		in.Ignored[".env.local"] = false
		in.Ignored[".env"] = false
		r := Evaluate(in)
		if got := findingIDs(r, EnvIgnored); !slices.Equal(got, []string{"env-ignored:ignore:."}) {
			t.Errorf("findings = %v", got)
		}
		if d := r.Findings[0].Detail; !strings.Contains(d, ".env,") || !strings.Contains(d, ".env.local") {
			t.Errorf("detail = %q", d)
		}
	})
	t.Run("a manifest folder's probe not ignored", func(t *testing.T) {
		files := clean()
		files["apps/web/package.json"] = `{"dependencies":{"a":"1"}}`
		files["package-lock.json"] = "{}"
		in := input(files)
		in.Ignored["apps/web/.env.production"] = false
		if got := findingIDs(Evaluate(in), EnvIgnored); !slices.Equal(got, []string{"env-ignored:ignore:apps/web"}) {
			t.Errorf("findings = %v", got)
		}
	})
	t.Run("a tracked env file", func(t *testing.T) {
		files := clean()
		files["apps/web/.env"] = "A=1"
		files[".env.staging"] = "A=1"
		files[".env.example"] = "A="
		got := findingIDs(Evaluate(input(files)), EnvIgnored)
		want := []string{"env-ignored:tracked:.env.staging", "env-ignored:tracked:apps/web/.env"}
		if !slices.Equal(got, want) {
			t.Errorf("findings = %v, want %v", got, want)
		}
	})
	t.Run("a probe with no answer is unknown", func(t *testing.T) {
		in := input(clean())
		delete(in.Ignored, ".env.test")
		got := statusOf(t, Evaluate(in), EnvIgnored)
		if got.Status != StatusUnknown || !strings.Contains(got.Reason, ".env.test") {
			t.Errorf("status = %v", got)
		}
	})
	t.Run("an unknown probe does not hide a finding", func(t *testing.T) {
		in := input(clean())
		delete(in.Ignored, ".env.test")
		in.Ignored[".env"] = false
		if got := statusOf(t, Evaluate(in), EnvIgnored).Status; got != StatusFinding {
			t.Errorf("status = %v", got)
		}
	})
}

func TestProbes(t *testing.T) {
	got := Probes([]string{"go.mod", "web/package.json", "node_modules/x/package.json", "vendor/y/go.mod", "docs/readme.md"})
	var want []string
	for _, folder := range []string{"", "web"} {
		for _, name := range []string{".env", ".env.local", ".env.development", ".env.production", ".env.test"} {
			want = append(want, joinPath(folder, name))
		}
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("Probes = %v, want %v", got, want)
	}
	if got := Probes(nil); len(got) != 5 || got[0] != ".env" {
		t.Errorf("Probes(nil) = %v", got)
	}
}

func TestSecrets(t *testing.T) {
	in := input(clean())
	in.Secrets = []SecretMatch{
		{Rule: "github_token", Path: "config/dev.yaml", Line: 4, Commit: "0123456789abcdef", Digest: "1a2b3c4d5e6f7a8b9c0d"},
		{Rule: "github_token", Path: "config/dev.yaml", Line: 4, Commit: "ffff", Digest: "1a2b3c4d5e6f7a8b9c0d"},
	}
	r := Evaluate(in)
	if got := findingIDs(r, Secrets); !slices.Equal(got, []string{"secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"}) {
		t.Fatalf("findings = %v", got)
	}
	f := r.Findings[0]
	if f.Rule != "github_token" || f.Path != "config/dev.yaml" || f.Line != 4 || f.Commit != "0123456789abcdef" || f.Digest != "1a2b3c4d5e6f7a8b9c0d" || f.Severity != High {
		t.Errorf("finding = %+v", f)
	}
	if got := statusOf(t, r, Secrets).Status; got != StatusFinding {
		t.Errorf("status = %s", got)
	}
	in = input(clean())
	in.SecretsUnknown = "the scan failed"
	if got := statusOf(t, Evaluate(in), Secrets); got.Status != StatusUnknown || got.Reason != "the scan failed" {
		t.Errorf("status = %v", got)
	}
}

func TestSecretsOfAPartialScanAreUnknownUnlessItFoundSomething(t *testing.T) {
	in := input(clean())
	in.SecretsPartial = "the repository is a shallow clone"
	got := statusOf(t, Evaluate(in), Secrets)
	if got.Status != StatusUnknown || !strings.Contains(got.Reason, "the repository is a shallow clone") {
		t.Errorf("status = %v", got)
	}
	in.Secrets = []SecretMatch{{Rule: "github_token", Path: "a.txt", Line: 1, Commit: "0123456789abcdef", Digest: "1a2b3c4d5e6f7a8b9c0d"}}
	if got := statusOf(t, Evaluate(in), Secrets).Status; got != StatusFinding {
		t.Errorf("status with a match = %s", got)
	}
}

func TestPreCommit(t *testing.T) {
	for name, tt := range map[string]struct {
		files map[string]string
		want  Status
	}{
		"config":              {map[string]string{".pre-commit-config.yaml": ""}, StatusMet},
		"hooks target":        {map[string]string{"Makefile": "all:\n\techo\nhooks:\n\tgit config x\n"}, StatusMet},
		"hooks with deps":     {map[string]string{"Makefile": "hooks: setup\n\ttrue\n"}, StatusMet},
		"hooks in a list":     {map[string]string{"Makefile": "lint hooks:\n\ttrue\n"}, StatusMet},
		"hooks assignment":    {map[string]string{"Makefile": "hooks := a b\n"}, StatusFinding},
		"hooks simple assign": {map[string]string{"Makefile": "hooks ::= a\nHOOKS = a:b\n"}, StatusFinding},
		"phony only":          {map[string]string{"Makefile": ".PHONY: hooks\n"}, StatusFinding},
		"a recipe line":       {map[string]string{"Makefile": "all:\n\thooks: x\n"}, StatusFinding},
		"a nested config":     {map[string]string{"sub/.pre-commit-config.yaml": ""}, StatusFinding},
		"a nested makefile":   {map[string]string{"sub/Makefile": "hooks:\n"}, StatusFinding},
		"nothing":             {map[string]string{}, StatusFinding},
	} {
		t.Run(name, func(t *testing.T) {
			if got := statusOf(t, Evaluate(input(tt.files)), PreCommit).Status; got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
	t.Run("an unreadable Makefile is unknown", func(t *testing.T) {
		in := input(map[string]string{"Makefile": "hooks:\n"})
		in.Read = func(string) (string, bool) { return "", false }
		if got := statusOf(t, Evaluate(in), PreCommit).Status; got != StatusUnknown {
			t.Errorf("status = %s", got)
		}
	})
}

func TestCI(t *testing.T) {
	workflow := map[string]string{".github/workflows/ci.yml": "on: push"}
	yaml := map[string]string{".github/workflows/ci.yaml": "on: push"}
	nested := map[string]string{"sub/.github/workflows/ci.yml": "on: push", ".github/workflows/readme.md": ""}
	hook := map[string]string{".pre-commit-config.yaml": ""}
	makefile := map[string]string{"Makefile": "hooks:\n"}
	for name, tt := range map[string]struct {
		files     map[string]string
		authority string
		want      Status
	}{
		"a yml workflow":                   {workflow, "review_approves", StatusMet},
		"a yaml workflow":                  {yaml, "review_approves", StatusMet},
		"a nested workflow is no workflow": {nested, "person_merges", StatusFinding},
		"recipe at person_merges":          {hook, "person_merges", StatusMet},
		"recipe at person_approves":        {hook, "person_approves", StatusMet},
		"recipe at the default":            {hook, "", StatusMet},
		"recipe with the hooks target":     {makefile, "person_merges", StatusMet},
		"recipe at review_approves":        {hook, "review_approves", StatusFinding},
		"no hook at person_merges":         {map[string]string{}, "person_merges", StatusFinding},
		"no hook at review_approves":       {map[string]string{}, "review_approves", StatusFinding},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(tt.files)
			in.MergeAuthority = tt.authority
			r := Evaluate(in)
			if got := statusOf(t, r, CI).Status; got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
			if tt.want == StatusFinding && !slices.Equal(findingIDs(r, CI), []string{"ci"}) {
				t.Errorf("ci findings = %v", findingIDs(r, CI))
			}
		})
	}
	t.Run("an unknown pre-commit under person merge authority is unknown", func(t *testing.T) {
		for _, authority := range []string{"", "person_merges", "person_approves"} {
			in := input(map[string]string{"Makefile": "hooks:\n"})
			in.Read = func(string) (string, bool) { return "", false }
			in.MergeAuthority = authority
			r := Evaluate(in)
			if got := statusOf(t, r, CI); got.Status != StatusUnknown || got.Reason == "" {
				t.Errorf("authority %q: ci = %+v", authority, got)
			}
			if got := findingIDs(r, CI); len(got) != 0 {
				t.Errorf("authority %q: ci findings = %v", authority, got)
			}
		}
	})
	t.Run("an unknown pre-commit under review_approves is a finding", func(t *testing.T) {
		in := input(map[string]string{"Makefile": "hooks:\n"})
		in.Read = func(string) (string, bool) { return "", false }
		in.MergeAuthority = "review_approves"
		if got := statusOf(t, Evaluate(in), CI).Status; got != StatusFinding {
			t.Errorf("ci = %s", got)
		}
	})
	t.Run("the finding names what fails", func(t *testing.T) {
		in := input(hook)
		in.MergeAuthority = "review_approves"
		r := Evaluate(in)
		var detail string
		for _, f := range r.Findings {
			if f.Practice == CI {
				detail = f.Detail
			}
		}
		if !strings.Contains(detail, "review_approves") || strings.Contains(detail, "pre-commit practice") {
			t.Errorf("detail = %q", detail)
		}
	})
}

func TestLockfileByManager(t *testing.T) {
	npmDeps := `{"dependencies":{"left-pad":"1"}}`
	for name, tt := range map[string]struct {
		files map[string]string
		want  Status
		ids   []string
	}{
		"go with require and go.sum":      {map[string]string{"go.mod": "module m\nrequire a v1\n", "go.sum": ""}, StatusMet, nil},
		"go with a require block":         {map[string]string{"go.mod": "module m\nrequire (\n\ta v1\n)\n", "go.sum": ""}, StatusMet, nil},
		"go with no go.sum":               {map[string]string{"go.mod": "module m\nrequire a v1\n"}, StatusFinding, []string{"lockfile:go"}},
		"go with no require":              {map[string]string{"go.mod": "module m\n// require a v1\ngo 1.22\n"}, StatusNotApplicable, nil},
		"npm with package-lock":           {map[string]string{"package.json": npmDeps, "package-lock.json": ""}, StatusMet, nil},
		"npm with yarn.lock":              {map[string]string{"package.json": npmDeps, "yarn.lock": ""}, StatusMet, nil},
		"npm with pnpm-lock.yaml":         {map[string]string{"package.json": npmDeps, "pnpm-lock.yaml": ""}, StatusMet, nil},
		"npm with bun.lockb":              {map[string]string{"package.json": npmDeps, "bun.lockb": ""}, StatusMet, nil},
		"npm with shrinkwrap":             {map[string]string{"package.json": npmDeps, "npm-shrinkwrap.json": ""}, StatusMet, nil},
		"npm with no lockfile":            {map[string]string{"package.json": npmDeps}, StatusFinding, []string{"lockfile:npm"}},
		"npm with devDependencies only":   {map[string]string{"package.json": `{"devDependencies":{"a":"1"}}`}, StatusFinding, []string{"lockfile:npm"}},
		"npm with no dependencies":        {map[string]string{"package.json": `{"name":"x","dependencies":{}}`}, StatusNotApplicable, nil},
		"npm with unreadable json":        {map[string]string{"package.json": `{`}, StatusUnknown, nil},
		"a nested npm finds a root lock":  {map[string]string{"web/package.json": npmDeps, "package-lock.json": ""}, StatusMet, nil},
		"a nested npm finds its own lock": {map[string]string{"web/package.json": npmDeps, "web/yarn.lock": ""}, StatusMet, nil},
		"a nested npm with none":          {map[string]string{"web/package.json": npmDeps}, StatusFinding, []string{"lockfile:npm:web"}},
		"a sibling's lock does not count": {map[string]string{"web/package.json": npmDeps, "api/package-lock.json": ""}, StatusFinding, []string{"lockfile:npm:web"}},
		"a deep folder finds one above":   {map[string]string{"a/b/c/package.json": npmDeps, "a/yarn.lock": ""}, StatusMet, nil},
		"cargo with Cargo.lock":           {map[string]string{"Cargo.toml": "", "Cargo.lock": ""}, StatusMet, nil},
		"cargo with none":                 {map[string]string{"Cargo.toml": ""}, StatusFinding, []string{"lockfile:cargo"}},
		"bundler":                         {map[string]string{"Gemfile": ""}, StatusFinding, []string{"lockfile:bundler"}},
		"composer with deps and no lock":  {map[string]string{"composer.json": `{"require":{"a/b":"1"}}`}, StatusFinding, []string{"lockfile:composer"}},
		"composer with require-dev only":  {map[string]string{"composer.json": `{"require-dev":{"a/b":"1"}}`, "composer.lock": ""}, StatusMet, nil},
		"composer with no deps":           {map[string]string{"composer.json": `{"require":{}}`}, StatusNotApplicable, nil},
		"maven has no lockfile":           {map[string]string{"pom.xml": ""}, StatusNotApplicable, nil},
		"gradle with the lockfile":        {map[string]string{"build.gradle.kts": "", "gradle.lockfile": ""}, StatusMet, nil},
		"gradle with none":                {map[string]string{"build.gradle": ""}, StatusFinding, []string{"lockfile:gradle"}},
		"swift":                           {map[string]string{"Package.swift": ""}, StatusFinding, []string{"lockfile:swift"}},
		"swift resolved":                  {map[string]string{"Package.swift": "", "Package.resolved": ""}, StatusMet, nil},
		"a skipped folder is no project":  {map[string]string{"node_modules/x/package.json": npmDeps, "vendor/y/go.mod": "require a v1"}, StatusNotApplicable, nil},
		"two managers, one finding":       {map[string]string{"go.mod": "require a v1", "go.sum": "", "Gemfile": ""}, StatusFinding, []string{"lockfile:bundler"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := Evaluate(input(tt.files))
			got := statusOf(t, r, Lockfile)
			if got.Status != tt.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Reason, tt.want)
			}
			if !slices.Equal(findingIDs(r, Lockfile), tt.ids) {
				t.Errorf("findings = %v, want %v", findingIDs(r, Lockfile), tt.ids)
			}
		})
	}
}

func TestLockfileWithAnUnreadableManifestIsUnknown(t *testing.T) {
	for _, tt := range []struct{ manifest, text string }{
		{"go.mod", "require a v1"},
		{"package.json", `{"dependencies":{"a":"1"}}`},
		{"composer.json", `{"require":{"a/b":"1"}}`},
		{"pyproject.toml", "[project]\ndependencies = [\"a\"]\n"},
		{"requirements.txt", "a==1\n"},
	} {
		t.Run(tt.manifest, func(t *testing.T) {
			in := input(map[string]string{tt.manifest: tt.text})
			in.Read = func(path string) (string, bool) { return "", false }
			r := Evaluate(in)
			got := statusOf(t, r, Lockfile)
			if got.Status != StatusUnknown || !strings.Contains(got.Reason, tt.manifest) {
				t.Errorf("lockfile = %+v", got)
			}
			if ids := findingIDs(r, Lockfile); len(ids) != 0 {
				t.Errorf("findings = %v", ids)
			}
		})
	}
}

func TestLockfileReasons(t *testing.T) {
	r := Evaluate(input(map[string]string{"pom.xml": ""}))
	if got := statusOf(t, r, Lockfile).Reason; got != "Maven has no lockfile" {
		t.Errorf("maven reason = %q", got)
	}
	r = Evaluate(input(map[string]string{"go.mod": "module m\n"}))
	if got := statusOf(t, r, Lockfile).Reason; got != "go.mod has no require directive" {
		t.Errorf("go reason = %q", got)
	}
	r = Evaluate(input(map[string]string{"web/package.json": `{"dependencies":{"a":"1"}}`}))
	if d := detailOf(r, Lockfile); !strings.Contains(d, "web") || !strings.Contains(d, "`npm install`") {
		t.Errorf("detail = %q", d)
	}
}

func TestLockfilePython(t *testing.T) {
	poetry := "[tool.poetry]\nname = \"x\"\n\n[tool.poetry.dependencies]\npython = \"^3.12\"\nrequests = \"^2\"\n"
	poetryEmpty := "[tool.poetry.dependencies]\npython = \"^3.12\"\n"
	pep621 := "[project]\nname = \"x\"\ndependencies = [\n  \"requests>=2\", # http\n]\n"
	pep621Empty := "[project]\nname = \"x\"\ndependencies = []\n[project.optional-dependencies]\ndev = [\"pytest\"]\n"
	pep621Inline := "[project]\ndependencies = [\"a\"]\n"
	for name, tt := range map[string]struct {
		files map[string]string
		want  Status
		ids   []string
	}{
		"poetry with poetry.lock":            {map[string]string{"pyproject.toml": poetry, "poetry.lock": ""}, StatusMet, nil},
		"poetry with no lock":                {map[string]string{"pyproject.toml": poetry}, StatusFinding, []string{"lockfile:python"}},
		"poetry with only python":            {map[string]string{"pyproject.toml": poetryEmpty}, StatusNotApplicable, nil},
		"pep 621 with uv.lock":               {map[string]string{"pyproject.toml": pep621, "uv.lock": ""}, StatusMet, nil},
		"pep 621 with pdm.lock":              {map[string]string{"pyproject.toml": pep621, "pdm.lock": ""}, StatusMet, nil},
		"pep 621 with no lock":               {map[string]string{"pyproject.toml": pep621}, StatusFinding, []string{"lockfile:python"}},
		"pep 621 inline with no lock":        {map[string]string{"pyproject.toml": pep621Inline}, StatusFinding, []string{"lockfile:python"}},
		"pep 621 with no dependencies":       {map[string]string{"pyproject.toml": pep621Empty}, StatusNotApplicable, nil},
		"pipfile with Pipfile.lock":          {map[string]string{"Pipfile": "", "Pipfile.lock": ""}, StatusMet, nil},
		"pipfile with none":                  {map[string]string{"Pipfile": ""}, StatusFinding, []string{"lockfile:python"}},
		"pinned requirements":                {map[string]string{"requirements.txt": "# c\nrequests==2.31.0\nflask[async]==3.0 ; python_version>'3'\n-r other.txt\n--hash=sha256:abc\n\n"}, StatusMet, nil},
		"unpinned requirements":              {map[string]string{"requirements.txt": "requests==2.31.0\nflask>=3\n"}, StatusFinding, []string{"lockfile:python"}},
		"a bare requirement":                 {map[string]string{"requirements.txt": "requests\n"}, StatusFinding, []string{"lockfile:python"}},
		"a url requirement":                  {map[string]string{"requirements.txt": "pkg @ https://example.com/p.zip\n"}, StatusFinding, []string{"lockfile:python"}},
		"unpinned requirements with a lock":  {map[string]string{"requirements.txt": "flask>=3\n", "uv.lock": ""}, StatusMet, nil},
		"empty pyproject with requirements":  {map[string]string{"pyproject.toml": pep621Empty, "requirements.txt": "a>=1\n"}, StatusFinding, []string{"lockfile:python"}},
		"a nested pinned requirements":       {map[string]string{"svc/requirements.txt": "a==1\n"}, StatusMet, nil},
		"a nested unpinned requirements":     {map[string]string{"svc/requirements.txt": "a\n"}, StatusFinding, []string{"lockfile:python:svc"}},
		"the root's pinned file is no lock":  {map[string]string{"requirements.txt": "a==1\n", "svc/requirements.txt": "a\n"}, StatusFinding, []string{"lockfile:python:svc"}},
		"a root lock covers a nested folder": {map[string]string{"uv.lock": "", "svc/requirements.txt": "a\n"}, StatusMet, nil},
	} {
		t.Run(name, func(t *testing.T) {
			r := Evaluate(input(tt.files))
			if got := statusOf(t, r, Lockfile); got.Status != tt.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Reason, tt.want)
			}
			if !slices.Equal(findingIDs(r, Lockfile), tt.ids) {
				t.Errorf("findings = %v, want %v", findingIDs(r, Lockfile), tt.ids)
			}
		})
	}
	t.Run("the finding names the manifest's lock command", func(t *testing.T) {
		r := Evaluate(input(map[string]string{"Pipfile": ""}))
		if d := detailOf(r, Lockfile); !strings.Contains(d, "`pipenv lock`") {
			t.Errorf("detail = %q", d)
		}
	})
}

func TestDependencyUpdates(t *testing.T) {
	with := func(extra string) map[string]string {
		files := map[string]string{"go.mod": "require a v1", "go.sum": ""}
		if extra != "" {
			files[extra] = ""
		}
		return files
	}
	for name, tt := range map[string]struct {
		files map[string]string
		want  Status
	}{
		"no manager":              {map[string]string{}, StatusNotApplicable},
		"none configured":         {with(""), StatusFinding},
		"dependabot yml":          {with(".github/dependabot.yml"), StatusMet},
		"dependabot yaml":         {with(".github/dependabot.yaml"), StatusMet},
		"renovate.json":           {with("renovate.json"), StatusMet},
		"renovate json5":          {with(".github/renovate.json5"), StatusMet},
		"gitlab renovate":         {with(".gitlab/renovate.json"), StatusMet},
		"renovaterc":              {with(".renovaterc"), StatusMet},
		"renovaterc json":         {with(".renovaterc.json"), StatusMet},
		"a nested dependabot":     {with("sub/.github/dependabot.yml"), StatusFinding},
		"a manager with no locks": {map[string]string{"pom.xml": ""}, StatusFinding},
	} {
		t.Run(name, func(t *testing.T) {
			if got := statusOf(t, Evaluate(input(tt.files)), DependencyUpdates).Status; got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSecurityPolicyAndCodeowners(t *testing.T) {
	for name, tt := range map[string]struct {
		path string
		id   ID
		want Status
	}{
		"SECURITY.md in the root":        {"SECURITY.md", SecurityPolicy, StatusMet},
		"security.md without case":       {"security.md", SecurityPolicy, StatusMet},
		"in .github":                     {".github/SECURITY.md", SecurityPolicy, StatusMet},
		"in docs":                        {"docs/SECURITY.md", SecurityPolicy, StatusMet},
		"in another folder":              {"web/SECURITY.md", SecurityPolicy, StatusFinding},
		"under .github/docs":             {".github/docs/SECURITY.md", SecurityPolicy, StatusFinding},
		"another name":                   {"SECURITY.txt", SecurityPolicy, StatusFinding},
		"CODEOWNERS in the root":         {"CODEOWNERS", Codeowners, StatusMet},
		"CODEOWNERS in .github":          {".github/CODEOWNERS", Codeowners, StatusMet},
		"CODEOWNERS in docs":             {"docs/CODEOWNERS", Codeowners, StatusMet},
		"codeowners in lower case":       {"codeowners", Codeowners, StatusFinding},
		"CodeOwners in mixed case":       {".github/CodeOwners", Codeowners, StatusFinding},
		"CODEOWNERS in another folder":   {"src/CODEOWNERS", Codeowners, StatusFinding},
		"a CODEOWNERS with an extension": {"CODEOWNERS.md", Codeowners, StatusFinding},
	} {
		t.Run(name, func(t *testing.T) {
			if got := statusOf(t, Evaluate(input(map[string]string{tt.path: ""})), tt.id).Status; got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLicense(t *testing.T) {
	public := &ForgeFacts{Visibility: VisibilityPublic}
	for name, tt := range map[string]struct {
		forge  *ForgeFacts
		files  map[string]string
		want   Status
		reason string
	}{
		"no forge facts":      {nil, nil, StatusNotApplicable, "no forge binding"},
		"a private repo":      {&ForgeFacts{Visibility: VisibilityPrivate}, nil, StatusNotApplicable, "the repository is not public"},
		"an internal repo":    {&ForgeFacts{Visibility: VisibilityInternal}, nil, StatusNotApplicable, "the repository is not public"},
		"unknown visibility":  {&ForgeFacts{VisibilityUnknown: "403 from the forge"}, nil, StatusUnknown, "403 from the forge"},
		"no visibility given": {&ForgeFacts{}, nil, StatusUnknown, "the forge gave no visibility"},
		"public with LICENSE": {public, map[string]string{"LICENSE": ""}, StatusMet, ""},
		"license.md":          {public, map[string]string{"license.md": ""}, StatusMet, ""},
		"LICENCE.txt":         {public, map[string]string{"LICENCE.txt": ""}, StatusMet, ""},
		"COPYING":             {public, map[string]string{"COPYING": ""}, StatusMet, ""},
		"LICENSE.md.orig":     {public, map[string]string{"LICENSE.md.orig": ""}, StatusMet, ""},
		"public with none":    {public, nil, StatusFinding, ""},
		"a nested license":    {public, map[string]string{"sub/LICENSE": ""}, StatusFinding, ""},
		"a license-like name": {public, map[string]string{"LICENSES": ""}, StatusFinding, ""},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(tt.files)
			in.Forge = tt.forge
			got := statusOf(t, Evaluate(in), License)
			if got.Status != tt.want || got.Reason != tt.reason {
				t.Errorf("license = %+v, want %s %q", got, tt.want, tt.reason)
			}
		})
	}
}

func TestTestsByManager(t *testing.T) {
	for name, tt := range map[string]struct {
		manifest string
		tests    []string
		not      []string
		manager  string
	}{
		"go": {"go.mod", []string{"a/b_test.go", "x_test.go"}, []string{"a/b.go", "testdata/x_test.go", "vendor/m/x_test.go"}, "go"},
		"npm": {"package.json", []string{"a.test.ts", "a.spec.jsx", "src/b.test.mjs", "c.spec.cts", "src/__tests__/helper.txt", "test.js"},
			[]string{"a.test.txt", "a.ts", "a.test", "node_modules/x/a.test.js", "contest.js"}, "npm"},
		"python":   {"pyproject.toml", []string{"test_a.py", "pkg/a_test.py"}, []string{"a.py", "test_a.txt", "conftest.py", "tests/a.py"}, "python"},
		"cargo":    {"Cargo.toml", []string{"tests/it.rs", "crate/tests/sub/it.rs"}, []string{"src/lib.rs", "tests/data.txt"}, "cargo"},
		"bundler":  {"Gemfile", []string{"test/a_test.rb", "spec/a_spec.rb"}, []string{"a.rb", "spec/helper.rb"}, "bundler"},
		"composer": {"composer.json", []string{"tests/AThingTest.php"}, []string{"tests/helper.php", "ATest.txt"}, "composer"},
		"maven": {"pom.xml", []string{"src/test/java/ATest.java", "m/src/test/java/ATests.java", "src/test/java/AIT.java", "src/test/java/TestA.java", "src/test/java/ATestCase.java", "src/test/java/ITA.java", "src/test/java/AITCase.java"},
			[]string{"src/main/java/ATest.java", "src/test/java/Helper.java", "src/test/resources/ATest.java.txt"}, "maven"},
		"gradle": {"build.gradle", []string{"src/test/kotlin/A.kt", "m/src/test/x.txt"}, []string{"src/main/A.kt", "test/A.kt"}, "gradle"},
		"swift":  {"Package.swift", []string{"Tests/AppTests/AppTests.swift", "Tests/x.swift"}, []string{"Sources/App/App.swift", "Tests/data.json"}, "swift"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, path := range tt.tests {
				r := Evaluate(input(map[string]string{tt.manifest: "", path: ""}))
				if got := statusOf(t, r, Tests).Status; got != StatusMet {
					t.Errorf("%s: tests = %s, want met", path, got)
				}
			}
			for _, path := range tt.not {
				r := Evaluate(input(map[string]string{tt.manifest: "", path: ""}))
				if got := statusOf(t, r, Tests).Status; got != StatusFinding {
					t.Errorf("%s: tests = %s, want finding", path, got)
				}
				if got := findingIDs(r, Tests); !slices.Equal(got, []string{"tests:" + tt.manager}) {
					t.Errorf("%s: findings = %v", path, got)
				}
			}
		})
	}
}

func TestTestsCargoReadsTestAttributes(t *testing.T) {
	files := map[string]string{"Cargo.toml": "", "src/lib.rs": "fn a() {}\n#[cfg(test)]\nmod t {\n #[test]\n fn ok() {}\n}\n"}
	if got := statusOf(t, Evaluate(input(files)), Tests).Status; got != StatusMet {
		t.Errorf("#[test] in a source file: %s", got)
	}
	files["src/lib.rs"] = "fn a() {}\n"
	if got := statusOf(t, Evaluate(input(files)), Tests).Status; got != StatusFinding {
		t.Errorf("no test: %s", got)
	}
}

func TestTestsCargoWithAnUnreadableSourceIsUnknown(t *testing.T) {
	in := input(map[string]string{"Cargo.toml": "", "src/lib.rs": "fn a() {}\n"})
	in.Read = func(path string) (string, bool) { return "", false }
	r := Evaluate(in)
	if got := statusOf(t, r, Tests); got.Status != StatusUnknown || got.Reason == "" {
		t.Errorf("tests = %+v", got)
	}
	if ids := findingIDs(r, Tests); len(ids) != 0 {
		t.Errorf("findings = %v", ids)
	}
	// A test found by its folder is met, whatever else cannot be read.
	in = input(map[string]string{"Cargo.toml": "", "src/lib.rs": "", "tests/it.rs": ""})
	in.Read = func(path string) (string, bool) { return "", false }
	if got := statusOf(t, Evaluate(in), Tests).Status; got != StatusMet {
		t.Errorf("tests with a tests folder = %s", got)
	}
}

func TestTestsNotApplicableWithNoManager(t *testing.T) {
	r := Evaluate(input(map[string]string{"a_test.go": ""}))
	if got := statusOf(t, r, Tests); got.Status != StatusNotApplicable || got.Reason == "" {
		t.Errorf("tests = %+v", got)
	}
}

func TestTestsFindingPerManager(t *testing.T) {
	r := Evaluate(input(map[string]string{"go.mod": "", "Gemfile": "", "web/package.json": "", "a_test.go": ""}))
	if got := findingIDs(r, Tests); !slices.Equal(got, []string{"tests:bundler", "tests:npm"}) {
		t.Errorf("findings = %v", got)
	}
}

func TestForgePractices(t *testing.T) {
	for name, tt := range map[string]struct {
		forge      *ForgeFacts
		protection Status
		review     Status
	}{
		"no binding":           {nil, StatusNotApplicable, StatusNotApplicable},
		"protected and review": {&ForgeFacts{Protected: true, ReviewCount: 1}, StatusMet, StatusMet},
		"protected, no review": {&ForgeFacts{Protected: true}, StatusMet, StatusFinding},
		"two reviews":          {&ForgeFacts{Protected: true, ReviewCount: 2}, StatusMet, StatusMet},
		"neither":              {&ForgeFacts{}, StatusFinding, StatusFinding},
		"review unreadable":    {&ForgeFacts{Protected: true, ReviewUnknown: "403"}, StatusMet, StatusUnknown},
		"both unreadable":      {&ForgeFacts{ProtectedUnknown: "rate limited", ReviewUnknown: "rate limited"}, StatusUnknown, StatusUnknown},
		"ruleset review only":  {&ForgeFacts{ProtectedUnknown: "404", ReviewCount: 1}, StatusUnknown, StatusMet},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(clean())
			in.Forge = tt.forge
			r := Evaluate(in)
			if got := statusOf(t, r, BranchProtection).Status; got != tt.protection {
				t.Errorf("branch-protection = %s, want %s", got, tt.protection)
			}
			if got := statusOf(t, r, RequiredReview).Status; got != tt.review {
				t.Errorf("required-review = %s, want %s", got, tt.review)
			}
		})
	}
	in := input(clean())
	in.Forge = &ForgeFacts{ReviewUnknown: "403 forbidden"}
	if got := statusOf(t, Evaluate(in), RequiredReview); got.Reason != "403 forbidden" {
		t.Errorf("reason = %q", got.Reason)
	}
	r := Evaluate(func() Input { in := input(clean()); in.Forge = &ForgeFacts{}; return in }())
	if got := findingIDs(r, BranchProtection); !slices.Equal(got, []string{"branch-protection"}) {
		t.Errorf("findings = %v", got)
	}
}

func TestReasonsOnlyOnUnknownAndNotApplicable(t *testing.T) {
	r := Evaluate(input(nil))
	for _, s := range r.Practices {
		if (s.Status == StatusMet || s.Status == StatusFinding) && s.Reason != "" {
			t.Errorf("%s is %s with reason %q", s.ID, s.Status, s.Reason)
		}
		if (s.Status == StatusNotApplicable || s.Status == StatusUnknown) && s.Reason == "" {
			t.Errorf("%s is %s with no reason", s.ID, s.Status)
		}
	}
}

func TestEvaluateNeedsNoReader(t *testing.T) {
	in := input(map[string]string{"go.mod": "require a v1", "Makefile": "hooks:\n"})
	in.Read = nil
	r := Evaluate(in)
	if got := statusOf(t, r, Lockfile).Status; got != StatusUnknown {
		t.Errorf("lockfile = %s, want unknown", got)
	}
	if got := statusOf(t, r, PreCommit).Status; got != StatusUnknown {
		t.Errorf("pre-commit = %s, want unknown", got)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	files := map[string]string{"go.mod": "require a v1", "web/package.json": `{"dependencies":{"a":"1"}}`, ".env": "A=1", "svc/requirements.txt": "a\n"}
	files["a_test.rb"] = ""
	files["Gemfile"] = ""
	files["Cargo.toml"] = ""
	secrets := []SecretMatch{
		{Rule: "github_token", Path: "a.yaml", Line: 9, Commit: "bbbb", Digest: "1a2b3c4d5e6f0001"},
		{Rule: "github_token", Path: "a.yaml", Line: 3, Commit: "aaaa", Digest: "1a2b3c4d5e6f0001"},
		{Rule: "github_token", Path: "a.yaml", Line: 3, Commit: "cccc", Digest: "2a2b3c4d5e6f0002"},
		{Rule: "api_key", Path: "b.txt", Line: 1, Commit: "dddd", Digest: "ffffffffffff0001"},
		{Rule: "api_key", Path: "b.txt", Line: 2, Commit: "eeee", Digest: "ffffffffffff0001"},
	}
	want := Evaluate(withSecrets(input(files), secrets))
	if got := findingIDs(want, Secrets); len(got) != 3 {
		t.Fatalf("secret findings = %v", got)
	}
	for _, f := range want.Findings {
		if f.Practice == Secrets && f.Digest == "1a2b3c4d5e6f0001" && (f.Commit != "aaaa" || f.Line != 3) {
			t.Errorf("a duplicate kept %s line %d, want the first by commit", f.Commit, f.Line)
		}
	}
	rng := rand.New(rand.NewPCG(7, 11))
	for range 50 {
		in := withSecrets(input(files), secrets)
		rng.Shuffle(len(in.Paths), func(i, j int) { in.Paths[i], in.Paths[j] = in.Paths[j], in.Paths[i] })
		rng.Shuffle(len(in.Secrets), func(i, j int) { in.Secrets[i], in.Secrets[j] = in.Secrets[j], in.Secrets[i] })
		if got := Evaluate(in); !reflect.DeepEqual(got, want) {
			t.Fatalf("a shuffled input gave a different result:\n%+v\n%+v", got, want)
		}
	}
}

func withSecrets(in Input, secrets []SecretMatch) Input {
	in.Secrets = slices.Clone(secrets)
	return in
}
