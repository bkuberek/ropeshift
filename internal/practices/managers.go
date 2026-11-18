package practices

import (
	"slices"
	"strings"
)

// Manager is one package manager the check detects by its manifest file.
type Manager struct {
	Name      string
	Manifests []string
	// Lockfiles are the files that lock the manager's dependencies; empty for a manager with none.
	Lockfiles []string
	// NoLockfile is the reason the lockfile practice does not apply to a manager with no lockfile.
	NoLockfile string
	// NotApplicable says when the lockfile practice does not apply to a manifest, in words.
	NotApplicable string
	// LockCommand writes the manager's lockfile, empty for a manager with none.
	LockCommand string
	// ManifestLockCommands holds a lock command for a manifest where it differs from LockCommand.
	ManifestLockCommands map[string]string
	// Tests says the manager's test file convention, in words.
	Tests string
	// Ecosystem is the manager's `package-ecosystem` value in a Dependabot configuration.
	Ecosystem string

	isTest func(path string, read func(string) (string, bool)) bool
}

// LockCommandFor is the command that locks a manifest of the manager.
func (m Manager) LockCommandFor(manifest string) string {
	if command, ok := m.ManifestLockCommands[manifest]; ok {
		return command
	}
	return m.LockCommand
}

var skippedSegments = []string{"node_modules", "vendor", "third_party", "testdata", ".git"}

// SkippedSegments names the folders detection skips: a path with a segment of one of these names is read by no
// package manager rule.
func SkippedSegments() []string { return slices.Clone(skippedSegments) }

// IsSkipped says a path has a segment named in SkippedSegments.
func IsSkipped(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if slices.Contains(skippedSegments, segment) {
			return true
		}
	}
	return false
}

