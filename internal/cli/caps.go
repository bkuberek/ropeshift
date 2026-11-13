package cli

import (
	"fmt"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// workerCaps shows a worker's lane caps, and with a flag sets them (FR-PROJ-7). A flag left out keeps its
// value. A per-worker cap above the recommendation is saved, and a warning names the recommendation and
// why.
func (c *invocation) workerCaps(args []string) int {
	set := c.flags()
	maxLanes := set.String("max-lanes", "", "lanes this machine runs: a number, none for no cap, or recommended")
	perSignIn := set.String("per-sign-in", "", "lanes on one agent sign-in: a number, or none for no cap")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one worker")
	}
	worker := positional[0]
	var change api.SetLaneCaps
	changing := *maxLanes != "" || *perSignIn != ""
	if *maxLanes != "" {
		switch value, err := strconv.ParseInt(*maxLanes, 10, 64); {
		case *maxLanes == "recommended":
		case *maxLanes == "none":
			change.MaxLanes = new(int64)
		case err == nil && value >= 0:
			change.MaxLanes = &value
		default:
			return c.usageError("--max-lanes is a number, none, or recommended")
		}
	}
	if *perSignIn != "" && *perSignIn != "none" {
		value, err := strconv.ParseInt(*perSignIn, 10, 64)
		if err != nil || value < 0 {
			return c.usageError("--per-sign-in is a number, or none")
		}
		change.MaxLanesPerSignIn = value
	}
	client := c.client()
	defer client.Close()
	view, err := client.LaneCaps(c.ctx, worker)
	if err != nil || !changing {
		return c.finish(err, view, func() string { return capsText(view) })
	}
	if *maxLanes == "" {
		change.MaxLanes = view.MaxLanes
	}
	if *perSignIn == "" {
		change.MaxLanesPerSignIn = view.MaxLanesPerSignIn
	}
	view, err = client.SetLaneCaps(c.ctx, worker, change)
	if err == nil && view.Warning != nil && !c.asJSON {
		fmt.Fprintf(c.stderr, "ropeshift %s: warning: %s\n", c.name, api.Printable(view.Warning.Message))
	}
	return c.finish(err, view, func() string { return capsText(view) })
}

// capsText states a worker's caps the way a person reads them, with the recommendation and its reason,
// which the worker wrote and so prints safe for a terminal.
func capsText(view ledger.WorkerCaps) string {
	var out strings.Builder
	switch {
	case view.MaxLanes != nil:
		fmt.Fprintf(&out, "max_lanes\t%s\n", capWords(*view.MaxLanes))
	case view.Recommendation == nil:
		out.WriteString("max_lanes\tthe recommendation, which this worker has not reported, so no cap\n")
	default:
		fmt.Fprintf(&out, "max_lanes\tthe recommendation, %d\n", view.EffectiveMaxLanes)
	}
	fmt.Fprintf(&out, "max_lanes_per_sign_in\t%s\n", capWords(view.MaxLanesPerSignIn))
	if view.Recommendation != nil {
		fmt.Fprintf(&out, "recommended\t%d: %s\n", view.Recommendation.MaxLanes, api.Printable(view.Recommendation.Reason))
	} else {
		out.WriteString("recommended\tnot reported yet\n")
	}
	return out.String()
}

// capWords says a cap, where zero is no cap.
func capWords(value int64) string {
	if value == 0 {
		return "no cap"
	}
	return strconv.FormatInt(value, 10)
}
