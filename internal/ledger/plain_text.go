package ledger

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiSequence matches the terminal escape sequences a stranger's text can carry: a CSI sequence, an OSC string ended
// by BEL or ST, and any other escape, which is an escape character, any intermediate characters, and a final one. The
// C1 forms of CSI and OSC are matched as well. An OSC string is bounded at a newline, a line separator, and a paragraph
// separator, so one that is never terminated takes nothing of the lines after it.
var ansiSequence = regexp.MustCompile(`(?:\x1b\[|\x{9b})[0-?]*[ -/]*[@-~]|(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}\n\x{2028}\x{2029}]*(?:\x07|\x1b\\|\x{9c})?|\x1b[ -/]*[0-~]`)

// PlainText makes text a stranger wrote safe to show or to put before an agent: it is valid UTF-8, it drops escape
// sequences and every character HiddenRune names, which is every control character, the C0 set, DEL, and the C1 set,
// and every bidirectional override and zero-width character, and it keeps newline and tab. A line separator, U+2028,
// and a paragraph separator, U+2029, become a newline, so the words around one stay apart. What git, `gh`, a CI log, or
// a repository prints is untrusted, so every place that shows it shows it through this.
func PlainText(text string) string {
	text = strings.ToValidUTF8(text, string(utf8.RuneError))
	text = ansiSequence.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\u2028' || r == '\u2029':
			return '\n'
		case HiddenRune(r):
			return -1
		}
		return r
	}, text)
}
