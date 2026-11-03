package top

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestWhatATerminalSendsBecomesKeys(t *testing.T) {
	tests := []struct {
		name  string
		bytes string
		want  []string
	}{
		{"letters", "jkqr", []string{"j", "k", "q", "r"}},
		{"arrows", "\x1b[A\x1b[B", []string{KeyUp, KeyDown}},
		{"an arrow we do not use", "\x1b[C", nil},
		{"control c", "\x03", []string{KeyCtrlC}},
		{"escape on its own", "\x1b", []string{KeyEsc}},
		{"an escape with the rest of its sequence still to come", "\x1b[", nil},
		{"a byte no key sends", "\x00\xff", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readKeys([]byte(tt.bytes)); !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAFrameClearsWhatTheLastOneLeft(t *testing.T) {
	var out bytes.Buffer
	if err := paint(&out, "one\ntwo"); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.HasPrefix(text, homeCursor) || !strings.HasSuffix(text, clearBelow) {
		t.Errorf("frame %q", text)
	}
	if strings.Count(text, "\r\n") != 1 || strings.Count(text, clearLine) != 2 {
		t.Errorf("a raw terminal needs each line ended: %q", text)
	}
}
