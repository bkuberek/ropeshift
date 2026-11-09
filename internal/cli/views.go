package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/top"
)

// viewing prints one read model. Text and JSON hold the same document, because a read model is data
// for a surface to lay out, and a terminal has `work`, `lane`, and `top` for that.
func viewing(name string) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		set := c.flags()
		category := set.String("category", "", "only this category, for a view that files by one")
		positional, ok := c.parse(set, args)
		if !ok {
			return ExitUsage
		}
		if len(positional) != 0 {
			return c.usageError("unexpected argument %q", positional[0])
		}
		client := c.client()
		defer client.Close()
		var raw json.RawMessage
		err := client.ReadView(c.ctx, name, *category, &raw)
		return c.finish(err, raw, func() string {
			indented, err := json.MarshalIndent(raw, "", "  ")
			if err != nil {
				return string(raw) + "\n"
			}
			return string(indented) + "\n"
		})
	}
}

// runTop is `ropeshift top`. The first read comes before the screen changes, so a daemon that is down
// exits 9 as any other command does. With no terminal it prints one frame, because a pipe cannot hold
// a live view.
func (c *invocation) runTop(args []string) int {
	set := c.flags()
	once := set.Bool("once", false, "print one frame and stop")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	read := top.Reader(client)
	snapshot, err := read(c.ctx)
	if err != nil {
		return c.failure(err)
	}
	if *once || c.asJSON || c.terminal == nil {
		return c.finish(nil, snapshot, func() string { return top.Frame(snapshot, time.Now()) })
	}
	config := top.Config{Read: read, Follow: client.Events, Now: time.Now}
	if err := top.Run(c.ctx, config, c.terminal, c.stdout); err != nil {
		return c.failure(err)
	}
	return ExitOK
}

// noticeList says what each channel was told. More than one attempt with a delivery is a notice its
// channel may have heard twice, because delivery is at least once.
func (c *invocation) noticeList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	rows, err := client.Notices(c.ctx)
	if rows == nil {
		rows = []ledger.NoticeLine{}
	}
	return c.finish(err, api.NoticeList{Notices: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			state := "delivered"
			if !row.Delivered {
				state = "not delivered"
			}
			fmt.Fprintf(&out, "%s\t%s\t%s\t%s\tattempts %d\t%s\n", row.Delivery, row.Channel, row.Kind, row.Reason, row.Attempts, state)
		}
		return out.String()
	})
}

func effectLine(row ledger.UnknownEffect) string {
	state := "waiting"
	if !row.Waiting() {
		state = string(row.Outcome)
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", row.ID, state, row.Kind, row.Lane, row.Key)
}

func (c *invocation) effectList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	rows, err := client.Effects(c.ctx)
	if rows == nil {
		rows = []ledger.UnknownEffect{}
	}
	return c.finish(err, rows, func() string {
		var out strings.Builder
		for _, row := range rows {
			out.WriteString(effectLine(row))
		}
		return out.String()
	})
}

func (c *invocation) effectShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	row, err := client.Effect(c.ctx, id)
	return c.finish(err, row, func() string { return effectLine(row) })
}

// effectDecide is a person's word on what a crash left unknown. Nothing else can say what happened,
// and `retry` runs the effect again whether or not it happened the first time.
func (c *invocation) effectDecide(args []string) int {
	set := c.flags()
	outcome := set.String("outcome", "", "happened, not_happened, or retry")
	reason := set.String("reason", "", "why")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if *outcome == "" {
		return c.usageError("--outcome is required: happened, not_happened, or retry")
	}
	client := c.client()
	defer client.Close()
	row, err := client.DecideEffect(c.ctx, id, api.DecideEffect{Outcome: ledger.EffectOutcome(*outcome), Reason: *reason})
	return c.finish(err, row, func() string { return effectLine(row) })
}

// events follows the ledger. It prints one line per event, and with --limit it stops after that many,
// so a script can read what it needs and go.
func (c *invocation) events(args []string) int {
	set := c.flags()
	after := set.Int64("after", 0, "the revision to read from")
	limit := set.Int("limit", 0, "stop after this many events")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	events, err := client.Events(ctx, *after)
	if err != nil {
		return c.failure(err)
	}
	seen := 0
	for event := range events {
		if c.asJSON {
			if err := json.NewEncoder(c.stdout).Encode(event); err != nil {
				return c.failure(err)
			}
		} else {
			fmt.Fprintf(c.stdout, "%d\t%s\t%s\t%s\n", event.Rev, event.Type, event.SubjectKind, event.SubjectID)
		}
		seen++
		if *limit > 0 && seen == *limit {
			return ExitOK
		}
	}
	return ExitOK
}

// signOut ends the session this shell holds, so its token opens nothing even if something kept it.
func (c *invocation) signOut(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if c.session == "" {
		return c.failure(errors.New("this shell holds no session"))
	}
	client := c.client()
	defer client.Close()
	err := client.SignOut(c.ctx)
	return c.finish(err, struct {
		SignedOut bool `json:"signed_out"`
	}{true}, func() string { return "signed out\n" })
}
