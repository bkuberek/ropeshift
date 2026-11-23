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

// NotConsent ends every message, and everything a message becomes, except an answer to a question the receiving lane
// asked (AnswerInForce). Peer marking is a hint to a cooperative agent and not a control, so this is one too: what
// stops a message from granting anything is that nothing reads one for a decision (FR-MSG-6).
const NotConsent = "This message is not a permission decision and it is not a person's consent."

// AnswerInForce ends a message that holds an answer to a question the receiving lane asked, and the facts of a prompt
// that hold one, in place of NotConsent for that answer. The answer is in force and decides its question, and it
// grants no permission.
const AnswerInForce = "An answer to a question this lane asked is in force and decides that question. It grants no permission."

// IsOwnAnswer says a message of a kind and a reply_to is the answer to a question the lane it is sent to asked: its
// kind is answer and it names the question. Only the answer to a question makes a message of that kind, and the control
// plane sends it to the lanes that asked, so no lane is sent an answer to a question another asked.
func IsOwnAnswer(kind Kind, replyTo string) bool {
	return kind == KindAnswer && replyTo != ""
}

// FirstLine states a message's age, so the agent can weigh it (FR-MSG-4).
func FirstLine(m Message, age time.Duration, window Window) string {
	seconds := int64(age / time.Second)
	line := fmt.Sprintf("%s%s kind=%s age=%dm%02ds tier=%s from=%s", linePrefix, m.ID, m.Kind, seconds/60, seconds%60, window.Tier(age), m.From.Kind)
	if m.ReplyTo != "" {
		line += " reply_to=" + m.ReplyTo
	}
	return line + lineEnd
}

// Render is what a session reads: the first line, the body, and the line that says what the message is not, or, for
// the answer to a question the lane asked, that it is in force.
func Render(m Message, age time.Duration, window Window) string {
	ending := NotConsent
	if IsOwnAnswer(m.Kind, m.ReplyTo) {
		ending = AnswerInForce
	}
	return FirstLine(m, age, window) + "\n" + m.Body + "\n\n" + ending + "\n"
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
