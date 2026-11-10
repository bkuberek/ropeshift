package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// panes is a machine's terminal server, scripted: which panes it holds, and what a person does while
// attached to one.
type panes struct {
	mu       sync.Mutex
	held     map[string]bool
	attached []string
	// during runs while a person is attached, and the pane it names is where they are.
	during func(lane string, generation int64) error
}

func paneKey(lane string, generation int64) string { return fmt.Sprintf("%s-g%d", lane, generation) }

func (p *panes) hold(lane string, generation int64, held bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.held == nil {
		p.held = map[string]bool{}
	}
	p.held[paneKey(lane, generation)] = held
}

func (p *panes) Has(_ context.Context, lane string, generation int64) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.held[paneKey(lane, generation)], nil
}

func (p *panes) Attach(_ context.Context, lane string, generation int64) error {
	p.mu.Lock()
	p.attached = append(p.attached, paneKey(lane, generation))
	during := p.during
	p.mu.Unlock()
	if during == nil {
		return nil
	}
	return during(lane, generation)
}

func (p *panes) attachments() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.attached...)
}

// lanes is the plane's view of lanes, which a test moves as the session goes on.
type lanes struct {
	mu   sync.Mutex
	rows map[string]ledger.LaneView
}

func (l *lanes) set(id, state string, generation int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rows == nil {
		l.rows = map[string]ledger.LaneView{}
	}
	l.rows[id] = ledger.LaneView{ID: id, WorkOrder: "wo-" + id, Generation: generation, LaneStatus: ledger.LaneStatus{ActualState: state}}
}

func (l *lanes) serve(p *plane) {
	p.on("GET /v1/lanes/{id}", func(r *http.Request, _ []byte) (any, *api.Error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		row, ok := l.rows[r.PathValue("id")]
		if !ok {
			return nil, notFound("lane " + r.PathValue("id"))
		}
		return row, nil
	})
}

// attachTo runs `lane attach` with a terminal and the given panes.
func attachTo(t *testing.T, p *plane, held *panes, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts := cli.Options{Socket: p.Socket, Terminal: strings.NewReader(""), Panes: held}
	code := cli.Run(append([]string{"lane", "attach"}, args...), &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

func quickly(t *testing.T) {
	t.Helper()
	t.Cleanup(cli.FollowTiming(2*time.Millisecond, 300*time.Millisecond))
}

func TestLaneAttachRefusesWhatHasNothingToAttachTo(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*lanes, *panes)
		args   []string
		want   int
		stderr string
	}{
		{"a lane that does not exist", func(*lanes, *panes) {}, []string{"nowhere"}, cli.ExitNotFound, "does not exist"},
		{
			"a lane that ended", func(l *lanes, _ *panes) { l.set("l1", ledger.StateEnded, 2) }, []string{"l1"},
			cli.ExitConflict, "ended",
		},
		{
			"a lane with no pane, as a headless lane has none", func(l *lanes, _ *panes) { l.set("l1", "working", 1) }, []string{"l1"},
			cli.ExitConflict, "has no pane",
		},
		{
			"a parked lane, which has no process", func(l *lanes, _ *panes) { l.set("l1", ledger.StateParked, 1) }, []string{"l1"},
			cli.ExitConflict, "parked",
		},
		{"no lane named", func(*lanes, *panes) {}, nil, cli.ExitUsage, "takes one lane ID"},
		{"two lanes named", func(*lanes, *panes) {}, []string{"a", "b"}, cli.ExitUsage, "takes one lane ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quickly(t)
			p, rows, held := newPlane(t), &lanes{}, &panes{}
			rows.serve(p)
			tt.setup(rows, held)
			code, _, stderr := attachTo(t, p, held, tt.args...)
			if code != tt.want || !strings.Contains(stderr, tt.stderr) {
				t.Errorf("exit %d (want %d): %s", code, tt.want, stderr)
			}
			if got := held.attachments(); len(got) != 0 {
				t.Errorf("attached to %v", got)
			}
		})
	}
}

func TestLaneAttachNeedsATerminalAndTmux(t *testing.T) {
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	t.Run("a pipe is no terminal", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{"lane", "attach", "l1"}, &stdout, &stderr, cli.Options{Socket: p.Socket, Panes: held})
		if code != cli.ExitUsage || !strings.Contains(stderr.String(), "terminal") {
			t.Errorf("exit %d: %s", code, stderr.String())
		}
	})
	t.Run("a machine with no tmux holds no pane", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{"lane", "attach", "l1"}, &stdout, &stderr, cli.Options{Socket: p.Socket, Terminal: strings.NewReader("")})
		if code != cli.ExitError || !strings.Contains(stderr.String(), "tmux") {
			t.Errorf("exit %d: %s", code, stderr.String())
		}
	})
	t.Run("a person attaches to a live pane, with JSON refused", func(t *testing.T) {
		code, _, stderr := attachTo(t, p, held, "l1", "--json")
		if code != cli.ExitUsage || !strings.Contains(stderr, "JSON") {
			t.Errorf("exit %d: %s", code, stderr)
		}
	})
	if got := held.attachments(); len(got) != 0 {
		t.Errorf("attached to %v", got)
	}
}

// A person who detaches leaves the lane running: the command ends, and it does not look for another
// pane to attach to.
func TestLaneAttachEndsWhenThePersonDetaches(t *testing.T) {
	quickly(t)
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	code, stdout, stderr := attachTo(t, p, held, "l1")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := held.attachments(); len(got) != 1 || got[0] != "l1-g1" {
		t.Errorf("attached to %v, want the lane's first pane once", got)
	}
	if !strings.Contains(stdout, "C-b d") {
		t.Errorf("no hint for the detach keys, which the status line no longer shows: %q", stdout)
	}
}

