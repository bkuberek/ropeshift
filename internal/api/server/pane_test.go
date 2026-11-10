package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// paneWait bounds how long a test waits on a pane's stream.
const paneWait = 10 * time.Second

// heldPanes stands for the worker's terminal: a screen for each generation's pane, and what each took.
type heldPanes struct {
	mu      sync.Mutex
	screens map[string]string
	sent    map[string][]server.PaneInput
	reads   int
}

func newHeldPanes() *heldPanes {
	return &heldPanes{screens: map[string]string{}, sent: map[string][]server.PaneInput{}}
}

func paneKey(lane string, generation int64) string { return fmt.Sprintf("%s-g%d", lane, generation) }

func (p *heldPanes) show(lane string, generation int64, screen string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.screens[paneKey(lane, generation)] = screen
}

func (p *heldPanes) close(lane string, generation int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.screens, paneKey(lane, generation))
}

func (p *heldPanes) readCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}

func (p *heldPanes) took(lane string, generation int64) []server.PaneInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.sent[paneKey(lane, generation)])
}

func (p *heldPanes) Screen(_ context.Context, lane string, generation int64) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	screen, ok := p.screens[paneKey(lane, generation)]
	if !ok {
		return "", fmt.Errorf("%s: %w", paneKey(lane, generation), ports.ErrNotFound)
	}
	return screen, nil
}

func (p *heldPanes) Send(_ context.Context, lane string, generation int64, input server.PaneInput) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := paneKey(lane, generation)
	if _, ok := p.screens[key]; !ok {
		return fmt.Errorf("%s: %w", key, ports.ErrNotFound)
	}
	p.sent[key] = append(p.sent[key], input)
	return nil
}

// paneRig is a daemon whose worker holds panes, with a running lane and the web listener.
type paneRig struct {
	*webRig
	panes *heldPanes
	world *apitest.World
	lane  *apitest.Lane
}

func paneOn(t *testing.T) *paneRig {
	t.Helper()
	panes := newHeldPanes()
	d := apitest.StartWith(t, apitest.Options{Panes: panes})
	world := apitest.NewWorld(t, d)
	lane := world.Running("wo-plan", "Plan the product")
	return &paneRig{webRig: webOn(t, d), panes: panes, world: world, lane: lane}
}

// paneEvent is one event of a pane's stream.
type paneEvent struct {
	name string
	data string
}

func (e paneEvent) screen(t *testing.T) api.PaneScreen {
	t.Helper()
	if e.name != api.PaneEventScreen {
		t.Fatalf("event %s %s, want a screen", e.name, e.data)
	}
	var screen api.PaneScreen
	if err := json.Unmarshal([]byte(e.data), &screen); err != nil {
		t.Fatal(err)
	}
	return screen
}

func (e paneEvent) lane(t *testing.T) api.PaneLane {
	t.Helper()
	if e.name != api.PaneEventLane {
		t.Fatalf("event %s %s, want the lane's state", e.name, e.data)
	}
	var lane api.PaneLane
	if err := json.Unmarshal([]byte(e.data), &lane); err != nil {
		t.Fatal(err)
	}
	return lane
}

// paneStream is one open stream of a pane, and how to end it.
type paneStream struct {
	events <-chan paneEvent
	stop   context.CancelFunc
	ended  <-chan struct{}
}

