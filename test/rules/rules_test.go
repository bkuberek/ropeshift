package rules

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
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
// TestOneAuthorizationFunction holds FR-AUTH-9: every check passes through one function,
// so optional roles can arrive with no change to the data model.
func TestOneAuthorizationFunction(t *testing.T) {
	found, err := CheckOneAuthorization(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "any"}
	for _, action := range authority.Actions() {
		if !authority.Allowed(member, action) {
			t.Errorf("the one function refuses a member %s", action)
		}
	}
}

func TestOneAuthorizationFunctionCanFail(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "control")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `package control

import "ropeshift.dev/ropeshift/internal/ledger"

var member = ledger.Actor{Kind: ledger.ActorMember}

func check(actor ledger.Actor) bool { return actor.Kind == ledger.ActorMember }

func other(actor ledger.Actor) bool {
	switch actor.Kind {
	case ledger.ActorLane:
		return true
	}
	return false
}
`
	if err := os.WriteFile(filepath.Join(dir, "check.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := CheckOneAuthorization(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %v, want the comparison and the case, and not the literal", found)
	}
}

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

// webListenerFile is the one file that may open a TCP listener: ListenWeb, which refuses anything but
// a loopback address, and a test holds it to that.
const webListenerFile = "internal/api/server/listen.go"

// TestOneTCPListener holds solo mode to its unix sockets and the one loopback web listener the design
// names (FR-DEP-1), so a second way onto the network cannot arrive unnoticed.
func TestOneTCPListener(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, []string{
		`ListenAndServe(`, `ListenConfig{`, `net.ListenTCP(`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found {
		t.Error(hit)
	}
	bound, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, []string{`net.Listen("tcp`})
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 1 || !strings.Contains(bound[0], webListenerFile) {
		t.Errorf("TCP listeners at %v, and only %s may hold one", bound, webListenerFile)
	}
}

// TestNoShellInTheSource backs the runner's refusal: nothing in the module builds a
// command line for a shell (FR-DRV-10).
func TestNoShellInTheSource(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, []string{
		`exec.Command("sh`, `exec.Command("bash`, `exec.Command("zsh`, `"-lc"`, `"-c",`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found {
		t.Error(hit)
	}
}

// vendorFiles are what ADR-0002 and NFR-COMPAT-1 keep out of the code: a transcript
// and a job file are formats the vendor documents as unstable.
var vendorFiles = []string{"/.claude/projects", "/.claude/jobs", ".claude/jobs/", "transcript_path", ".jsonl\""}

// TestNoVendorFileReads finds no read of a transcript or a job file.
func TestNoVendorFileReads(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, vendorFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found {
		t.Error(hit)
	}
}

// TestNoVendorFileReadsCanFail runs the same rule on a tree that breaks it.
func TestNoVendorFileReadsCanFail(t *testing.T) {
	found, err := FindSource(filepath.Join("testdata", "vendorfiles"), []string{"internal"}, vendorFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Error("the rule found nothing in a tree that reads a transcript")
	}
}

// agentsListing finds a command line that runs the CLI's own listing.
var agentsListing = []string{`"agents"`}

// listingFile is the one file that may run the listing. It reads as `idle` for a hung
// process and drops a killed one with no record, so it is a read at adoption and never
// a state source.
const listingFile = "internal/adapters/claudecode/adoption.go"

// TestAgentsListingOnlyInAdoption finds `claude agents` nowhere but adoption.
func TestAgentsListingOnlyInAdoption(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, agentsListing)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, hit := range found {
		if strings.HasPrefix(hit, listingFile) {
			listed = true
			continue
		}
		t.Error(hit)
	}
	if !listed {
		t.Errorf("the listing is not in %s, so this rule is checking the wrong file", listingFile)
	}
}

// TestAgentsListingRuleCanFail runs the same rule on a tree that polls the listing.
func TestAgentsListingRuleCanFail(t *testing.T) {
	found, err := FindSource(filepath.Join("testdata", "agentslisting"), []string{"internal"}, agentsListing)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Error("the rule found nothing in a tree that polls the listing")
	}
}

