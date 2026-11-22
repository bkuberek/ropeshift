package claudecode_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// evidenceOf reads what a probe kept for this driver: each file's name and body. A directory that is not there
// reads as nothing kept.
func evidenceOf(t *testing.T, root string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, claudecode.DriverName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(root, claudecode.DriverName, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = string(raw)
	}
	return out
}

func names(evidence map[string]string) []string {
	var out []string
	for name := range evidence {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// requireMode fails the test when a path's permission bits are not exactly mode.
func requireMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Errorf("%s has mode %v, want %v", path, got, mode)
	}
}

// A check whose screen did not pass leaves that screen, and nothing of the checks that passed.
func TestAProbeKeepsTheScreenOfACheckThatFailed(t *testing.T) {
	driver, spec, _ := probeRig(t, "start_screens")
	spec.EvidenceDir = filepath.Join(t.TempDir(), "evidence")
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Checks[claudecode.SurfaceStartScreens] != lanes.CheckFailed {
		t.Fatalf("the screens read %s", q.Checks[claudecode.SurfaceStartScreens])
	}
	kept := evidenceOf(t, spec.EvidenceDir)
	if got := names(kept); !slices.Equal(got, []string{"screens.screen"}) {
		t.Fatalf("a probe whose only failure is the screens kept %v", got)
	}
	if !strings.Contains(kept["screens.screen"], "A screen no qualification has seen") {
		t.Errorf("the kept screen reads %q", kept["screens.screen"])
	}
	requireMode(t, spec.EvidenceDir, 0o700)
	requireMode(t, filepath.Join(spec.EvidenceDir, claudecode.DriverName), 0o700)
	requireMode(t, filepath.Join(spec.EvidenceDir, claudecode.DriverName, "screens.screen"), 0o600)
}

// A check that failed on a session's stream leaves that session's stream.
func TestAProbeKeepsTheStreamOfACheckThatFailed(t *testing.T) {
	driver, spec, _ := probeRig(t, "max_budget_usd")
	spec.EvidenceDir = filepath.Join(t.TempDir(), "evidence")
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Checks[claudecode.SurfaceMaxBudget] != lanes.CheckFailed {
		t.Fatalf("the budget reads %s", q.Checks[claudecode.SurfaceMaxBudget])
	}
	kept := evidenceOf(t, spec.EvidenceDir)
	if got := names(kept); !slices.Equal(got, []string{"budget.stream"}) {
		t.Fatalf("a probe whose only failure is the budget kept %v", got)
	}
	if body := kept["budget.stream"]; !strings.Contains(body, `"type":"result"`) || !strings.HasSuffix(body, "\n") {
		t.Errorf("the kept stream reads %q", body)
	}
	requireMode(t, filepath.Join(spec.EvidenceDir, claudecode.DriverName, "budget.stream"), 0o600)
}

// Only the newest probe's evidence stays: a later probe replaces what the earlier left, and one that passes every
// check leaves none.
func TestAProbeReplacesTheEvidenceOfTheOneBeforeIt(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence")
	for _, tt := range []struct {
		drop string
		want []string
	}{
		{"max_budget_usd", []string{"budget.stream"}},
		{"start_screens", []string{"screens.screen"}},
		{"", nil},
	} {
		var drops []string
		if tt.drop != "" {
			drops = []string{tt.drop}
		}
		driver, spec, _ := probeRig(t, drops...)
		spec.EvidenceDir = evidence
		if _, err := driver.Probe(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if got := names(evidenceOf(t, evidence)); !slices.Equal(got, tt.want) {
			t.Errorf("after a probe that drops %q the evidence is %v, want %v", tt.drop, got, tt.want)
		}
	}
}

// What a probe keeps is what a session showed, and never what it was given: not the environment, not the
// generated settings, and no file of the probe's own directories.
func TestAProbesEvidenceHoldsNoEnvironmentOrSettings(t *testing.T) {
	driver, spec, _ := probeRig(t, "max_budget_usd", "start_screens")
	const secret = "evidence-sentinel-4f9a"
	spec.Env = append(slices.Clone(spec.Env), "ROPESHIFT_EVIDENCE_SECRET="+secret, "ANTHROPIC_API_KEY="+secret)
	spec.EvidenceDir = filepath.Join(t.TempDir(), "evidence")
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	kept := evidenceOf(t, spec.EvidenceDir)
	if got := names(kept); !slices.Equal(got, []string{"budget.stream", "screens.screen"}) {
		t.Fatalf("the probe kept %v", got)
	}
	for name, body := range kept {
		for _, leak := range []string{secret, "crossSessionInbound", "mcpServers", spec.Binary} {
			if strings.Contains(body, leak) {
				t.Errorf("%s holds %q", name, leak)
			}
		}
	}
	var files []string
	if err := filepath.WalkDir(spec.EvidenceDir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, entry.Name())
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	if !slices.Equal(files, []string{"budget.stream", "screens.screen"}) {
		t.Errorf("the evidence directory holds %v", files)
	}
}

// Each file is bounded, and one that was cut says so.
func TestAProbesEvidenceIsBounded(t *testing.T) {
	const limit = 200
	t.Cleanup(claudecode.ShortenEvidence(limit))
	driver, spec, _ := probeRig(t, "max_budget_usd", "start_screens")
	spec.EvidenceDir = filepath.Join(t.TempDir(), "evidence")
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	kept := evidenceOf(t, spec.EvidenceDir)
	if _, ok := kept["budget.stream"]; !ok {
		t.Fatalf("the probe kept %v", names(kept))
	}
	for name, body := range kept {
		if len(body) > limit {
			t.Errorf("%s holds %d bytes, over the bound of %d", name, len(body), limit)
		}
	}
	if body := kept["budget.stream"]; len(body) != limit || !strings.HasSuffix(body, claudecode.EvidenceCut) {
		t.Errorf("a stream longer than the bound ends %q after %d bytes", body[max(len(body)-20, 0):], len(body))
	}
}

// A probe with no evidence directory keeps nothing, and in particular writes nothing where the process runs.
func TestAProbeWithNoEvidenceDirectoryKeepsNothing(t *testing.T) {
	driver, spec, _ := probeRig(t, "max_budget_usd")
	here := t.TempDir()
	t.Chdir(here)
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(here); err != nil || len(entries) != 0 {
		t.Errorf("a probe with no evidence directory left %v, %v", entries, err)
	}
}

// A probe on an API key does not run the login check, which reads unobserved by design, so a probe that passed every
// check it runs keeps no evidence.
func TestAProbeKeepsNoEvidenceForACheckItDoesNotRun(t *testing.T) {
	driver, spec, _ := probeRig(t)
	spec.SignIn = ports.SignInAPIKey
	spec.EvidenceDir = filepath.Join(t.TempDir(), "evidence")
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if got := names(evidenceOf(t, spec.EvidenceDir)); len(got) != 0 {
		t.Errorf("a probe that passed every check it runs kept %v", got)
	}
}
