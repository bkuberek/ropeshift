package server_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
)

var updateGolden = flag.Bool("update", false, "write the golden JSON the web tests read")

// goldenDir is what the web tests read, so a change to a read model fails here until the file changes
// and fails there until the view does.
var goldenDir = filepath.Join("..", "..", "..", "web", "testdata")

var generatedID = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)

// steady names each generated ID by where it first appears, because only those differ between runs.
func steady(raw []byte) []byte {
	seen := map[string]string{}
	return generatedID.ReplaceAllFunc(raw, func(id []byte) []byte {
		name, ok := seen[string(id)]
		if !ok {
			name = fmt.Sprintf("generated-%d", len(seen)+1)
			seen[string(id)] = name
		}
		return []byte(name)
	})
}

func pretty(t *testing.T, value any) []byte {
	t.Helper()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func holdsGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".json")
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run this test with -update", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed: run this test with -update, and read the diff\ngot:\n%s", name, got)
	}
}

// TestGoldenReadModels writes what the web tests read, from a real ledger on a clock that repeats, so
// a test of the page runs on what the API serves and nothing a test author imagined.
func TestGoldenReadModels(t *testing.T) {
	d, _, _ := aWorld(t)
	for _, name := range api.Views {
		t.Run(name, func(t *testing.T) {
			var raw json.RawMessage
			if err := d.Client.ReadView(t.Context(), name, "", &raw); err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			holdsGolden(t, name, steady(pretty(t, value)))
		})
	}
}

// TestGoldenWebRoutes writes the closed list of what the web listener serves, so a web test can hold
// the application's own client to it.
func TestGoldenWebRoutes(t *testing.T) {
	holdsGolden(t, "routes", pretty(t, api.WebRoutes()))
}
