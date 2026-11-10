package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// paneEvery is how often an open pane stream reads the screen: five times a second, which keeps a
// person's typing readable. Only a stream reads, so a pane nobody watches is never read.
const paneEvery = 200 * time.Millisecond

// Panes is the worker's terminal as the web pane reaches it (ADR-0018), by the lane and the generation
// that own a pane. In solo mode it is the worker's own terminal server, in this process; a control
// plane that reaches its worker over the network puts the worker's route behind the same two calls.
type Panes interface {
	// Screen returns the visible screen of a generation's pane, with the escape sequences that set its
	// colours and emphasis. ErrNotFound means there is no such pane.
	Screen(ctx context.Context, lane string, generation int64) (string, error)
	// Send types into a generation's pane. ErrNotFound means there is no such pane.
	Send(ctx context.Context, lane string, generation int64, input PaneInput) error
}

// PaneInput is what reaches a pane: literal text, then keys by the terminal's names for them. Either
// one may be empty.
type PaneInput struct {
	Text string
	Keys []string
}

// paneKeys names each key a member may press as the terminal names it. A key it does not hold is
// refused, so no key name a page sends reaches the terminal as it was sent.
var paneKeys = map[string]string{
	"Enter": "Enter", "Escape": "Escape", "Tab": "Tab", "Backspace": "BSpace",
	"Up": "Up", "Down": "Down", "Left": "Left", "Right": "Right", "Ctrl-C": "C-c", "Ctrl-D": "C-d",
}

// The pane's routes take only a member's session, so neither a lane nor an orchestrator can read a
// screen or type into one.
func (h *handler) paneRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+api.PanePath, h.member(h.streamPane))
	mux.HandleFunc("POST "+api.PaneInputPath, h.member(h.typeIntoPane))
}

// paneLane reads the lane a request names. It refuses a lane that ended, which will never have a pane
// again, and a control plane that reaches no worker's terminal, and returns false once it refused.
func (h *handler) paneLane(w http.ResponseWriter, r *http.Request) (ledger.LaneView, bool) {
	lane, err := h.workers.Lane(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return lane, false
	}
	if lane.ActualState == ledger.StateEnded {
		refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeLaneEnded, Message: fmt.Sprintf("lane %s has ended", lane.ID), State: lane.ActualState})
		return lane, false
	}
	if h.Panes == nil {
		refuse(w, http.StatusConflict, api.ErrorDetail{
			Code: api.CodeNoPane, Message: "this control plane reaches no worker's terminal, so it holds no pane to show", State: lane.ActualState,
		})
		return lane, false
	}
	return lane, true
}

// noPane refuses a lane that has no pane now, and says why in the lane's own terms.
func noPane(w http.ResponseWriter, lane ledger.LaneView) {
	message := fmt.Sprintf("lane %s has no pane for generation %d (state %s): it runs without a terminal, its pane has not opened yet, "+
		"or another worker holds it", lane.ID, lane.Generation, lane.ActualState)
	if lane.ActualState == ledger.StateParked {
		message = fmt.Sprintf("lane %s is parked, so it has no process until it resumes", lane.ID)
	}
	refuse(w, http.StatusConflict, api.ErrorDetail{Code: api.CodeNoPane, Message: message, State: lane.ActualState})
}

