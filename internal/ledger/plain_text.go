package ledger

import (
	"regexp"
	"strings"
)

// ansiSequence matches the terminal escape sequences a tool's output can carry: a CSI sequence, an OSC string ended by
// BEL or ST, and any other two-character escape. The C1 forms of CSI and OSC are matched as well.
var ansiSequence = regexp.MustCompile(`(?:\x1b\[|\x{9b})[0-?]*[ -/]*[@-~]|(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}]*(?:\x07|\x1b\\|\x{9c})?|\x1b[ -~]`)

// PlainText makes text a tool printed safe to show: it drops escape sequences and every character HiddenRune names,
// which is every control character, the C0 set, DEL, and the C1 set, and every bidirectional override and zero-width
// character, but keeps newline and tab. What git and `gh` print is untrusted, so every place that shows a step's detail
// or a commit's error shows it through this.
func PlainText(text string) string {
	text = ansiSequence.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if HiddenRune(r) {
			return -1
		}
		return r
	}, text)
}
