package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// Panes is this machine's terminal server as `lane attach` and `plan start` reach it, by the lane and
// the generation that own a pane. A client reaches no adapter, so the entry point supplies it, and a
// machine with no tmux supplies none.
type Panes interface {
	// Has says a generation has a pane a person can use. An error is a read that did not finish.
	Has(ctx context.Context, lane string, generation int64) (bool, error)
	// Attach puts the person's terminal on the pane. It returns when the person detaches or the pane
	// goes, and it leaves the lane running either way.
	Attach(ctx context.Context, lane string, generation int64) error
}

// followPoll is how often an attach that lost its pane asks where the lane went, and followWait is how
// long it waits for a lane that is moving between generations. A parked lane has no limit, because a
// park can last as long as a person takes to answer.
var (
	followPoll = 500 * time.Millisecond
	followWait = 60 * time.Second
)

// panePoll is how often `plan start` looks for its new lane's pane, and paneWait is how long it waits:
// a worker takes the lane, makes its worktree, and opens the pane first.
var (
	panePoll = 500 * time.Millisecond
	paneWait = 2 * time.Minute
)

// detachKeys is tmux's own binding, and a person already inside tmux types its prefix twice, because
// the outer tmux takes the first. The status line is off in the panes ropeshift opens, so nothing on
// the screen says how to leave.
const detachKeys = "C-b d (C-b C-b d inside tmux)"

// laneAttach puts the person's terminal on an interactive lane's pane. Detaching leaves the lane
// running, and the command follows the lane to its next generation when a hand-off, a restart, or a
// resume gives it a new pane.
func (c *invocation) laneAttach(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one lane ID")
	}
	if c.asJSON {
		return c.usageError("attaching is interactive, and prints no JSON")
	}
	if c.terminal == nil {
		return c.usageError("attaching needs a terminal: run this in one")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	// A headless lane has no pane: its stream is what a person follows, and a line typed there is a message.
	// A read that fails leaves the pane to say what it can.
	if streamed, err := headless(client, c.ctx, positional[0]); err == nil && streamed {
		return c.followLane(client, positional[0])
	}
	if c.panes == nil {
		return c.refuse(ExitError, "this machine has no tmux, so it holds no pane to attach to")
	}
	return c.attach(client, positional[0])
}

// attach is the loop of one attached session: attach to the lane's current pane, and when that pane
// goes, look for where the lane went.
func (c *invocation) attach(client *api.ProjectClient, id string) int {
	lane, err := client.Lane(c.ctx, id)
	if err != nil {
		return c.failure(err)
	}
	if lane.ActualState == ledger.StateEnded {
		return c.refuse(ExitConflict, "lane %s has ended", id)
	}
	attached := false
	for {
		has, err := c.panes.Has(c.ctx, id, lane.Generation)
		if err != nil {
			return c.failure(err)
		}
		if has {
			c.say("Attaching to lane %s, generation %d. Detach with %s, which leaves the lane running.\n", id, lane.Generation, detachKeys)
			attachErr := c.panes.Attach(c.ctx, id, lane.Generation)
			attached = true
			stays, err := c.panes.Has(c.ctx, id, lane.Generation)
			if err != nil {
				return c.failure(err)
			}
			if stays {
				// The pane is still there, so the person detached.
				if attachErr != nil && !errors.Is(attachErr, context.Canceled) {
					return c.failure(attachErr)
				}
				return ExitOK
			}
		} else if !attached {
			return c.noPane(lane)
		}
		next, code, done := c.follow(client, id, lane.Generation)
		if done {
			return code
		}
		lane = next
		c.say("Lane %s moved to generation %d.\n", id, lane.Generation)
	}
}

// noPane refuses a lane that has nothing to attach to now, and says why in the lane's own terms.
func (c *invocation) noPane(lane ledger.LaneView) int {
	switch lane.ActualState {
	case ledger.StateParked:
		return c.refuse(ExitConflict, "lane %s is parked, so it has no process to attach to; attach again after it resumes", lane.ID)
	}
	return c.refuse(ExitConflict, "lane %s has no pane on this machine for generation %d (state %s): it runs without a terminal, "+
		"its pane has not opened yet, or another worker holds it", lane.ID, lane.Generation, lane.ActualState)
}

// follow waits for a lane that lost its pane to end, to stop, or to open a pane under a newer
// generation. It returns that lane, or done with the exit code of the command.
func (c *invocation) follow(client *api.ProjectClient, id string, generation int64) (ledger.LaneView, int, bool) {
	deadline := time.Now().Add(followWait)
	announced := false
	for {
		lane, err := client.Lane(c.ctx, id)
		if err != nil {
			return ledger.LaneView{}, c.failure(err), true
		}
		switch lane.ActualState {
		case ledger.StateEnded:
			c.say("Lane %s has ended.\n", id)
			return lane, ExitOK, true
		case "tripped":
			c.say("Lane %s stopped at its restart limit; reset it with 'ropeshift lane reset %s' to run it again.\n", id, lane.WorkOrder)
			return lane, ExitOK, true
		}
		if lane.Generation > generation {
			has, err := c.panes.Has(c.ctx, id, lane.Generation)
			if err != nil {
				return ledger.LaneView{}, c.failure(err), true
			}
			if has {
				return lane, 0, false
			}
		}
		if lane.ActualState == ledger.StateParked {
			if !announced {
				c.say("Lane %s is parked. Waiting for it to resume; stop with Ctrl-C.\n", id)
				announced = true
			}
			deadline = time.Now().Add(followWait)
		} else if time.Now().After(deadline) {
			return lane, c.refuse(ExitConflict, "lane %s has no pane after %s (state %s)", id, followWait, lane.ActualState), true
		}
		select {
		case <-c.ctx.Done():
			return lane, c.failure(c.ctx.Err()), true
		case <-time.After(followPoll):
		}
	}
}

// refuse says why the lane's state leaves nothing to do, and returns the exit code that names it.
func (c *invocation) refuse(exit int, format string, args ...any) int {
	fmt.Fprintf(c.stderr, "ropeshift %s: "+format+"\n", append([]any{c.name}, args...)...)
	return exit
}
