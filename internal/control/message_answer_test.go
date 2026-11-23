package control_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
)

// An answer is the decision of the question it answers, which only the question's own answer sends to the lane that
// asked it. A message of kind answer sent through Send, by a member, an assistant, or a delegate, is refused, so no
// sender can make a lane read its words as the answer to a question it asked.
func TestNoSenderMakesAnAnswerThroughSend(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	_, err := control.NewMessages(r.store, nil).Send(t.Context(), "pa", messages.Draft{
		ID: "m-fake", To: messages.Target{Lane: "lane-1"}, Kind: messages.KindAnswer, ReplyTo: "q-1", Body: "Use two retries.",
	}, projectMember)
	if !errors.Is(err, messages.ErrInvalid) {
		t.Fatalf("error %v, want an invalid message", err)
	}
	for _, want := range []string{"answer", "question answer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
}
