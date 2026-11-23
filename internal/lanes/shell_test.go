package lanes

import (
	"strings"
	"testing"
)

// Before a command, a shell reads `name[` on to its `]`, so the parser stops there, and a run of such words must not make it
// look at each earlier word of the command again for each word it takes: that is quadratic work, which a bound on time cannot
// tell from a slow run on a shared machine. The parser counts the words it looks at again to place a keyword. A step looks at
// no more than the first maxCommandLead words of a command, and no `((` stands in these texts, so the count of a text is the
// same however long the text runs: it grows with the words only where the parser looks at all of them again.
func TestOpenBracketWordsParseInLinearTime(t *testing.T) {
	const limit = (maxCommandLead + 1) * (maxCommandLead + 1)
	for _, unit := range []string{"a=[ ", "a=1 b=[ ", "a=[1] ", "do ", "esac ", "if then "} {
		for _, repeats := range []int{1 << 7, 1 << 9, 1 << 11, 1 << 13} {
			text := strings.Repeat(unit, repeats)
			if looked := parseScript(text).looked; looked > limit {
				t.Errorf("%q repeated %d times (%d words): the parser looked at %d words again, over the %d a command's leading words come to",
					unit, repeats, strings.Count(text, " "), looked, limit)
			}
		}
	}
}

// A subscript before the command is unread where every word before it is an assignment or a keyword, in each
// command of a line and each substitution.
func TestAnOpenBracketBeforeTheCommandIsUnread(t *testing.T) {
	tests := []struct {
		text   string
		unread bool
	}{
		{"a=[ b", true},
		{"a=1 b=[ c", true},
		{"if a=[ b", true},
		{"time a=[ b", true},
		{"echo x | a=[ b", true},
		{"echo x; a=[ b", true},
		{"echo $(a=[ b)", true},
		{"echo x a=[ b", false},
		{"echo a=[ b", false},
		{"a=1 echo b=[ c", false},
		{"a=(x y) b=[ c", true},
		{"a=[1] echo", false},
	}
	for _, tt := range tests {
		if got := parseScript(tt.text).unread; got != tt.unread {
			t.Errorf("%q: unread %v, want %v", tt.text, got, tt.unread)
		}
	}
}
