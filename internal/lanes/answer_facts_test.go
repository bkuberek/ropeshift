package lanes_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
)

// An answer to the lane's own question reaches a new generation as a ledger fact that is in force: it decides the
// question, and it grants no permission. The line that says a message is no consent does not stand over it, because a
// lane read that line as the answer being assumed and refused it.
func TestAnAnswerToTheLanesOwnQuestionIsStatedAsInForce(t *testing.T) {
	prompts := map[string]string{
		"a native resume":     lanes.ResumePrompt("", answerFact),
		"a fresh generation":  lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T", Facts: []ledger.MessageFact{answerFact}}),
		"a restarted session": lanes.RestartPrompt(answerFact),
	}
	for name, prompt := range prompts {
		if !strings.Contains(prompt, messages.AnswerInForce) {
			t.Errorf("%s does not say the answer is in force:\n%s", name, prompt)
		}
		for _, want := range []string{"in force", "decides", "grants no permission"} {
			if !strings.Contains(messages.AnswerInForce, want) {
				t.Errorf("the line %q does not say %q", messages.AnswerInForce, want)
			}
		}
		if strings.Contains(prompt, messages.NotConsent) {
			t.Errorf("%s states the answer is no consent:\n%s", name, prompt)
		}
	}
}

// Any other fact keeps the line that says a message is not a permission decision and not a person's consent, and a
// prompt that holds both an answer and another fact says each line once, under the facts it is about.
func TestEveryOtherFactKeepsTheNotConsentLine(t *testing.T) {
	prompts := map[string]string{
		"a native resume":     lanes.ResumePrompt("", openFact),
		"a fresh generation":  lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T", Facts: []ledger.MessageFact{openFact}}),
		"a restarted session": lanes.RestartPrompt(openFact),
	}
	for name, prompt := range prompts {
		if !strings.Contains(prompt, messages.NotConsent) || strings.Contains(prompt, messages.AnswerInForce) {
			t.Errorf("%s:\n%s", name, prompt)
		}
	}
	mixed := lanes.RestartPrompt(answerFact, openFact)
	for _, line := range []string{messages.NotConsent, messages.AnswerInForce} {
		if strings.Count(mixed, line) != 1 {
			t.Errorf("the prompt holds %q %d times:\n%s", line, strings.Count(mixed, line), mixed)
		}
	}
}
