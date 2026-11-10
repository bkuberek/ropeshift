package api

// The web pane is an interactive lane's terminal pane as a member's browser reaches it, through the
// worker that holds the pane, so a person has one conversation whichever surface they use
// (ADR-0018). A pane is named by its lane's ID, and the routes follow the lane to the pane of its
// current generation. They live apart from a lane's own routes, because typing into a pane writes
// nothing a lane's state is made of.
const (
	// PanePath streams a lane's pane: a snapshot of its screen each time the screen changes.
	PanePath = "/v1/panes/{id}"
	// PaneInputPath types into a lane's pane.
	PaneInputPath = "/v1/panes/{id}/input"
)

// The refusals of the web pane.
const (
	// CodeNoPane means the lane has no pane to show or type into now: it runs without a terminal,
	// its pane has not opened yet, it is parked, or no worker this control plane reaches holds it.
	CodeNoPane = "no_pane"
	// CodeLaneEnded means the lane has ended, so it will never have a pane again.
	CodeLaneEnded = "lane_ended"
)

// The events of a pane's stream.
const (
	// PaneEventScreen carries a PaneScreen.
	PaneEventScreen = "screen"
	// PaneEventLane carries a PaneLane, when the lane has no screen to show.
	PaneEventLane = "lane"
)

// PaneScreen is one snapshot of a pane: the visible screen, with the escape sequences that set its
// colours and emphasis, from the pane of the generation it names.
type PaneScreen struct {
	Lane       string `json:"lane_id"`
	Generation int64  `json:"generation"`
	Screen     string `json:"screen"`
}

// PaneLane says a lane has no screen to show now, and the state that says why: a parked lane has no
// process until it resumes, an ended one has none for good, and a lane in any other state is between
// panes or has none on a worker this control plane reaches.
type PaneLane struct {
	Lane       string `json:"lane_id"`
	Generation int64  `json:"generation"`
	State      string `json:"state"`
}

// PaneInput is what a member types into a pane: literal text, which Enter may follow, or one key from
// PaneKeys. The key list is closed, so no key name a page sends reaches the terminal as it was sent.
type PaneInput struct {
	Text  string `json:"text,omitempty"`
	Enter bool   `json:"enter,omitempty"`
	Key   string `json:"key,omitempty"`
}

// PaneKeys are the keys a member may press in a pane, by the names the API takes.
var PaneKeys = []string{"Enter", "Escape", "Tab", "Backspace", "Up", "Down", "Left", "Right", "Ctrl-C", "Ctrl-D"}

// MaxPaneText bounds the text of one input, in bytes. A person types a line at a time.
const MaxPaneText = 4096
