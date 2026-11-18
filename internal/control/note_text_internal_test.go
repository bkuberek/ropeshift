package control

import (
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// hostile is text a model could write to change what a pull request shows: a mention, a link, markup, the marker
// a comment is deduplicated by, a fence, and a line that starts a list item or a heading.
const hostile = "@user [x](y) <br><h3>big</h3> <!-- ropeshift:comment:rv-9 -->\n- a list item\n# a heading\n```\nnot a fence\n````"

// A fence is the only structure the text of a note can carry, and every line of it is indented in the item
// that holds it, so nothing in the text starts a list item, a heading, or a comment of its own.
func structureOf(t *testing.T, section string) (items, fences int) {
	t.Helper()
	inside := false
	var fence string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case inside && trimmed == fence:
			inside, fences = false, fences+1
		case inside:
			if !strings.HasPrefix(line, "  ") {
				t.Errorf("a line of the text leaves its item: %q", line)
			}
		case strings.HasPrefix(line, "- `"):
			items++
		case strings.HasPrefix(trimmed, "```"):
			inside, fence, fences = true, strings.TrimSuffix(trimmed, "text"), fences+1
		case trimmed != "" && !strings.HasPrefix(line, "**Security notes**") && !strings.Contains(line, "more note"):
			t.Errorf("a line outside any item or fence: %q", line)
		}
	}
	if inside {
		t.Errorf("a fence is left open:\n%s", section)
	}
	return items, fences
}

func TestANoteIsSetAsLiteralTextInAFence(t *testing.T) {
	notes := []workorders.ReviewNote{
		{Point: workorders.PointSecrets, Reasons: hostile, Citations: []string{"main.go:1", "@user", "[x](y)"}},
		{Point: workorders.PointInjection, Reasons: "plain"},
	}
	section := notesSection(notes, 1<<20)
	items, fences := structureOf(t, section)
	if items != 2 || fences != 4 {
		t.Errorf("the section holds %d items and %d fence lines, want 2 and 4:\n%s", items, fences, section)
	}
	if strings.Contains(section, "<!-- ropeshift") {
		t.Errorf("the section holds a marker a comment is deduplicated by:\n%s", section)
	}
	if !strings.Contains(section, "  @user [x](y) <br><h3>big</h3>") || !strings.Contains(section, "  # a heading") {
		t.Errorf("the text is not kept as it was written:\n%s", section)
	}
	// The text holds a run of four backticks, so its fence is of five.
	if !strings.Contains(section, "  `````text\n") {
		t.Errorf("the fence is no longer than the longest run of backticks in the text:\n%s", section)
	}
}

func TestATextCannotLeaveItsFenceThroughABacktickAndANewline(t *testing.T) {
	// A citation of this kind is refused when a review reports it. Text that reached the comment another way is
	// still set as literal text.
	note := workorders.ReviewNote{Point: workorders.PointAuthority, Reasons: "x", Citations: []string{"a`\n```\n- injected\n```"}}
	section := notesSection([]workorders.ReviewNote{note}, 1<<20)
	items, _ := structureOf(t, section)
	if items != 1 {
		t.Errorf("the text started an item of its own:\n%s", section)
	}
}

func TestAVerdictLineSetsItsWordsAsLiteralText(t *testing.T) {
	v := workorders.CriterionVerdict{Criterion: "c`1\n- x", Verdict: workorders.VerdictFail, Reasons: hostile, Citations: []string{"@user", "x`\n# y"}}
	item := verdictItem(v, "<img src=x onerror=alert(1)> @team")
	items, _ := structureOf(t, item)
	if items != 1 || strings.Contains(item, "<!-- ropeshift") {
		t.Errorf("the verdict changed the comment's structure:\n%s", item)
	}
	if strings.Count(strings.SplitN(item, "\n", 2)[0], "`") != 2 {
		t.Errorf("the criterion left its code span: %q", strings.SplitN(item, "\n", 2)[0])
	}
}

func TestTheSecurityNotesAreClippedToTheirBudget(t *testing.T) {
	notes := make([]workorders.ReviewNote, workorders.MaxNotes)
	for i := range notes {
		notes[i] = workorders.ReviewNote{Point: workorders.PointSecrets, Reasons: strings.Repeat("r", workorders.MaxNoteReasons), Citations: []string{fmt.Sprint("c", i)}}
	}
	section := notesSection(notes, noteBudget(500, 300))
	if len(section) > noteSectionBudget {
		t.Errorf("the section is %d bytes, and its budget is %d", len(section), noteSectionBudget)
	}
	items, _ := structureOf(t, section)
	if items == 0 || items >= len(notes) {
		t.Fatalf("the section holds %d of %d notes", items, len(notes))
	}
	if want := fmt.Sprintf("%d more notes in ropeshift\n", len(notes)-items); !strings.HasSuffix(section, want) {
		t.Errorf("the section ends %q, want it to end %q", section[max(0, len(section)-60):], want)
	}
	whole := notesSection(notes[:1], noteBudget(500, 300))
	if strings.Contains(whole, "more note") {
		t.Errorf("a section that fits is clipped:\n%s", whole)
	}
	if one := notesSection(notes[:2], len("\n**Security notes**\n\n")+4300+64); !strings.HasSuffix(one, "1 more note in ropeshift\n") {
		t.Errorf("one note left out ends %q", one[max(0, len(one)-40):])
	}
}

// A comment is never more than GitHub takes: the head and the tail of a comment count against the budget of its notes.
func TestTheBudgetOfTheNotesKeepsTheWholeCommentUnderItsBound(t *testing.T) {
	if got := noteBudget(0, 0); got != noteSectionBudget {
		t.Errorf("a comment of nothing else has %d bytes for its notes, want %d", got, noteSectionBudget)
	}
	for _, other := range []int{10_000, 45_000, 58_000, 70_000} {
		budget := noteBudget(other, 200)
		if budget < 0 || budget > noteSectionBudget || (budget > 0 && other+200+budget > maxCommentBytes-commentReserve) {
			t.Errorf("a comment of %d other bytes gives its notes %d", other, budget)
		}
	}
}

func TestRedactedNotesAreCleanedAndHeldToTheirBounds(t *testing.T) {
	token := "ghp_" + strings.Repeat("a1", 12)
	long := strings.Repeat("é", workorders.MaxNoteReasons)
	got := redactedNotes([]workorders.ReviewNote{
		{Point: workorders.PointSecrets, Reasons: "key " + token, Citations: []string{"k " + token}},
		{Point: workorders.PointSecrets, Reasons: long},
		{Point: workorders.PointSecrets, Reasons: strings.Repeat("a", workorders.MaxNoteReasons-1) + token},
	})
	if got[0].Reasons != "key [redacted: github_token]" || got[0].Citations[0] != "k [redacted: github_token]" {
		t.Errorf("redacted to %+v", got[0])
	}
	for i, n := range got[1:] {
		if len(n.Reasons) > workorders.MaxNoteReasons || strings.Contains(n.Reasons, "ghp_") {
			t.Errorf("note %d keeps %d bytes", i+1, len(n.Reasons))
		}
		if err := n.Validate(); err != nil {
			t.Errorf("note %d after redaction: %v", i+1, err)
		}
	}
	if ledger.CIRedactionRulesVersion < 1 {
		t.Errorf("the rule set has version %d", ledger.CIRedactionRulesVersion)
	}
}
