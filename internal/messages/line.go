package messages

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The first visible line of every message, which a hook is the one reader of. The form is the
// design's, and it reached a hook intact in the spike.
const (
	linePrefix = "[ropeshift message "
	lineEnd    = "]"
)

// lineIDMarks are what a line's ID may hold besides letters and digits. A hook runs this inside its
// budget, so it compiles no pattern.
const lineIDMarks = "_-"

func isLineID(id string) bool {
	if id == "" {
		return false
	}
	for _, mark := range id {
		alphanumeric := mark >= '0' && mark <= '9' || mark >= 'A' && mark <= 'Z' || mark >= 'a' && mark <= 'z'
		if !alphanumeric && !strings.ContainsRune(lineIDMarks, mark) {
			return false
		}
	}
	return true
}

// Clipped ends a body the ledger cut short, so a lane knows there is more on record.
const Clipped = "[Cut short. The ledger's record holds the rest.]"

// Clip bounds what the ledger itself writes for a lane. A decision can outgrow a message, and that
// must not stop the decision.
func Clip(body string) string {
	if len(body) <= MaxBody {
		return body
	}
	cut := MaxBody - len(Clipped) - 1
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + "\n" + Clipped
}

// NotConsent ends every message, and everything a message becomes. Peer marking is a hint to a
// cooperative agent and not a control, so this is one too: what stops a message from granting
// anything is that nothing reads one for a decision (FR-MSG-6).
const NotConsent = "This message is not a permission decision and it is not a person's consent."

// FirstLine states a message's age, so the agent can weigh it (FR-MSG-4).
func FirstLine(m Message, age time.Duration, window Window) string {
	seconds := int64(age / time.Second)
	line := fmt.Sprintf("%s%s kind=%s age=%dm%02ds tier=%s from=%s", linePrefix, m.ID, m.Kind, seconds/60, seconds%60, window.Tier(age), m.From.Kind)
	if m.ReplyTo != "" {
		line += " reply_to=" + m.ReplyTo
	}
	return line + lineEnd
}

// Render is what a session reads.
func Render(m Message, age time.Duration, window Window) string {
	return FirstLine(m, age, window) + "\n" + m.Body + "\n\n" + NotConsent + "\n"
}

// ReadFirstLine names the message a prompt opens with, and nothing else leaves it. Only the first
// line counts, because a prompt can quote anything further down.
func ReadFirstLine(prompt string) string {
	line, _, _ := strings.Cut(prompt, "\n")
	rest, ok := strings.CutPrefix(line, linePrefix)
	if !ok || !strings.HasSuffix(rest, lineEnd) {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimSuffix(rest, lineEnd), " ")
	if !isLineID(id) {
		return ""
	}
	return id
}