// A hand-off or a resume makes a new pane under a new generation, and a person who was watching the
// old one follows the lane to it.
func TestLaneAttachFollowsTheLaneToItsNextGeneration(t *testing.T) {
	quickly(t)
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	held.during = func(lane string, generation int64) error {
		if generation != 1 {
			return nil
		}
		// The first generation hands off: its pane goes, and the next one opens a moment later.
		held.hold(lane, 1, false)
		rows.set(lane, "handing_off", 1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			held.hold(lane, 2, true)
			rows.set(lane, "working", 2)
		}()
		return nil
	}
	code, stdout, stderr := attachTo(t, p, held, "l1")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := held.attachments(); len(got) != 2 || got[0] != "l1-g1" || got[1] != "l1-g2" {
		t.Errorf("attached to %v, want both generations in order", got)
	}
	if !strings.Contains(stdout, "generation 2") {
		t.Errorf("a person is not told the lane moved: %q", stdout)
	}
}

// A park closes the pane and a resume opens the next one, maybe much later, and a person who wants
// the lane keeps their command until it comes back.
func TestLaneAttachWaitsThroughAPark(t *testing.T) {
	quickly(t)
	t.Cleanup(cli.FollowTiming(2*time.Millisecond, 30*time.Millisecond))
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	held.during = func(lane string, generation int64) error {
		if generation != 1 {
			return nil
		}
		held.hold(lane, 1, false)
		rows.set(lane, ledger.StateParked, 1)
		go func() {
			// Longer than the time to give up on a lane that is not parked.
			time.Sleep(150 * time.Millisecond)
			held.hold(lane, 2, true)
			rows.set(lane, "working", 2)
		}()
		return nil
	}
	code, stdout, stderr := attachTo(t, p, held, "l1")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := held.attachments(); len(got) != 2 || got[1] != "l1-g2" {
		t.Errorf("attached to %v, want the resumed generation", got)
	}
	if !strings.Contains(stdout, "parked") {
		t.Errorf("a person is not told why nothing is on the screen: %q", stdout)
	}
}

func TestLaneAttachSaysWhenTheLaneEndedUnderThePerson(t *testing.T) {
	quickly(t)
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	held.during = func(lane string, generation int64) error {
		held.hold(lane, generation, false)
		rows.set(lane, ledger.StateEnded, generation)
		return nil
	}
	code, stdout, stderr := attachTo(t, p, held, "l1")
	if code != cli.ExitOK || !strings.Contains(stdout, "ended") {
		t.Errorf("exit %d: %q, %q", code, stdout, stderr)
	}
	if got := held.attachments(); len(got) != 1 {
		t.Errorf("attached to %v after the lane ended", got)
	}
}

func TestLaneAttachGivesUpOnALaneThatNeverComesBack(t *testing.T) {
	quickly(t)
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	held.during = func(lane string, generation int64) error {
		held.hold(lane, generation, false)
		return nil
	}
	code, _, stderr := attachTo(t, p, held, "l1")
	if code != cli.ExitConflict || !strings.Contains(stderr, "no pane") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

func TestLaneAttachReportsAClientThatFailedWhileThePaneStays(t *testing.T) {
	quickly(t)
	p, rows, held := newPlane(t), &lanes{}, &panes{}
	rows.serve(p)
	rows.set("l1", "working", 1)
	held.hold("l1", 1, true)
	held.during = func(string, int64) error { return errors.New("open terminal failed") }
	code, _, stderr := attachTo(t, p, held, "l1")
	if code != cli.ExitError || !strings.Contains(stderr, "open terminal failed") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// The whole follow on the terminal's own fake: a person attached to a generation's pane watches a park
// close it and a resume open the next generation's, under the name the driver gives that generation, and
// the command ends only when they detach from the second.
func TestLaneAttachFollowsAParkAndResumeOnTheFakeTerminal(t *testing.T) {
	quickly(t)
	p, rows := newPlane(t), &lanes{}
	rows.serve(p)
	terminal := fake.NewTerminal()
	open := func(generation int64) string {
		name := claudecode.PaneName("lane.1", generation)
		if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
			t.Fatal(err)
		}
		return name
	}
	first := open(1)
	rows.set("lane.1", "working", 1)
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		opts := cli.Options{Socket: p.Socket, Terminal: strings.NewReader(""), Panes: claudecode.Panes{Terminal: terminal}}
		done <- cli.Run([]string{"lane", "attach", "lane.1"}, &stdout, &stderr, opts)
	}()
	waitAttached := func(count int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); len(terminal.Attached()) < count; {
			if time.Now().After(deadline) {
				t.Fatalf("attached to %v, want %d attaches: %s", terminal.Attached(), count, stderr.String())
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitAttached(1)

	// The lane parks: its process ends, and the worker closes the pane.
	rows.set("lane.1", ledger.StateParked, 1)
	if err := terminal.Close(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	// A member answers, and the lane resumes in a new generation with a new pane.
	second := open(2)
	rows.set("lane.1", "working", 2)
	waitAttached(2)
	terminal.Detach(second)

	if code := <-done; code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got := terminal.Attached(); !slices.Equal(got, []string{first, second}) {
		t.Errorf("attached to %v, want both generations' panes in order", got)
	}
}
