package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/messages"
)

// messageLine says how far a message got, and why, which is all a sender is told.
func messageLine(m messages.Message) string {
	line := fmt.Sprintf("%s  %s  to %s generation %d  %s", m.ID, m.Kind, m.To.Lane, m.To.Generation, m.State)
	if m.Reason != "" {
		line += " (" + m.Reason + ")"
	}
	if m.Reply != "" {
		line += "  reply: " + m.Reply
	}
	return line + "\n"
}

// messageSend sends a lane a message. A retry with the same --id sends it once.
func (c *invocation) messageSend(args []string) int {
	set := c.flags()
	var draft messages.Draft
	kind := string(messages.KindInstruction)
	set.StringVar(&draft.ID, "id", "", "a client-generated ID")
	set.StringVar(&kind, "kind", kind, "instruction, notice, answer, or decision_notice")
	set.StringVar(&draft.Body, "body", "", "what the message says")
	set.StringVar(&draft.ReplyTo, "reply-to", "", "what this answers")
	set.Int64Var(&draft.To.Generation, "generation", 0, "the generation it is for, the current one when left out")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one lane")
	}
	if draft.Body == "" {
		return c.usageError("--body is required")
	}
	if draft.ID == "" {
		id, err := newID()
		if err != nil {
			return c.failure(err)
		}
		draft.ID = id
	}
	draft.To.Lane, draft.Kind = positional[0], messages.Kind(kind)
	client := c.client()
	defer client.Close()
	sent, err := client.SendMessage(c.ctx, draft)
	return c.finish(err, sent, func() string { return messageLine(sent) })
}

// messageShow says what became of a message.
func (c *invocation) messageShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	message, err := client.Message(c.ctx, id)
	return c.finish(err, message, func() string { return messageLine(message) })
}

// messageList says what was sent to a lane.
func (c *invocation) messageList(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	rows, err := client.LaneMessages(c.ctx, id)
	return c.finish(err, rows, func() string {
		var out strings.Builder
		for _, row := range rows {
			out.WriteString(messageLine(row))
		}
		return out.String()
	})
}