// watch opens a pane's stream as a browser does, with the session's cookies.
func (r *paneRig) watch(t *testing.T, cookies []*http.Cookie, lane string) *paneStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), paneWait)
	t.Cleanup(cancel)
	path := strings.ReplaceAll(api.PanePath, "{id}", lane)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.web.URL(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := r.web.HTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		defer response.Body.Close()
		t.Fatalf("status %d, type %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	events := make(chan paneEvent, 64)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		defer close(events)
		defer response.Body.Close()
		lines := bufio.NewScanner(response.Body)
		lines.Buffer(make([]byte, 1<<20), 1<<20)
		var event paneEvent
		for lines.Scan() {
			line := lines.Text()
			switch {
			case line == "":
				if event.name != "" {
					events <- event
				}
				event = paneEvent{}
			case strings.HasPrefix(line, "event: "):
				event.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				event.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return &paneStream{events: events, stop: cancel, ended: ended}
}

func (s *paneStream) next(t *testing.T) paneEvent {
	t.Helper()
	select {
	case event, ok := <-s.events:
		if !ok {
			t.Fatal("the stream ended")
		}
		return event
	case <-time.After(paneWait):
		t.Fatal("no event came")
	}
	return paneEvent{}
}

// quiet says the stream sent nothing while the pane was read at least `reads` more times.
func (s *paneStream) quiet(t *testing.T, panes *heldPanes, reads int) {
	t.Helper()
	until := panes.readCount() + reads
	deadline := time.Now().Add(paneWait)
	for panes.readCount() < until {
		s.silent(t, 20*time.Millisecond)
		if time.Now().After(deadline) {
			t.Fatal("the pane was not read again")
		}
	}
	s.silent(t, 0)
}

// silent says the stream sent nothing, and is still open, for a while.
func (s *paneStream) silent(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case event, ok := <-s.events:
		if !ok {
			t.Fatal("the stream ended")
		}
		t.Fatalf("the stream sent %s %s with nothing changed", event.name, event.data)
	case <-time.After(wait):
	}
}

func inputPath(lane string) string { return strings.ReplaceAll(api.PaneInputPath, "{id}", lane) }

// typeInto sends an input as the page does, with the session's cookies and its token.
func (r *paneRig) typeInto(t *testing.T, cookies []*http.Cookie, token, lane string, input any) (*http.Response, []byte) {
	t.Helper()
	return r.send(t, call{method: http.MethodPost, path: inputPath(lane), body: input, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
}

// A member who signed in sees the screen of the lane's current pane, with its colours, at once.
func TestAMemberSeesALanesPane(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "\x1b[1mPlanning\x1b[0m: what is the plan for?")
	cookies, _ := r.signedIn(t)
	stream := r.watch(t, cookies, r.lane.ID)
	screen := stream.next(t).screen(t)
	if screen.Lane != r.lane.ID || screen.Generation != 1 || screen.Screen != "\x1b[1mPlanning\x1b[0m: what is the plan for?" {
		t.Errorf("snapshot %+v", screen)
	}
}

// ATTACK: a page with no session, or a process that holds a worker's credential and no member's
// session, reads no screen and types nothing.
func TestThePaneRefusesAnyoneWithNoMembersSession(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	response, raw := r.send(t, call{path: strings.ReplaceAll(api.PanePath, "{id}", r.lane.ID)})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	response, raw = r.send(t, call{method: http.MethodPost, path: inputPath(r.lane.ID), body: api.PaneInput{Key: "Enter"}})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)

	socket := apitest.RawClient(r.d.Socket)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := strings.ReplaceAll(api.PanePath, "{id}", r.lane.ID)
		var body *strings.Reader
		if method == http.MethodPost {
			path, body = inputPath(r.lane.ID), strings.NewReader(`{"key":"Enter"}`)
		} else {
			body = strings.NewReader("")
		}
		request, err := http.NewRequestWithContext(t.Context(), method, "http://ropeshift"+path, body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+r.world.Credential())
		request.Header.Set("Content-Type", "application/json")
		answer, err := socket.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = answer.Body.Close()
		if answer.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with a worker's credential: status %d, want 401", method, path, answer.StatusCode)
		}
	}
	if reads := r.panes.readCount(); reads != 0 {
		t.Errorf("the screen was read %d times for nobody", reads)
	}
	if took := r.panes.took(r.lane.ID, 1); len(took) != 0 {
		t.Errorf("the pane took %v", took)
	}
}

// ATTACK: cross-site request forgery. A page that cannot read the token types nothing.
func TestTypingIntoAPaneNeedsTheToken(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, token := r.signedIn(t)
	response, raw := r.send(t, call{method: http.MethodPost, path: inputPath(r.lane.ID), body: api.PaneInput{Text: "rm -rf", Enter: true}, cookies: cookies})
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeCSRF)
	if took := r.panes.took(r.lane.ID, 1); len(took) != 0 {
		t.Fatalf("the pane took %v with no token", took)
	}
	response, raw = r.typeInto(t, cookies, token, r.lane.ID, api.PaneInput{Text: "A planning tool", Enter: true})
	wantStatus(t, response, raw, http.StatusNoContent, "")
}

// Text goes in literally, Enter after it when asked, and a named key as the terminal names it.
func TestThePaneTakesTextAndNamedKeys(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, token := r.signedIn(t)
	inputs := []api.PaneInput{
		{Text: "A planning tool", Enter: true},
		{Text: "half a line"},
		{Key: "Escape"}, {Key: "Backspace"}, {Key: "Up"}, {Key: "Ctrl-C"}, {Key: "Ctrl-D"}, {Key: "Enter"}, {Key: "Tab"},
	}
	for _, input := range inputs {
		response, raw := r.typeInto(t, cookies, token, r.lane.ID, input)
		wantStatus(t, response, raw, http.StatusNoContent, "")
	}
	want := []server.PaneInput{
		{Text: "A planning tool", Keys: []string{"Enter"}},
		{Text: "half a line"},
		{Keys: []string{"Escape"}}, {Keys: []string{"BSpace"}}, {Keys: []string{"Up"}}, {Keys: []string{"C-c"}}, {Keys: []string{"C-d"}},
		{Keys: []string{"Enter"}}, {Keys: []string{"Tab"}},
	}
	got := r.panes.took(r.lane.ID, 1)
	if !slices.EqualFunc(got, want, func(a, b server.PaneInput) bool { return a.Text == b.Text && slices.Equal(a.Keys, b.Keys) }) {
		t.Errorf("the pane took %+v, want %+v", got, want)
	}
}

