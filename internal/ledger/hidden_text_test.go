package ledger

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"unicode"
)

var update = flag.Bool("update", false, "write the hidden characters the web tests read")

func TestHiddenRuneIsAControlOrAFormatCharacter(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]rune{
		"NUL": 0x00, "BEL": 0x07, "newline": '\n', "DEL": 0x7f, "C1 CSI": 0x9b, "soft hyphen": 0xad, "Arabic letter mark": 0x061c,
		"zero-width space": 0x200b, "zero-width non-joiner": 0x200c, "zero-width joiner": 0x200d, "left-to-right mark": 0x200e,
		"right-to-left mark": 0x200f, "left-to-right embedding": 0x202a, "right-to-left override": 0x202e, "word joiner": 0x2060,
		"left-to-right isolate": 0x2066, "pop directional isolate": 0x2069, "byte order mark": 0xfeff, "tag space": 0xe0020,
	} {
		if !HiddenRune(r) {
			t.Errorf("%s (%U) is not hidden", name, r)
		}
	}
	for name, r := range map[string]rune{
		"letter": 'a', "space": ' ', "accented letter": 'é', "ideograph": '世', "emoji": '😀', "no-break space": 0xa0, "line separator": 0x2028,
	} {
		if HiddenRune(r) {
			t.Errorf("%s (%U) is hidden", name, r)
		}
	}
}

// TestTheWebUIHidesTheSameCharacters writes the characters HiddenRune names, as ranges of code points, where the web
// tests read them. Run it with -update after a change to the rule or to the Go toolchain's Unicode tables.
func TestTheWebUIHidesTheSameCharacters(t *testing.T) {
	var ranges [][2]rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !HiddenRune(r) {
			continue
		}
		if last := len(ranges) - 1; last >= 0 && ranges[last][1] == r-1 {
			ranges[last][1] = r
			continue
		}
		ranges = append(ranges, [2]rune{r, r})
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(map[string]any{"ranges": ranges}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "web", "testdata", "hidden-runes.json")
	if *update {
		if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run this test with -update", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("the hidden characters changed: run this test with -update, and read the diff\ngot:\n%s", out.Bytes())
	}
}