// screenRead finds a read of a pane's screen.
var screenRead = []string{".Capture(", "capture-pane"}

// The two files that may read a screen: the start check, which is the one use on a
// qualified version, and the adapter, which is how it reads.
var screenFiles = []string{"internal/adapters/claudecode/startcheck.go", "internal/adapters/tmux/tmux.go"}

// TestScreenReadsOnlyInStartCheck finds no read of a screen outside the start check. From
// `briefing` onward state and input go through hooks and the messaging socket.
func TestScreenReadsOnlyInStartCheck(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, screenRead)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, hit := range found {
		file, _, _ := strings.Cut(hit, " holds ")
		if !slices.Contains(screenFiles, file) {
			t.Error(hit)
		}
		seen[file] = true
	}
	for _, file := range screenFiles {
		if !seen[file] {
			t.Errorf("no read of a screen in %s, so this rule is checking the wrong file", file)
		}
	}
}

// TestScreenRuleCanFail runs the same rule on a tree that reads a pane for state.
func TestScreenRuleCanFail(t *testing.T) {
	found, err := FindSource(filepath.Join("testdata", "screenreads"), []string{"internal"}, screenRead)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Error("the rule found nothing in a tree that reads a pane for state")
	}
}

// markingFields are the names that would let a caller say what a lane is.
var markingFields = []string{"ready", "briefed", "actual_state", "liveness", "conditions", "reachability", "model_reach", "ledger_reach"}

// markingField walks a request's fields, by their wire names, for one that could mark a
// lane. A lane's state derives from what happened (FR-LANE-5).
func markingField(t reflect.Type, seen map[reflect.Type]bool) (string, bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return "", false
	}
	seen[t] = true
	for field := range t.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if slices.Contains(markingFields, name) {
			return t.Name() + "." + name, true
		}
		if found, ok := markingField(field.Type, seen); ok {
			return found, true
		}
	}
	return "", false
}

// TestNoFieldMarksALane reads every request a person or an agent can send.
func TestNoFieldMarksALane(t *testing.T) {
	requests := []reflect.Type{
		reflect.TypeFor[api.CreateWorkOrder](), reflect.TypeFor[api.ClaimRequest](), reflect.TypeFor[api.StartLane](),
		reflect.TypeFor[api.ReleaseRequest](), reflect.TypeFor[api.ReassignRequest](), reflect.TypeFor[api.WriteRequest](),
		reflect.TypeFor[lanes.Request](),
	}
	for _, request := range requests {
		if found, ok := markingField(request, map[reflect.Type]bool{}); ok {
			t.Errorf("%s can mark a lane", found)
		}
	}
}

// TestNoFieldMarksALaneCanFail runs the same walk on a request that breaks it.
func TestNoFieldMarksALaneCanFail(t *testing.T) {
	type marked struct {
		Inner []struct {
			Ready bool `json:"ready"`
		} `json:"inner"`
	}
	if _, ok := markingField(reflect.TypeFor[marked](), map[reflect.Type]bool{}); !ok {
		t.Error("the walk found nothing in a request that marks a lane")
	}
}

// laneWrites are the routes that would write a lane's state.
var laneWrites = []string{`"POST /v1/lanes`, `"PUT /v1/lanes`, `"PATCH /v1/lanes`, `"DELETE /v1/lanes`}

// TestNoRouteWritesALane finds no client write on a lane. A lane starts through its work
// order, and everything after that derives from what happened.
func TestNoRouteWritesALane(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"internal"}, laneWrites)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found {
		t.Error(hit)
	}
}

