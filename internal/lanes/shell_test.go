package lanes

import (
	"strings"
	"testing"
	"time"
)

// Before a command, a shell reads `name[` on to its `]`, so the parser stops there. A run of such words is
// read in time that grows with their number, not with its square.
func TestOpenBracketWordsParseInLinearTime(t *testing.T) {
	best := func(size int) time.Duration {
		text := strings.Repeat("a=[ ", size/4)
		fastest := time.Duration(1<<63 - 1)
		// The fastest of several runs leaves out the ones a collection or a busy machine slowed, which the race detector makes
		// more of.
		for range 9 {
			start := time.Now()
			parseShell(text)
			fastest = min(fastest, time.Since(start))
		}
		return fastest
	}
	small, large := best(16<<10), best(64<<10)
	// Four times the text takes about four times as long when the reading is linear, and about sixteen when it is not.
	if large >= 8*small {
		t.Errorf("64 KiB of open brackets took %v, and 16 KiB took %v: that is %.1f times as long", large, small, float64(large)/float64(small))
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
