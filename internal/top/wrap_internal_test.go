package top

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A need's words wrap within the width however narrow the room beside its head is, and a word wider than
// a line breaks where the line does, so no line is wider than the frame and no letter is lost.
func TestAWrappedNeedKeepsEveryLetterWithinTheWidth(t *testing.T) {
	text := "short " + strings.Repeat("x", 70) + " and a few more words after it"
	for _, tc := range []struct {
		name  string
		head  string
		width int
	}{
		{"room beside the head", "> 1m  work wo-1  ", 50},
		{"too little room beside the head", "> 1m  a-project-name  work wo-1  ", 40},
		{"a head wider than the frame", "> 1m  a-project-name  work wo-1  ", 20},
		{"a frame of two columns", "> ", 2},
	} {
		lines := wrapAfter(tc.head, text, tc.width)
		for _, line := range lines {
			if utf8.RuneCountInString(line) > max(tc.width, utf8.RuneCountInString(strings.TrimRight(tc.head, " "))) {
				t.Errorf("%s: %q is wider than %d", tc.name, line, tc.width)
			}
		}
		if got, want := strings.ReplaceAll(strings.Join(lines, ""), " ", ""), strings.ReplaceAll(tc.head+text, " ", ""); got != want {
			t.Errorf("%s: wrapped text reads %q, want %q", tc.name, got, want)
		}
	}
	if lines := wrapAfter("> ", "fits", 80); len(lines) != 1 || lines[0] != "> fits" {
		t.Errorf("text that fits wraps to %q", lines)
	}
}