// streamPane shows a lane's pane for as long as a member watches: a snapshot each time the screen
// changes, and the lane's state while it has no screen to show. Each read asks where the lane is now,
// so a park, a resume, or a restart moves the stream to the next generation's pane with it.
func (h *handler) streamPane(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	lane, ok := h.paneLane(w, r)
	if !ok {
		return
	}
	first, held := "", false
	if lane.ActualState != ledger.StateParked {
		screen, err := h.Panes.Screen(r.Context(), lane.ID, lane.Generation)
		switch {
		case errors.Is(err, ports.ErrNotFound):
			noPane(w, lane)
			return
		case err != nil:
			fail(w, err)
			return
		}
		first, held = screen, true
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		refuse(w, http.StatusInternalServerError, api.ErrorDetail{Code: api.CodeUnavailable, Message: "streaming is not available"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	watch := &paneWatch{w: w, flusher: flusher}
	if held {
		ok = watch.show(api.PaneEventScreen, api.PaneScreen{Lane: lane.ID, Generation: lane.Generation, Screen: first})
	} else {
		ok = watch.show(api.PaneEventLane, paneLaneOf(lane))
	}
	if !ok {
		return
	}
	reading := time.NewTicker(paneEvery)
	defer reading.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-reading.C:
		}
		now, err := h.workers.Lane(r.Context(), lane.ID)
		if err != nil {
			return
		}
		if !watch.follow(r.Context(), h.Panes, now) || now.ActualState == ledger.StateEnded || !watch.keepAlive() {
			return
		}
	}
}

func paneLaneOf(lane ledger.LaneView) api.PaneLane {
	return api.PaneLane{Lane: lane.ID, Generation: lane.Generation, State: lane.ActualState}
}

// paneWatch is one member's stream of a pane. It remembers a digest of what it sent last, so it sends
// only a change.
type paneWatch struct {
	w       http.ResponseWriter
	flusher http.Flusher
	last    [sha256.Size]byte
	spoke   time.Time
}

// follow sends what the lane shows now: its screen, or its state when it has no pane. A read that did
// not finish sends nothing, because the next one may. It returns false once the watcher is gone.
func (p *paneWatch) follow(ctx context.Context, panes Panes, lane ledger.LaneView) bool {
	if lane.ActualState == ledger.StateEnded || lane.ActualState == ledger.StateParked {
		return p.show(api.PaneEventLane, paneLaneOf(lane))
	}
	screen, err := panes.Screen(ctx, lane.ID, lane.Generation)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return p.show(api.PaneEventLane, paneLaneOf(lane))
	case err != nil:
		return true
	}
	return p.show(api.PaneEventScreen, api.PaneScreen{Lane: lane.ID, Generation: lane.Generation, Screen: screen})
}

// show sends one event, unless it is the one the watcher already has.
func (p *paneWatch) show(event string, body any) bool {
	raw, err := json.Marshal(body)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(append([]byte(event+"\n"), raw...))
	if digest == p.last {
		return true
	}
	if _, err := fmt.Fprintf(p.w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
		return false
	}
	p.flusher.Flush()
	p.last, p.spoke = digest, time.Now()
	return true
}

// keepAlive speaks on a quiet stream, so a watcher that left is found.
func (p *paneWatch) keepAlive() bool {
	if time.Since(p.spoke) < streamComment {
		return true
	}
	if _, err := fmt.Fprint(p.w, ": still here\n\n"); err != nil {
		return false
	}
	p.flusher.Flush()
	p.spoke = time.Now()
	return true
}

// typeIntoPane types what a member sent into the lane's current pane.
func (h *handler) typeIntoPane(w http.ResponseWriter, r *http.Request, _ ledger.Actor) {
	var in api.PaneInput
	if !decode(w, r, &in) {
		return
	}
	input, err := paneInputOf(in)
	if err != nil {
		invalid(w, "%s", err.Error())
		return
	}
	lane, ok := h.paneLane(w, r)
	if !ok {
		return
	}
	if lane.ActualState == ledger.StateParked {
		noPane(w, lane)
		return
	}
	err = h.Panes.Send(r.Context(), lane.ID, lane.Generation, input)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		noPane(w, lane)
		return
	case err != nil:
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// paneInputOf checks what a member typed, and names its key as the terminal does. Text is typed
// literally, so a control character in it would act as a key that is not on the list.
func paneInputOf(in api.PaneInput) (PaneInput, error) {
	switch {
	case in.Key != "" && (in.Text != "" || in.Enter):
		return PaneInput{}, errors.New("an input types text or presses one key, not both")
	case in.Key != "":
		key, ok := paneKeys[in.Key]
		if !ok {
			return PaneInput{}, fmt.Errorf("no key is named %q: the keys are %s", in.Key, strings.Join(api.PaneKeys, ", "))
		}
		return PaneInput{Keys: []string{key}}, nil
	case in.Text == "":
		return PaneInput{}, errors.New("an input types text or presses one key")
	case len(in.Text) > api.MaxPaneText:
		return PaneInput{}, fmt.Errorf("the text is longer than %d bytes", api.MaxPaneText)
	case strings.ContainsFunc(in.Text, unicode.IsControl):
		return PaneInput{}, errors.New("the text holds a control character, which would act as a key: press the key instead")
	}
	input := PaneInput{Text: in.Text}
	if in.Enter {
		input.Keys = []string{paneKeys["Enter"]}
	}
	return input, nil
}
