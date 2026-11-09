package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// EventsPath is the ledger's event stream.
const EventsPath = "/v1/events"

// StreamEvent is one ledger event as a reader sees it: what happened, to what, and when. It carries a
// record's name and none of its content, so a surface reads the record through a view.
type StreamEvent struct {
	Rev         int64  `json:"rev"`
	Type        string `json:"type"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	ActorKind   string `json:"actor_kind"`
	ActorID     string `json:"actor_id"`
	OccurredAt  int64  `json:"occurred_at,omitempty"`
	RecordedAt  int64  `json:"recorded_at"`
}

// StreamEventOf is what the stream says of one event.
func StreamEventOf(event ledger.Event) StreamEvent {
	return StreamEvent{
		Rev: event.Rev, Type: event.Type, SubjectKind: event.Subject.Kind, SubjectID: event.Subject.ID,
		ActorKind: string(event.Actor.Kind), ActorID: event.Actor.ID, OccurredAt: event.OccurredAt, RecordedAt: event.RecordedAt,
	}
}

// Events follows the ledger from a revision: every event after it, in order, then each new one. The
// stream is one long read, so the channel closes when it breaks, and the last revision a reader saw
// resumes it with nothing missed. A slow reader holds the stream back and loses nothing.
func (c *Client) Events(ctx context.Context, after int64) (<-chan StreamEvent, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+EventsPath, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	if c.session != "" {
		request.Header.Set(HeaderSession, c.session)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer response.Body.Close()
		return nil, readError(response)
	}
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		defer response.Body.Close()
		lines := bufio.NewScanner(response.Body)
		for lines.Scan() {
			data, ok := strings.CutPrefix(lines.Text(), "data: ")
			if !ok {
				continue
			}
			var event StreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}
