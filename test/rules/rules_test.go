package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const module = "ropeshift.dev/ropeshift"

// repoRoot is two levels up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDependencyDirection(t *testing.T) {
	pkgs, err := LoadPackages(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages found, so the rule checked nothing")
	}
	for _, v := range CheckDependencies(module, pkgs) {
		t.Error(v)
	}
}

// TestDependencyDirectionFixtures proves that each rule can fail: every fixture
// is a small tree that breaks exactly one rule.
func TestDependencyDirectionFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		rule    string
	}{
		{"domain reaches the operating system", "domain-impure-std", "domain-pure"},
		{"domain reaches a third-party module", "domain-third-party", "domain-pure"},
		{"domain imports ports", "domain-imports-ports", "domain-pure"},
		{"ports import an adapter", "ports-import-adapter", "ports-domain-only"},
		{"ports import the operating system", "ports-impure-std", "ports-domain-only"},
		{"control imports an adapter", "control-imports-adapter", "only-entry-wires"},
		{"worker imports an adapter", "worker-imports-adapter", "only-entry-wires"},
		{"control imports worker", "control-imports-worker", "planes-apart"},
		{"worker imports control", "worker-imports-control", "planes-apart"},
		{"control imports a third-party module", "control-third-party", "application-inward"},
		{"api wires an adapter", "api-imports-adapter", "only-entry-wires"},
		{"a main wires an adapter", "main-imports-adapter", "thin-mains"},
		{"a client reaches the Store port", "client-imports-ports", "clients-through-api"},
		{"a client reaches an adapter", "client-imports-adapter", "clients-through-api"},
		{"the hook reaches an adapter directly", "hook-imports-adapter", "hook-no-adapter"},
		{"the hook reaches an adapter through a domain package", "hook-transitive-adapter", "hook-no-adapter"},
		{"an adapter imports the application", "adapter-imports-control", "adapters-outward"},
		{"a package has no layer", "unclassified", "unclassified"},
		{"crashpoint imports the module", "support-imports-module", "support-leaf"},
		{"a client imports the API handlers", "client-imports-api-server", "clients-through-api"},
		{"a client reaches the Store port through the API package", "client-transitive-store", "clients-through-api"},
		{"the API client imports the application", "api-client-imports-control", "api-client-light"},
		{"the API client imports the ports", "api-client-imports-ports", "api-client-light"},
		{"the API handlers wire an adapter", "api-server-imports-adapter", "only-entry-wires"},
		{"an adapter imports the API handlers", "adapter-imports-api-server", "adapters-outward"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkgs, err := LoadPackages(filepath.Join("testdata", "dependencies", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if len(pkgs) == 0 {
				t.Fatalf("fixture %s holds no packages", tt.fixture)
			}
			violations := CheckDependencies(module, pkgs)
			rules := make([]string, 0, len(violations))
			for _, v := range violations {
				rules = append(rules, v.Rule)
			}
			if !slices.Contains(rules, tt.rule) {
				t.Errorf("rule %s not reported, got %v", tt.rule, violations)
			}
		})
	}
}

// TestAllowedAPIShapes proves the rules do not refuse the layout they describe.
func TestAllowedAPIShapes(t *testing.T) {
	for _, fixture := range []string{"adapter-imports-api-client", "api-server-is-allowed"} {
		t.Run(fixture, func(t *testing.T) {
			pkgs, err := LoadPackages(filepath.Join("testdata", "dependencies", fixture))
			if err != nil {
				t.Fatal(err)
			}
			if violations := CheckDependencies(module, pkgs); len(violations) != 0 {
				t.Errorf("reported %v", violations)
			}
		})
	}
}

func TestEveryPortHasAFake(t *testing.T) {
	violations, err := CheckPortFakes(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

func TestEveryPortHasAFakeFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		rule    string
		detail  string
	}{
		{"a port with no fake", "port-without-fake", "port-has-fake", "Mailbox"},
		{"a file with no port", "port-file-mismatch", "port-file", "Outbox"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations, err := CheckPortFakes(filepath.Join("testdata", "ports", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range violations {
				if v.Rule == tt.rule && strings.Contains(v.Detail, tt.detail) {
					return
				}
			}
			t.Errorf("rule %s about %s not reported, got %v", tt.rule, tt.detail, violations)
		})
	}
}

func TestPortWithAFakePasses(t *testing.T) {
	violations, err := CheckPortFakes(filepath.Join("testdata", "ports", "port-with-fake"))
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Errorf("a port with a fake reported %v", violations)
	}
}

// TestNoTCPListener holds solo mode to its unix sockets (FR-DEP-1): nothing in this
// module opens a TCP listener yet.
func TestNoTCPListener(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, []string{
		`net.Listen("tcp`, `ListenAndServe(`, `ListenConfig{`, `net.ListenTCP(`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found {
		t.Error(hit)
	}
}

func TestFixturesStayOutOfTheBuild(t *testing.T) {
	// A fixture under a directory named testdata is invisible to ./... patterns.
	if _, err := os.Stat(filepath.Join("testdata", "dependencies")); err != nil {
		t.Fatal(err)
	}
}
