package cli

import (
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

func orchestratorLine(row api.Orchestrator) string {
	actual := api.OrchestratorState(row)
	if actual == "" {
		actual = "no status yet"
	}
	line := fmt.Sprintf("%s on %s: asked to be %s, reads %s, generation %d\n", row.ID, row.WorkerID, row.Desired, actual, row.Generation)
	if row.Hold != nil {
		line += "held: " + holdWords(*row.Hold) + "\n"
	}
	if row.Scheduled != nil {
		line += "scheduled: " + scheduledWords(*row.Scheduled) + "\n"
	}
	return line
}

// holdWords says what keeps an orchestrator from starting or running.
func holdWords(hold ledger.OrchestratorHold) string {
	switch hold.Reason {
	case ledger.HoldOffline:
		return "the worker is offline"
	case ledger.HoldTripped:
		return "its breaker tripped; orchestrator start resets it"
	}
	words := "the usage limit of the " + windowWords(hold.Window) + " is reached"
	switch {
	case hold.ResetsAt == 0:
		return words + ", and no reset time was reported"
	case hold.Stale:
		return words + ", reset reported for " + clock(hold.ResetsAt) + ", which may be out of date"
	}
	return words + ", resets at " + clock(hold.ResetsAt)
}

// windowWords names a usage window as a person reads it.
func windowWords(window string) string {
	switch window {
	case ledger.UsageWindowSession:
		return "five-hour session window"
	case ledger.UsageWindowWeekly:
		return "weekly window"
	}
	return "sign-in"
}

// scheduledWords says when a scheduled start starts the orchestrator.
func scheduledWords(start ledger.ScheduledStart) string {
	if start.StartsAt == 0 {
		return "starts at the next reported reset"
	}
	return "starts at " + clock(start.StartsAt)
}

// clock prints a time in epoch milliseconds as RFC 3339 in UTC.
func clock(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

// workerArgument reads a command's one worker.
func (c *invocation) workerArgument(args []string) (string, bool) {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return "", false
	}
	if len(positional) != 1 {
		c.usageError("this takes one worker")
		return "", false
	}
	return positional[0], true
}

func (c *invocation) orchestratorShow(args []string) int {
	worker, ok := c.workerArgument(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	row, err := client.Orchestrator(c.ctx, worker)
	return c.finish(err, row, func() string { return orchestratorLine(row) })
}

func (c *invocation) orchestratorSchedule(args []string) int {
	worker, ok := c.workerArgument(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	start, err := client.ScheduleOrchestrator(c.ctx, worker)
	return c.finish(err, start, func() string { return worker + "\t" + scheduledWords(start) + "\n" })
}

func (c *invocation) orchestratorUnschedule(args []string) int {
	worker, ok := c.workerArgument(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	err := client.CancelScheduledStart(c.ctx, worker)
	return c.finish(err, struct {
		Canceled string `json:"canceled"`
	}{worker}, func() string { return worker + "\tscheduled start canceled\n" })
}

// setsOrchestrator is the command of one desired state. Starting one is the only way an agent comes
// to run with no lane dispatched, so this takes a member's session like any other change.
func setsOrchestrator(desired string) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		worker, ok := c.workerArgument(args)
		if !ok {
			return ExitUsage
		}
		client := c.client()
		defer client.Close()
		row, err := client.SetOrchestrator(c.ctx, worker, api.SetOrchestrator{Desired: desired})
		return c.finish(err, row, func() string { return orchestratorLine(row) })
	}
}
