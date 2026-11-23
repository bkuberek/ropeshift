package lanes

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// Before a command, a shell reads `name[` on to its `]`, so the parser stops there. A run of such words is read in time
// that grows with their number, not with its square. The text grows sixteen-fold, so a linear parse takes about sixteen times
// as long and a quadratic one about two hundred and fifty-six, and a bound of sixty-four stands between them with room for the
// slow runs a collection or a loaded machine makes: only the fastest run of a size counts, and the large size, whose runs are
// the long ones, is tried until one is within the bound.
func TestOpenBracketWordsParseInLinearTime(t *testing.T) {
	const small, large, bound = 16 << 10, 256 << 10, 64
	timed := func(size int) time.Duration {
		text := strings.Repeat("a=[ ", size/4)
		runtime.GC()
		start := time.Now()
		parseShell(text)
		return time.Since(start)
	}
	fastest := time.Duration(1<<63 - 1)
	for range 15 {
		fastest = min(fastest, timed(small))
	}
	// A quadratic parse takes minutes of the large size, so the tries end after twenty seconds.
	ends, slowest := time.Now().Add(20*time.Second), time.Duration(0)
	best := time.Duration(1<<63 - 1)
	for tries := 0; tries < 15 && time.Now().Before(ends); tries++ {
		took := timed(large)
		best, slowest = min(best, took), max(slowest, took)
		if best < bound*fastest {
			return
		}
	}
	t.Errorf("%d KiB of open brackets took at best %v, and %d KiB took at best %v: that is %.0f times as long, where a linear parse takes about %d and a quadratic about %d",
		large>>10, best, small>>10, fastest, float64(best)/float64(fastest), large/small, (large/small)*(large/small))
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
