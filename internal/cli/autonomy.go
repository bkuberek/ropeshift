package cli

import (
	"fmt"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

func autonomyLine(view authority.AutonomyView) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s on %s: %s", view.WorkerID, view.SignIn, view.Level)
	if view.Chosen != "" && view.Chosen != view.Level {
		fmt.Fprintf(&out, ", though %s was asked for", view.Chosen)
	}
	if view.Window != nil {
		fmt.Fprintf(&out, ", window open until %s", time.UnixMilli(view.Window.ClosesAt).UTC().Format(time.RFC3339))
	}
	if view.MayClaim {
		out.WriteString(", claims now\n")
	} else {
		out.WriteString(", claims nothing now\n")
	}
	return out.String()
}

func (c *invocation) workerAutonomy(args []string) int {
	worker, ok := c.workerArgument(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	view, err := client.Autonomy(c.ctx, worker)
	return c.finish(err, view, func() string { return autonomyLine(view) })
}

// workerLevel chooses a level. `unattended` on a subscription stops here until a member has read the
// stated risk and acknowledged it, so nobody reaches it by accident.
func (c *invocation) workerLevel(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 2 {
		return c.usageError("this takes one worker and one level")
	}
	client := c.client()
	defer client.Close()
	err := client.SetAutonomy(c.ctx, positional[0], api.SetAutonomy{Level: authority.Level(positional[1])})
	return c.finish(err, api.SetAutonomy{Level: authority.Level(positional[1])}, func() string { return positional[1] + "\n" })
}

// workerRisk prints the statement and the digest an acknowledgement names. A read needs no session,
// and this is how a person comes to hold the digest at all.
func (c *invocation) workerRisk(args []string) int {
	client, ok, code := c.noArgumentsAtInstallation(args)
	if !ok {
		return code
	}
	defer client.Close()
	risk, err := client.Risk(c.ctx)
	return c.finish(err, risk, func() string { return risk.Statement + "\n" + risk.Digest + "\n" })
}

// workerAcknowledge takes the digest and offers no shortcut: a person runs `worker risk`, reads what
// it prints, and names what they read.
func (c *invocation) workerAcknowledge(args []string) int {
	set := c.flags()
	digest := set.String("digest", "", "the digest `worker risk` printed")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one worker")
	}
	if *digest == "" {
		return c.usageError("--digest is required: run ropeshift worker risk, and read what it prints")
	}
	client := c.client()
	defer client.Close()
	err := client.AcknowledgeRisk(c.ctx, positional[0], api.AcknowledgeRisk{Digest: *digest})
	return c.finish(err, api.AcknowledgeRisk{Digest: *digest}, func() string { return "acknowledged for " + positional[0] + "\n" })
}

func (c *invocation) workerWindowOpen(args []string) int {
	set := c.flags()
	var length time.Duration
	set.DurationVar(&length, "for", 0, "how long it stays open, 4h by default and 12h at most")
	id := set.String("id", "", "the window's ID, for a retry")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one worker")
	}
	window := *id
	if window == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		window = generated
	}
	client := c.client()
	defer client.Close()
	row, err := client.OpenWindow(c.ctx, positional[0], api.OpenWindow{ID: window, LengthMs: length.Milliseconds()})
	return c.finish(err, row, func() string {
		return fmt.Sprintf("%s: open until %s\n", row.ID, time.UnixMilli(row.ClosesAt).UTC().Format(time.RFC3339))
	})
}

func (c *invocation) workerWindowClose(args []string) int {
	worker, ok := c.workerArgument(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	err := client.CloseWindow(c.ctx, worker)
	return c.finish(err, api.SetAutonomy{}, func() string { return "closed\n" })
}