var managers = []Manager{
	{
		Name: "go", Manifests: []string{"go.mod"}, Lockfiles: []string{"go.sum"},
		NotApplicable: "go.mod has no require directive",
		LockCommand:   "go mod tidy", Tests: "*_test.go", Ecosystem: "gomod",
		isTest: func(path string, _ func(string) (string, bool)) bool { return strings.HasSuffix(path, "_test.go") },
	},
	{
		Name: "npm", Manifests: []string{"package.json"},
		Lockfiles:     []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"},
		NotApplicable: "package.json has no non-empty dependencies, devDependencies, or optionalDependencies",
		LockCommand:   "npm install",
		Tests:         "*.test.* or *.spec.* with extension js, jsx, ts, tsx, mjs, cjs, mts, or cts, or any file under a __tests__ folder",
		Ecosystem:     "npm",
		isTest:        isNpmTest,
	},
	{
		Name: "python", Manifests: []string{"pyproject.toml", "Pipfile", "requirements.txt"},
		Lockfiles:     []string{"poetry.lock", "uv.lock", "pdm.lock", "Pipfile.lock"},
		NotApplicable: "pyproject.toml declares no dependency under [project] dependencies or [tool.poetry.dependencies] other than python, and no other manifest exists",
		LockCommand:   "uv lock",
		ManifestLockCommands: map[string]string{
			"Pipfile":          "pipenv lock",
			"requirements.txt": "uv pip compile requirements.txt -o requirements.txt",
		},
		Tests: "test_*.py or *_test.py", Ecosystem: "pip",
		isTest: func(path string, _ func(string) (string, bool)) bool {
			_, name := splitPath(path)
			return strings.HasSuffix(name, ".py") && (strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py"))
		},
	},
	{
		Name: "cargo", Manifests: []string{"Cargo.toml"}, Lockfiles: []string{"Cargo.lock"},
		LockCommand: "cargo generate-lockfile",
		Tests:       "any .rs file under a tests folder, or a .rs file containing #[test]",
		Ecosystem:   "cargo",
		isTest:      isCargoTest,
	},
	{
		Name: "bundler", Manifests: []string{"Gemfile"}, Lockfiles: []string{"Gemfile.lock"},
		LockCommand: "bundle lock", Tests: "*_test.rb or *_spec.rb", Ecosystem: "bundler",
		isTest: func(path string, _ func(string) (string, bool)) bool {
			return strings.HasSuffix(path, "_test.rb") || strings.HasSuffix(path, "_spec.rb")
		},
	},
	{
		Name: "composer", Manifests: []string{"composer.json"}, Lockfiles: []string{"composer.lock"},
		NotApplicable: "composer.json has no non-empty require or require-dev",
		LockCommand:   "composer update", Tests: "*Test.php", Ecosystem: "composer",
		isTest: func(path string, _ func(string) (string, bool)) bool { return strings.HasSuffix(path, "Test.php") },
	},
	{
		Name: "maven", Manifests: []string{"pom.xml"}, NoLockfile: "Maven has no lockfile",
		Tests:     "Test*.java, *Test.java, *Tests.java, *TestCase.java, IT*.java, *IT.java, or *ITCase.java under src/test/",
		Ecosystem: "maven",
		isTest:    isMavenTest,
	},
	{
		Name: "gradle", Manifests: []string{"build.gradle", "build.gradle.kts"}, Lockfiles: []string{"gradle.lockfile"},
		LockCommand: "gradle dependencies --write-locks", Tests: "any file under src/test/", Ecosystem: "gradle",
		isTest: func(path string, _ func(string) (string, bool)) bool { return hasSegments(path, "src", "test") },
	},
	{
		Name: "swift", Manifests: []string{"Package.swift"}, Lockfiles: []string{"Package.resolved"},
		LockCommand: "swift package resolve", Tests: "any .swift file under a Tests folder", Ecosystem: "swift",
		isTest: func(path string, _ func(string) (string, bool)) bool {
			return strings.HasSuffix(path, ".swift") && underFolder(path, "Tests")
		},
	},
}

// Managers returns the package manager table in its order.
func Managers() []Manager {
	out := make([]Manager, len(managers))
	for i, m := range managers {
		m.Manifests = slices.Clone(m.Manifests)
		m.Lockfiles = slices.Clone(m.Lockfiles)
		if m.ManifestLockCommands != nil {
			copied := make(map[string]string, len(m.ManifestLockCommands))
			for k, v := range m.ManifestLockCommands {
				copied[k] = v
			}
			m.ManifestLockCommands = copied
		}
		out[i] = m
	}
	return out
}

// managerNamed finds a manager by name.
func managerNamed(name string) (Manager, bool) {
	for _, m := range managers {
		if m.Name == name {
			return m, true
		}
	}
	return Manager{}, false
}

// underFolder says a path sits in a folder with a segment of that name, not counting its own file name.
func underFolder(path, folder string) bool {
	dir, _ := splitPath(path)
	return slices.Contains(strings.Split(dir, "/"), folder)
}

// hasSegments says a path's folders hold the segments one after the other.
func hasSegments(path string, want ...string) bool {
	dir, _ := splitPath(path)
	parts := strings.Split(dir, "/")
	for i := 0; i+len(want) <= len(parts); i++ {
		if slices.Equal(parts[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

var npmTestExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "cjs", "mts", "cts"}

func isNpmTest(path string, _ func(string) (string, bool)) bool {
	if underFolder(path, "__tests__") {
		return true
	}
	_, name := splitPath(path)
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || !slices.Contains(npmTestExtensions, name[dot+1:]) {
		return false
	}
	stem := name[:dot]
	return stem == "test" || stem == "spec" || strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec")
}

func isCargoTest(path string, read func(string) (string, bool)) bool {
	if !strings.HasSuffix(path, ".rs") {
		return false
	}
	if underFolder(path, "tests") {
		return true
	}
	if read == nil {
		return false
	}
	text, ok := read(path)
	return ok && strings.Contains(text, "#[test]")
}

func isMavenTest(path string, _ func(string) (string, bool)) bool {
	if !hasSegments(path, "src", "test") {
		return false
	}
	_, name := splitPath(path)
	stem, ok := strings.CutSuffix(name, ".java")
	if !ok {
		return false
	}
	for _, prefix := range []string{"Test", "IT"} {
		if strings.HasPrefix(stem, prefix) {
			return true
		}
	}
	for _, suffix := range []string{"Test", "Tests", "TestCase", "IT", "ITCase"} {
		if strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	return false
}