// TestCanaryIsFirstAndFails proves the canary can fail a run: a copy of it, executed,
// exits with an error and leaves its mark.
func TestCanaryIsFirstAndFails(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "test", "doubles", "canary", "claude"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copied := filepath.Join(dir, "claude")
	if err := os.WriteFile(copied, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(copied, "-p")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err == nil {
		t.Error("the canary exited 0")
	}
	if _, err := os.Stat(filepath.Join(dir, "tripped")); err != nil {
		t.Errorf("the canary left no mark: %v", err)
	}
}

// TestCoreTestsPassWithNoGit runs the domain and application tests with a PATH that
// holds the Go tool and nothing else (NFR-TEST-1).
func TestCoreTestsPassWithNoGit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a second test binary")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(goTool, filepath.Join(bin, "go")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goTool, "test", "-count=1", "-buildvcs=false",
		"./internal/ledger/...", "./internal/planning/...", "./internal/workorders/...", "./internal/lanes/...",
		"./internal/control/...", "./internal/worker/...")
	cmd.Dir = repoRoot(t)
	cmd.Env = []string{
		"PATH=" + bin, "HOME=" + os.Getenv("HOME"), "GOCACHE=" + goEnv(t, "GOCACHE"), "GOMODCACHE=" + goEnv(t, "GOMODCACHE"),
		"GOROOT=" + goEnv(t, "GOROOT"), "GOFLAGS=-mod=readonly", "CGO_ENABLED=0",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "internal/worker") {
		t.Fatalf("the worker's tests did not run:\n%s", out)
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

func TestFixturesStayOutOfTheBuild(t *testing.T) {
	// A fixture under a directory named testdata is invisible to ./... patterns.
	if _, err := os.Stat(filepath.Join("testdata", "dependencies")); err != nil {
		t.Fatal(err)
	}
}

// credentialFiles are what NFR-SEC-1 keeps out of the code: the places a CLI keeps a sign-in.
var credentialFiles = []string{".credentials.json", "credentials.json", "/.claude.json", "oauthAccount"}

// TestNoCredentialFileReads finds no read of a place a CLI keeps a sign-in.
func TestNoCredentialFileReads(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, credentialFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found {
		t.Error(hit)
	}
}

// signInCommands are the words of a command that changes a sign-in, which no code here runs.
var signInCommands = []string{`"logout"`, `"setup-token"`, `"/login"`, `"/logout"`}

// refusedList is the file that names these words in order to refuse them.
const refusedList = "internal/lanes/override_real.go"

// outsideRefusedList drops what that one file holds.
func outsideRefusedList(found []string) []string {
	var kept []string
	for _, hit := range found {
		if !strings.Contains(hit, refusedList) {
			kept = append(kept, hit)
		}
	}
	return kept
}

// signInRead finds the one vendor command that touches a sign-in.
var signInRead = []string{`"auth"`}

// signInFile is the one file that may run it, and it runs only `auth status`.
const signInFile = "internal/adapters/claudecode/doctor.go"

// TestOnlyAStatusReadTouchesASignIn holds the doctor to reading. A command that changes a
// sign-in would disturb every session on this login.
func TestOnlyAStatusReadTouchesASignIn(t *testing.T) {
	found, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, signInCommands)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range outsideRefusedList(found) {
		t.Error(hit)
	}
	reads, err := FindSource(repoRoot(t), []string{"cmd", "internal"}, signInRead)
	if err != nil {
		t.Fatal(err)
	}
	reads = outsideRefusedList(reads)
	if len(reads) != 1 || !strings.Contains(reads[0], signInFile) {
		t.Errorf("the status read is at %v, and only %s may hold it", reads, signInFile)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), signInFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `[]string{"auth", "status"}`) {
		t.Error("the doctor runs something other than the status read")
	}
}

// networkPackages are what a package needs to reach a model or anything else off the machine.
var networkPackages = []string{"net", "net/http", "crypto/tls"}

func reachesNetwork(deps []string) []string {
	var found []string
	for _, dep := range deps {
		if slices.Contains(networkPackages, dep) {
			found = append(found, dep)
		}
	}
	return found
}

// TestLexicalMatchingNeedsNoNetwork holds ADR-0007's default install: a question finds its
// candidates with no model and no network, so the import closure cannot hold a way out.
func TestLexicalMatchingNeedsNoNetwork(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "./internal/adapters/lexical", "./internal/questions")
	command.Dir = repoRoot(t)
	out, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if found := reachesNetwork(strings.Fields(string(out))); len(found) != 0 {
		t.Errorf("the lexical matcher reaches %v", found)
	}
	if len(reachesNetwork([]string{"strings", "net/http"})) != 1 {
		t.Error("the rule cannot fail")
	}
}
