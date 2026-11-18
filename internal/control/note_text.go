package control

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	// maxCommentBytes is what a comment on a pull request may hold, which GitHub bounds at 65,536 characters.
	maxCommentBytes = 60_000
	// commentReserve is what the worker adds to a comment's body: the line a comment is deduplicated by.
	commentReserve = 1_000
	// noteSectionBudget is the most a comment gives the security notes of a review, in bytes.
	noteSectionBudget = 20_000
)

// redactedNotes copies a report's notes with the shapes of credentials taken out of their reasons and citations, and
// each held to its bound afterwards, because a marker can be longer than what it replaces. A note is model text that
// reaches the ledger, its events, and a pull request, so it is redacted before any of them keeps it.
func redactedNotes(notes []workorders.ReviewNote) []workorders.ReviewNote {
	if len(notes) == 0 {
		return nil
	}
	out := make([]workorders.ReviewNote, len(notes))
	for i, n := range notes {
		out[i] = workorders.ReviewNote{Point: n.Point, Reasons: clipText(ledger.RedactCredentials(n.Reasons), workorders.MaxNoteReasons)}
		for _, citation := range n.Citations {
			out[i].Citations = append(out[i].Citations, clipText(ledger.RedactCredentials(citation), workorders.MaxNoteCitation))
		}
	}
	return out
}

// clipText keeps at most n bytes of text, never cutting a character in two.
func clipText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// cleanText makes model text safe to set in a comment: line breaks are newlines, any other control character or
// byte that is no text is a question mark, and the opening of an HTML comment is broken, so that text cannot
// carry the marker a comment is deduplicated by.
func cleanText(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", " ", "\n", " ", "\n").Replace(text)
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case r == utf8.RuneError || r < ' ' || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return '?'
		}
		return r
	}, text)
	return strings.ReplaceAll(text, "<!--", "<!"+string(rune(0x200b))+"--")
}

// codeSpan sets a word of one line in a code span that it cannot leave.
func codeSpan(text string) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(cleanText(text), "`", "'")), " ")
	return "`" + text + "`"
}

// literal sets text in a fenced block of an item's own, in from the margin by the item's indent, so that a
// pull request shows it as it was written: a mention, a link, markup, a heading, or a marker in it is only
// text. The fence is longer than any run of backticks in the text, so the text cannot close it.
func literal(text, indent string) string {
	text = strings.TrimRight(cleanText(text), " \n")
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	var out strings.Builder
	out.WriteString(indent + fence + "text\n")
	for line := range strings.SplitSeq(text, "\n") {
		out.WriteString(strings.TrimRight(indent+line, " ") + "\n")
	}
	out.WriteString(indent + fence + "\n")
	return out.String()
}

// itemText sets the lines of an item as its literal text, then its citations on one line.
func itemText(lines []string, citations []string) string {
	if len(citations) > 0 {
		lines = append(lines, "Cites: "+strings.Join(citations, ", "))
	}
	return literal(strings.Join(lines, "\n"), "  ")
}

// verdictItem writes one verdict as an item of the comment: the criterion and its verdict, then, as literal text,
// what the contract says of the criterion, the review's reasons, and what it cites.
func verdictItem(v workorders.CriterionVerdict, criterion string) string {
	var lines []string
	if criterion = strings.TrimSpace(criterion); criterion != "" {
		lines = append(lines, "Criterion: "+strings.Join(strings.Fields(criterion), " "))
	}
	lines = append(lines, v.Reasons)
	return fmt.Sprintf("- %s **%s**\n", codeSpan(v.Criterion), v.Verdict) + itemText(lines, v.Citations)
}

// noteItem writes one security note as an item of the comment: its point, then its reasons and citations as
// literal text.
func noteItem(n workorders.ReviewNote) string {
	return fmt.Sprintf("- %s\n", codeSpan(string(n.Point))) + itemText([]string{n.Reasons}, n.Citations)
}

// moreNotes ends a section that was clipped, and says how many notes it leaves out.
func moreNotes(left int) string {
	if left == 1 {
		return "1 more note in ropeshift\n"
	}
	return fmt.Sprintf("%d more notes in ropeshift\n", left)
}

// noteBudget is what the security notes may take of a comment whose verdicts, and whose words after the notes, take
// head and tail bytes: the room that keeps the whole comment under its bound, and never more than the notes' own
// budget.
func noteBudget(head, tail int) int {
	return max(0, min(noteSectionBudget, maxCommentBytes-commentReserve-head-tail))
}

// notesSection writes a review's security notes after its verdicts, each as literal text, within budget bytes. A
// section that does not fit ends with how many notes it leaves for ropeshift to show.
func notesSection(notes []workorders.ReviewNote, budget int) string {
	if len(notes) == 0 {
		return ""
	}
	const title = "\n**Security notes**\n\n"
	last := moreNotes(len(notes))
	out := title
	for i, n := range notes {
		item := noteItem(n)
		reserve := 0
		if i < len(notes)-1 {
			reserve = len(moreNotes(len(notes) - i - 1))
		}
		if len(out)+len(item)+reserve > budget {
			if i == 0 && len(title)+len(last) > budget {
				return "\n" + moreNotes(len(notes))
			}
			return out + moreNotes(len(notes)-i)
		}
		out += item
	}
	return out
}