// Every key the API names is one the pane takes, so the list a page reads and the keys the server
// accepts are the same list.
func TestThePaneTakesEveryKeyOnTheList(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, token := r.signedIn(t)
	for _, key := range api.PaneKeys {
		response, raw := r.typeInto(t, cookies, token, r.lane.ID, api.PaneInput{Key: key})
		wantStatus(t, response, raw, http.StatusNoContent, "")
	}
	if took := r.panes.took(r.lane.ID, 1); len(took) != len(api.PaneKeys) {
		t.Errorf("the pane took %d keys, want %d", len(took), len(api.PaneKeys))
	}
}

// ATTACK: the key list is closed, so a page cannot send a key name, a sequence, or an escape the list
// does not hold, and text cannot carry a control character that would act as a key.
func TestThePaneRefusesAnyKeyOffItsList(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, token := r.signedIn(t)
	for name, input := range map[string]any{
		"tmux's own name":       api.PaneInput{Key: "C-c"},
		"a key not on the list": api.PaneInput{Key: "F1"},
		"two keys":              api.PaneInput{Key: "Enter Enter"},
		"a sequence":            api.PaneInput{Key: "\x1b[A"},
		"nothing":               api.PaneInput{},
		"Enter alone":           api.PaneInput{Enter: true},
		"text and a key":        api.PaneInput{Text: "yes", Key: "Enter"},
		"an escape in text":     api.PaneInput{Text: "\x1b[2J"},
		"a newline in text":     api.PaneInput{Text: "one\ntwo"},
		"a tab in text":         api.PaneInput{Text: "one\ttwo"},
		"text past the limit":   api.PaneInput{Text: strings.Repeat("a", api.MaxPaneText+1)},
		"an unknown field":      map[string]string{"keys": "C-c"},
	} {
		t.Run(name, func(t *testing.T) {
			response, raw := r.typeInto(t, cookies, token, r.lane.ID, input)
			wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
		})
	}
	if took := r.panes.took(r.lane.ID, 1); len(took) != 0 {
		t.Errorf("the pane took %+v", took)
	}
}

// A lane with nothing to show is refused with a code that says why, and so is a control plane that
// reaches no worker's terminal.
func TestThePaneRefusesALaneWithNoPane(t *testing.T) {
	r := paneOn(t)
	cookies, token := r.signedIn(t)
	ended := r.world.Running("wo-ended", "A lane that ended")
	ended.End(workorders.OutcomeCompleted, "Done")
	parked := r.world.Running("wo-parked", "A lane that parked")
	parked.Park("after 1h")
	for _, tt := range []struct {
		name   string
		lane   string
		status int
		code   string
	}{
		{"a lane with no pane, as a lane that runs with no terminal has", r.lane.ID, http.StatusConflict, api.CodeNoPane},
		{"no such lane", "lane-none", http.StatusNotFound, api.CodeNotFound},
		{"an ended lane", ended.ID, http.StatusConflict, api.CodeLaneEnded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, raw := r.send(t, call{path: strings.ReplaceAll(api.PanePath, "{id}", tt.lane), cookies: cookies})
			wantStatus(t, response, raw, tt.status, tt.code)
			response, raw = r.typeInto(t, cookies, token, tt.lane, api.PaneInput{Key: "Enter"})
			wantStatus(t, response, raw, tt.status, tt.code)
		})
	}
	t.Run("a parked lane takes no keys", func(t *testing.T) {
		response, raw := r.typeInto(t, cookies, token, parked.ID, api.PaneInput{Key: "Enter"})
		wantStatus(t, response, raw, http.StatusConflict, api.CodeNoPane)
	})

	none := webOn(t, apitest.Start(t))
	noneWorld := apitest.NewWorld(t, none.d)
	running := noneWorld.Running("wo-plan", "Plan the product")
	noneCookies, noneToken := none.signedIn(t)
	response, raw := none.send(t, call{path: strings.ReplaceAll(api.PanePath, "{id}", running.ID), cookies: noneCookies})
	wantStatus(t, response, raw, http.StatusConflict, api.CodeNoPane)
	response, raw = none.send(t, call{method: http.MethodPost, path: inputPath(running.ID), body: api.PaneInput{Key: "Enter"}, cookies: noneCookies, headers: map[string]string{api.HeaderCSRF: noneToken}})
	wantStatus(t, response, raw, http.StatusConflict, api.CodeNoPane)
}

