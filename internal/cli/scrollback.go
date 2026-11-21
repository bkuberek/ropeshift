package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// laneScrollback prints the newest lines of the scrollback of an interactive lane's pane: the lines that scrolled
// off the top of its screen, which the web UI shows above the screen and copies with Copy all. What an agent printed
// is a stranger's text, so the lines print as plain text, with no escape sequence that could act on the terminal.
// stdout holds only the lines, and stderr says when the pane keeps more or has none yet. --json prints the API's own
// answer, with the lines as the pane holds them.
func (c *invocation) laneScrollback(args []string) int {
	set := c.flags()
	lines := set.Int("lines", api.DefaultPaneScrollback, fmt.Sprintf("how many of the newest lines, from 1 to %d", api.MaxPaneScrollback))
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one lane ID")
	}
	if *lines < 1 || *lines > api.MaxPaneScrollback {
		return c.usageError("--lines is a whole number from 1 to %d, not %d", api.MaxPaneScrollback, *lines)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	history, err := client.PaneScrollback(c.ctx, positional[0], *lines)
	if err == nil && !c.asJSON {
		c.sayScrollbackShown(positional[0], history)
	}
	return c.finish(err, history, func() string { return ledger.PlainText(history.Lines) })
}

// sayScrollbackShown says on stderr what the lines printed leave out: all of a pane's scrollback when it holds
// none yet, or the rest of it when it holds more than the count asked for.
func (c *invocation) sayScrollbackShown(lane string, history api.PaneScrollback) {
	shown := strings.Count(history.Lines, "\n")
	switch {
	case history.Size == 0:
		fmt.Fprintf(c.stderr, "ropeshift %s: lane %s has no scrollback yet\n", c.name, lane)
	case shown < history.Size:
		fmt.Fprintf(c.stderr, "ropeshift %s: showing the newest %d of the %d lines the pane holds; --lines asks for more, up to %d\n", c.name, shown, history.Size, api.MaxPaneScrollback)
	}
}
