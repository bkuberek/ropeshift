//go:build chaos

package chaos

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "effects.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAKilledWritersTornRecordIsSkipped(t *testing.T) {
	whole := `{"k":"attempted","op":"write","wo":"w1","fence":2}`
	torn := `{"k":"attempted","op":"write","wo":"w1","actor":"w`
	next := `{"k":"completed","op":"write","wo":"w1","fence":2}`
	want := []Record{
		{Kind: Attempted, Op: "write", WorkOrder: "w1", Fence: 2},
		{Kind: Completed, Op: "write", WorkOrder: "w1", Fence: 2},
	}
	cases := map[string]string{
		"on its own line":        whole + "\n" + torn + "\n" + next + "\n",
		"before the next record": whole + "\n" + torn + next + "\n",
		"as the last line":       whole + "\n" + next + "\n" + torn,
		"cut inside the opening": whole + "\n{\"k\"" + next + "\n",
		"a blank line":           whole + "\n\n" + next + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ReadEffectLog(writeLog(t, content))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("records = %+v, want %+v", got, want)
			}
		})
	}
}

func TestAMalformedCompleteRecordIsStillAnError(t *testing.T) {
	path := writeLog(t, `{"k":"attempted"}`+"\n"+`{"k":"completed","fence":"two"}`+"\n")
	if _, err := ReadEffectLog(path); err == nil || !strings.Contains(err.Error(), "malformed record") {
		t.Fatalf("err = %v, want a malformed record error", err)
	}
	path = writeLog(t, "not json at all\n")
	if _, err := ReadEffectLog(path); err == nil {
		t.Fatal("a line that is no record was accepted")
	}
}

func TestAppendKeepsATornTailOnItsOwnLine(t *testing.T) {
	path := writeLog(t, `{"k":"attempted","op":"write","wo":"w1","actor":"w`)
	log, err := OpenEffectLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if err := log.Append(Record{Kind: Completed, Op: "write", WorkOrder: "w1"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Count(line, `{"k":`) > 1 {
			t.Fatalf("two records share a line: %q", line)
		}
	}
	got, err := ReadEffectLog(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := []Record{{Kind: Completed, Op: "write", WorkOrder: "w1"}}; !slices.Equal(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}
