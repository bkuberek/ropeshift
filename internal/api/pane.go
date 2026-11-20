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
	// PaneScrollbackPath reads the newest lines of the scrollback of a lane's pane.
	PaneScrollbackPath = "/v1/panes/{id}/scrollback"
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

// PaneInput is what a member types into a pane: literal text, which Enter may follow, or keys. Key is a
// key sequence in the pane's key grammar, which the server reads into the terminal's names for the keys,
// so no text a page sends reaches the terminal as a key name it was sent as.
//
// A chord is zero or more of the modifiers Ctrl, Alt, and Shift, each followed by a hyphen, then one of
// PaneKeyNames or one printable character: Enter, Shift-Tab, Ctrl-O, Alt-x, or Ctrl--. Modifiers and
// names are read in any case. Shift goes only with a named key, because a character carries its own
// Shift: send A, not Shift-a. A sequence is one to MaxPaneChords chords, each separated from the next by
// one space: Ctrl-X Ctrl-S.
type PaneInput struct {
	Text  string `json:"text,omitempty"`
	Enter bool   `json:"enter,omitempty"`
	Key   string `json:"key,omitempty"`
}

// PaneKeyNames are the named keys a chord may end with. They are tmux's names for them
// (https://man.openbsd.org/tmux.1#KEY_BINDINGS): BSpace is Backspace, BTab is Shift-Tab, DC is Delete,
// and IC is Insert.
var PaneKeyNames = []string{
	"Up", "Down", "Left", "Right", "BSpace", "BTab", "DC", "End", "Enter", "Escape",
	"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
	"Home", "IC", "PageUp", "PageDown", "Space", "Tab",
}

// PaneModifiers are the modifiers a chord may hold.
var PaneModifiers = []string{"Ctrl", "Alt", "Shift"}

// MaxPaneChords bounds the chords of one key sequence.
const MaxPaneChords = 8

// MaxPaneKeys bounds a key sequence, in bytes: eight of the longest chords, with the spaces between them.
const MaxPaneKeys = 256

// MaxPaneText bounds the text of one input, in bytes. A person types a line at a time.
const MaxPaneText = 4096

// PaneScrollback is a read of a pane's scrollback, the lines that scrolled off the top of its screen: the
// newest of them the read asked for, oldest first, each ending in a newline, with the escape sequences that
// set their colours and emphasis, and how many lines the history holds now and keeps at most.
type PaneScrollback struct {
	Lane       string `json:"lane_id"`
	Generation int64  `json:"generation"`
	Lines      string `json:"lines"`
	Size       int    `json:"size"`
	Limit      int    `json:"limit"`
}

// A read of a scrollback asks for DefaultPaneScrollback lines when it names no count, and for at most
// MaxPaneScrollback.
const (
	DefaultPaneScrollback = 200
	MaxPaneScrollback     = 10000
)
