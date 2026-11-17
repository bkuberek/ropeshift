package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fileHolds says a file of the tree holds a text.
func fileHolds(t *testing.T, root, rel, text string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), text)
}

// TestOnlyTheGateNamesTheGroupVariable holds the preview gate (ADR-0031): the variable is named in one
// file, which only the entry points of group mode call, so solo mode cannot come to depend on it.
func TestOnlyTheGateNamesTheGroupVariable(t *testing.T) {
	root := repoRoot(t)
	found, err := CheckGate(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
	// The rule reads something: the gate file is where it says it is, and holds the name.
	if !fileHolds(t, root, gateFile, groupGateVariable) {
		t.Fatalf("%s does not name %s, so the gate check reads nothing", gateFile, groupGateVariable)
	}
}

// TestTheGateCheckCanFail proves each way the gate can leak: the variable named in another file, the
// identifier that holds it used elsewhere, and a call from a file that is no group entry point.
func TestTheGateCheckCanFail(t *testing.T) {
	gate := "package entry\n\nconst envGroupMode = \"ROPESHIFT_GROUP_MODE\"\n\nfunc groupPreview(getenv func(string) string) bool { return getenv(envGroupMode) == \"preview\" }\n"
	tests := []struct {
		name  string
		files map[string]string
		rule  string
		file  string
	}{
		{"the name in a solo file", map[string]string{
			"internal/entry/solo.go": "package entry\n\nvar gated = os.Getenv(\"ROPESHIFT_GROUP_MODE\")\n",
		}, "gate-one-reader", "internal/entry/solo.go"},
		{"the name below the entry", map[string]string{
			"internal/control/gate.go": "package control\n\nconst x = \"ROPESHIFT_GROUP_MODE\"\n",
		}, "gate-one-reader", "internal/control/gate.go"},
		{"the identifier in a solo file", map[string]string{
			"internal/entry/solo.go": "package entry\n\nvar gated = os.Getenv(envGroupMode)\n",
		}, "gate-one-reader", "internal/entry/solo.go"},
		{"a call from solo wiring", map[string]string{
			"internal/entry/solo.go": "package entry\n\nvar on = groupPreview(os.Getenv)\n",
		}, "gate-callers", "internal/entry/solo.go"},
		{"a call from a package below the entry", map[string]string{
			"internal/control/gate.go": "package control\n\nvar on = groupPreview(nil)\n",
		}, "gate-callers", "internal/control/gate.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{gateFile: gate}
			for name, text := range tt.files {
				files[name] = text
			}
			writeTree(t, root, files)
			found, err := CheckGate(root)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(found, func(v Violation) bool { return v.Rule == tt.rule && v.Package == tt.file }) {
				t.Errorf("%s is not refused under %s: %v", tt.file, tt.rule, found)
			}
		})
	}
}

// TestTheGateCheckAllowsItsOwnPlaces proves the check refuses nothing it should leave: the gate file, the
// files that dispatch commands, and a test file that sets the variable.
func TestTheGateCheckAllowsItsOwnPlaces(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		gateFile:                      "package entry\n\nconst envGroupMode = \"ROPESHIFT_GROUP_MODE\"\n\nfunc groupPreview(getenv func(string) string) bool { return false }\n",
		"internal/entry/entry.go":     "package entry\n\nvar on = groupPreview(os.Getenv)\n",
		"internal/entry/routes.go":    "package entry\n\nvar on = groupPreview(os.Getenv)\n",
		"internal/entry/solo_test.go": "package entry\n\nfunc init() { t.Setenv(\"ROPESHIFT_GROUP_MODE\", \"preview\") }\n",
	})
	found, err := CheckGate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("reported %v", found)
	}
}