// The stream sends a snapshot when the screen changes and stays silent while it does not, however
// often it reads.
func TestThePaneSendsASnapshotOnlyWhenTheScreenChanges(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "first")
	cookies, _ := r.signedIn(t)
	stream := r.watch(t, cookies, r.lane.ID)
	if got := stream.next(t).screen(t).Screen; got != "first" {
		t.Fatalf("screen %q", got)
	}
	stream.quiet(t, r.panes, 3)
	r.panes.show(r.lane.ID, 1, "second")
	if got := stream.next(t).screen(t).Screen; got != "second" {
		t.Fatalf("screen %q", got)
	}
	stream.quiet(t, r.panes, 3)
}

// Nobody watching means nobody reading: the screen is read only while a stream is open.
func TestThePaneIsReadOnlyWhileSomeoneWatches(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, _ := r.signedIn(t)
	time.Sleep(500 * time.Millisecond)
	if reads := r.panes.readCount(); reads != 0 {
		t.Fatalf("the screen was read %d times with nobody watching", reads)
	}
	stream := r.watch(t, cookies, r.lane.ID)
	stream.next(t)
	stream.stop()
	<-stream.ended
	time.Sleep(500 * time.Millisecond)
	settled := r.panes.readCount()
	time.Sleep(time.Second)
	if reads := r.panes.readCount(); reads != settled {
		t.Errorf("the screen was read %d more times after the watcher left", reads-settled)
	}
}

// A restart, a hand-off, or a resume gives the lane a new generation and a new pane, and the stream
// shows that pane with no new request.
func TestThePaneFollowsTheLaneToItsNextGeneration(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "generation one")
	cookies, _ := r.signedIn(t)
	stream := r.watch(t, cookies, r.lane.ID)
	if got := stream.next(t).screen(t); got.Generation != 1 {
		t.Fatalf("snapshot %+v", got)
	}
	r.panes.close(r.lane.ID, 1)
	r.lane.NextGeneration()
	r.panes.show(r.lane.ID, 2, "generation two")
	for {
		event := stream.next(t)
		if event.name == api.PaneEventLane {
			continue
		}
		got := event.screen(t)
		if got.Generation != 2 || got.Screen != "generation two" {
			t.Fatalf("snapshot %+v, want the next generation's", got)
		}
		return
	}
}

// A parked lane has no process, and the stream says so instead of going silent, both to a person
// already watching and to one who opens the page while it is parked.
func TestThePaneSaysWhileTheLaneIsParked(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, _ := r.signedIn(t)
	stream := r.watch(t, cookies, r.lane.ID)
	stream.next(t).screen(t)
	r.panes.close(r.lane.ID, 1)
	r.lane.Park("after 1h")
	for {
		state := stream.next(t).lane(t)
		if state.State == "parked" {
			break
		}
	}
	// A parked lane has no pane to read, so only time passes.
	stream.silent(t, 5*200*time.Millisecond)

	late := r.watch(t, cookies, r.lane.ID)
	if state := late.next(t).lane(t); state.State != "parked" || state.Lane != r.lane.ID {
		t.Errorf("state %+v", state)
	}
}

// A lane that ends under a watcher says so, and the stream closes, because nothing will show again.
func TestThePaneStreamEndsWithTheLane(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, _ := r.signedIn(t)
	stream := r.watch(t, cookies, r.lane.ID)
	stream.next(t).screen(t)
	r.lane.End(workorders.OutcomeCompleted, "Done")
	for {
		event := stream.next(t)
		if event.name == api.PaneEventLane && event.lane(t).State == "ended" {
			break
		}
	}
	select {
	case <-stream.ended:
	case <-time.After(paneWait):
		t.Fatal("the stream outlived its lane")
	}
}

// A pane's stream asks whether its session stands while it runs, as every stream on this listener
// does, so signing out ends it.
func TestThePaneStreamEndsWithItsSession(t *testing.T) {
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, token := r.signedIn(t)
	stream := r.watch(t, cookies, r.lane.ID)
	stream.next(t)
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignOutPath, body: struct{}{}, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
	wantStatus(t, response, raw, http.StatusNoContent, "")
	select {
	case <-stream.ended:
	case <-time.After(paneWait):
		t.Fatal("the stream outlived its session")
	}
}
