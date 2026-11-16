package cli_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/delivery"
)

var updateText = flag.Bool("update", false, "write the text the web tests hold the pages to")

// The web UI shows delivery look back in the numbers and the words the command line prints. The goldens a Go test of the API wrote are what the web tests read, and the text here is what these
// commands print for the same goldens, so a web test holds a page to the command line's own text.

func goldenPath(name string) string { return filepath.Join("..", "..", "web", "testdata", name) }

func holdsText(t *testing.T, name string, got string) {
	t.Helper()
	if *updateText {
		if err := os.WriteFile(goldenPath(name), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath(name))
	if err != nil {
		t.Fatalf("%v: run this test with -update", err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Errorf("%s changed: run this test with -update, and read the diff\ngot:\n%s", name, got)
	}
}

func readGolden(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(goldenPath(name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestTheDeliveryGoldensPrintAsTheWebTestsRead(t *testing.T) {
	for _, name := range []string{"delivery", "delivery-size", "delivery-empty"} {
		var report delivery.LookBack
		readGolden(t, name+".json", &report)
		holdsText(t, name+".cli.txt", cli.LookBackText(report))
	}
}
