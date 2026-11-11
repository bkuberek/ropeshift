// Package top is `ropeshift top`, an operator's live view of the fleet (FR-UI-3). It reads the API any
// other surface reads, so it decides nothing about what needs a person or in what order (FR-UI-4), and
// it changes nothing: it says which command does.
package top

import (
	"context"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/api"
)

// Snapshot is one read of the fleet. The two views stand at their own revisions, and a surface that
// hears of a later one reads again.
type Snapshot struct {
	Needs api.NeedsYou  `json:"needs_you"`
	Floor api.ShopFloor `json:"shop_floor"`
	// Telemetry is what the worker's local store says of each lane, which needs no backend.
	Telemetry api.LaneTelemetryList `json:"telemetry"`
}

// Revision is the newest revision this read saw.
func (s Snapshot) Revision() int64 { return max(s.Needs.Revision, s.Floor.Revision) }

// Config is what a model runs on.
type Config struct {
	// Read reads the fleet.
	Read func(context.Context) (Snapshot, error)
	// Follow opens the ledger's stream from a revision. Nil means the frame only polls.
	Follow func(context.Context, int64) (<-chan api.StreamEvent, error)
	// Now is the clock ages count from, on this machine.
	Now func() time.Time
}

// Model is the view. It is a value, so a test can hold any one step of it.
type Model struct {
	config   Config
	snapshot Snapshot
	readAt   time.Time
	failure  error
	cursor   int
	width    int
	height   int
	// reading says a read is out, and stale that the ledger moved since it left.
	reading bool
	stale   bool
	events  <-chan api.StreamEvent
}

// New returns a model that has read nothing.
func New(config Config) Model {
	if config.Now == nil {
		config.Now = time.Now
	}
	return Model{config: config}
}

// WithRead replaces the read.
func (m Model) WithRead(read func(context.Context) (Snapshot, error)) Model {
	m.config.Read = read
	return m
}

type (
	loadedMsg    struct{ snapshot Snapshot }
	failedMsg    struct{ err error }
	movedMsg     struct{ revision int64 }
	keyMsg       struct{ key string }
	sizeMsg      struct{ width, height int }
	tickMsg      struct{}
	followingMsg struct {
		events <-chan api.StreamEvent
	}
	followLostMsg struct{}
)

// Loaded is what a read found.
func Loaded(snapshot Snapshot) Msg { return loadedMsg{snapshot} }

// Failed is a read that found nothing.
func Failed(err error) Msg { return failedMsg{err} }

// Moved says the ledger reached a revision.
func Moved(revision int64) Msg { return movedMsg{revision} }

// Pressed is one key.
func Pressed(key string) Msg { return keyMsg{key} }

// Resized says how big the terminal is.
func Resized(width, height int) Msg { return sizeMsg{width, height} }

// Init starts the first read, the stream, and the clock.
func (m Model) Init() Cmd { return batch(m.read(), m.follow(), tick) }

func (m Model) read() Cmd {
	read := m.config.Read
	return func() Msg {
		snapshot, err := read(context.Background())
		if err != nil {
			return failedMsg{err}
		}
		return loadedMsg{snapshot}
	}
}

func (m Model) follow() Cmd {
	if m.config.Follow == nil {
		return nil
	}
	follow, after := m.config.Follow, m.Revision()
	return func() Msg {
		events, err := follow(context.Background(), after)
		if err != nil {
			return followLostMsg{}
		}
		return followingMsg{events}
	}
}

// nextEvent waits for one event, so a slow frame holds the stream back and loses nothing.
func nextEvent(events <-chan api.StreamEvent) Cmd {
	if events == nil {
		return nil
	}
	return func() Msg {
		event, open := <-events
		if !open {
			return followLostMsg{}
		}
		return movedMsg{event.Rev}
	}
}

// reread starts a read, or says one is due when one is out, so a burst of events costs two reads.
func (m Model) reread() (Model, Cmd) {
	if m.reading {
		m.stale = true
		return m, nil
	}
	m.reading, m.stale = true, false
	return m, m.read()
}

