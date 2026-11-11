package api

import (
	"context"
	"net/http"

	"ropeshift.com/ropeshift/internal/messages"
)

// MessageList is what was sent to a lane, oldest first.
type MessageList struct {
	Messages []messages.Message `json:"messages"`
}

// SendMessage records a message for a lane.
func (c *ProjectClient) SendMessage(ctx context.Context, in messages.Draft) (messages.Message, error) {
	var out messages.Message
	err := c.do(ctx, http.MethodPost, "/v1/messages", in, &out)
	return out, err
}

// Message reads one message, which says what became of it.
func (c *ProjectClient) Message(ctx context.Context, id string) (messages.Message, error) {
	var out messages.Message
	err := c.do(ctx, http.MethodGet, "/v1/messages/"+id, nil, &out)
	return out, err
}

// LaneMessages reads what was sent to a lane.
func (c *ProjectClient) LaneMessages(ctx context.Context, lane string) ([]messages.Message, error) {
	var out MessageList
	err := c.do(ctx, http.MethodGet, "/v1/lanes/"+lane+"/messages", nil, &out)
	return out.Messages, err
}