// Update takes one message.
func (m Model) Update(msg Msg) (Model, Cmd) {
	switch msg := msg.(type) {
	case keyMsg:
		return m.key(msg.key)
	case sizeMsg:
		m.width, m.height = msg.width, msg.height
	case loadedMsg:
		m = m.loaded(msg.snapshot)
		if m.stale {
			return m.reread()
		}
	case failedMsg:
		m.reading, m.failure = false, msg.err
	case followingMsg:
		m.events = msg.events
		return m, nextEvent(m.events)
	case followLostMsg:
		m.events = nil
	case movedMsg:
		if msg.revision <= m.Revision() {
			return m, nextEvent(m.events)
		}
		next, read := m.reread()
		return next, batch(read, nextEvent(next.events))
	case tickMsg:
		next, read := m.reread()
		if next.events == nil {
			return next, batch(read, next.follow(), tick)
		}
		return next, batch(read, tick)
	}
	return m, nil
}

func (m Model) key(key string) (Model, Cmd) {
	switch key {
	case KeyCtrlC, KeyEsc, "q":
		return m, quit
	case KeyDown, "j":
		return m.moved(1), nil
	case KeyUp, "k":
		return m.moved(-1), nil
	case "r":
		return m.reread()
	}
	return m, nil
}

func (m Model) moved(by int) Model {
	m.cursor = max(0, min(m.cursor+by, len(m.snapshot.Needs.Items)-1))
	return m
}

// loaded keeps the cursor on what it stood on, because a list that moved under a person's eye must not
// move what they read. What left the list leaves the cursor at the same height.
func (m Model) loaded(snapshot Snapshot) Model {
	selected := m.Selected()
	m.snapshot, m.reading, m.failure, m.readAt = snapshot, false, nil, m.config.Now()
	at := slices.IndexFunc(snapshot.Needs.Items, func(item api.NeedItem) bool { return item.ID == selected })
	if at >= 0 {
		m.cursor = at
		return m
	}
	return m.moved(0)
}

// Selected names what the cursor stands on.
func (m Model) Selected() string {
	items := m.snapshot.Needs.Items
	if m.cursor >= len(items) || m.cursor < 0 {
		return ""
	}
	return items[m.cursor].ID
}

// Revision is the revision the last read stood at.
func (m Model) Revision() int64 { return m.snapshot.Revision() }

// LaneOrder lists the lanes for an operator: those that need a person first, in the order the server
// put them, and then the rest by name, so a line stays where it was while nothing changes.
func LaneOrder(snapshot Snapshot) []api.FloorLane {
	rank := map[string]int{}
	for i, item := range snapshot.Needs.Items {
		if item.Work == nil || item.Work.Lane == nil {
			continue
		}
		if _, seen := rank[item.Work.Lane.ID]; !seen {
			rank[item.Work.Lane.ID] = i
		}
	}
	lanes := slices.Clone(snapshot.Floor.Lanes)
	slices.SortStableFunc(lanes, func(a, b api.FloorLane) int {
		left, leftWaits := rank[a.ID]
		right, rightWaits := rank[b.ID]
		switch {
		case leftWaits && rightWaits:
			return left - right
		case leftWaits:
			return -1
		case rightWaits:
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return lanes
}

// Reader reads the fleet through the one client every surface uses: what waits in every active project,
// and the lanes of the project the client calls. The revision it follows is that project's.
func Reader(client *api.ProjectClient) func(context.Context) (Snapshot, error) {
	return func(ctx context.Context) (Snapshot, error) {
		var snapshot Snapshot
		needs, err := client.NeedsYou(ctx)
		if err != nil {
			return snapshot, err
		}
		for _, project := range needs.Projects {
			if project.Name == client.Name() {
				needs.Revision = project.Revision
			}
		}
		snapshot.Needs = needs
		if err := client.ReadView(ctx, api.ViewShopFloor, "", &snapshot.Floor); err != nil {
			return snapshot, err
		}
		snapshot.Telemetry, err = client.LaneTelemetry(ctx)
		return snapshot, err
	}
}

// Frame is one frame of a fleet read once, for a terminal, a pipe, or a script's log, clipped to a
// width. A width of zero or less is the plain width, 120 columns.
func Frame(snapshot Snapshot, now time.Time, width int) string {
	m := New(Config{Now: func() time.Time { return now }}).loaded(snapshot)
	m.width = width
	return m.View() + "\n"
}
