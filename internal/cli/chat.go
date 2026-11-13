package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/messages"
)

// talkPoll is how often a followed stream asks for what is new, and talkBacklog how many of a stream's
// newest entries a follow prints first.
var (
	talkPoll    = 500 * time.Millisecond
	talkBacklog = 20
)

// talkLine is one line of a stream as a terminal reads it.
func talkLine(entry api.TalkEntry) string {
	at := time.UnixMilli(entry.At).Format("15:04")
	var line string
	switch entry.Kind {
	case api.TalkKindMember:
		state := ""
		if entry.Message != nil {
			state = "  [" + string(entry.Message.State) + "]"
		}
		line = fmt.Sprintf("%s  %s: %s%s", at, fromLabel(entry.From, "you"), entry.Body, state)
	case api.TalkKindReply:
		line = fmt.Sprintf("%s  %s: %s", at, fromLabel(entry.From, "reply"), entry.Body)
	case api.TalkKindAction:
		line = fmt.Sprintf("%s  · ran %s (%s)", at, entry.Action.Label, entry.Action.Operation)
	case api.TalkKindCard:
		line = fmt.Sprintf("%s  · prepared a card: %s", at, entry.Action.Label)
		if entry.Approval != nil {
			line = fmt.Sprintf("%s  · card %s: %s — %s; confirm with 'ropeshift confirm %s'", at, entry.Approval.ID, entry.Approval.Summary, entry.Approval.State, entry.Approval.ID)
		}
	case api.TalkKindRequest:
		line = fmt.Sprintf("%s  · request %s: %s — %s", at, entry.Approval.ID, entry.Approval.Summary, entry.Approval.State)
	case api.TalkKindQuestion:
		line = fmt.Sprintf("%s  · question %s: %s — %s", at, entry.Question.Question.ID, entry.Question.Question.Text, entry.Question.Question.Status)
	case api.TalkKindEvent:
		line = fmt.Sprintf("%s  · %s %s %s by %s", at, entry.Event.Type, entry.Event.SubjectKind, entry.Event.SubjectID, entry.Event.ActorKind)
	case api.TalkKindMarker:
		line = fmt.Sprintf("%s  -- %s --", at, markerWords[entry.Marker])
	default:
		line = fmt.Sprintf("%s  %s", at, entry.Body)
	}
	if entry.BeforeClear {
		line += "  (before the clear)"
	}
	line += "\n"
	if entry.Assent {
		line += "       A message never confirms a card. The card above waits for a member's confirmation.\n"
	}
	return line
}

var markerWords = map[string]string{
	conversations.MarkerStarted: "session started", conversations.MarkerStopped: "session stopped",
	conversations.MarkerCompacted: "context compacted", conversations.MarkerCleared: "context cleared; earlier messages stay readable",
	conversations.MarkerIdleStopped: "session stopped after it sat idle",
}

func fromLabel(from *api.TalkFrom, fallback string) string {
	if from == nil || from.Label == "" {
		return fallback
	}
	return from.Label
}

// conversationLine says what one conversation is and how it stands.
func conversationLine(c api.Conversation) string {
	line := fmt.Sprintf("%s  %q  %s", c.ID, c.Name, c.Actual)
	if c.WorkerName != "" {
		line += " on " + c.WorkerName
	}
	if c.OpenCards != 0 {
		line += fmt.Sprintf("  %d open card(s)", c.OpenCards)
	}
	if c.StoppedReason != "" && !c.Running() {
		line += "  (" + c.StoppedReason + ")"
	}
	return line + "\n"
}

// openedLines says what a start did: the conversation, and the running one it stopped first.
func openedLines(opened api.ConversationOpened) string {
	var out strings.Builder
	out.WriteString(conversationLine(opened.Conversation))
	for _, stopped := range opened.Stopped {
		fmt.Fprintf(&out, "The running conversation %s stopped first.\n", stopped.ID)
	}
	return out.String()
}

// following prints a stream's newest entries, then each new one as it arrives, and hands each line the
// person types to send, until their input ends.
type following struct {
	read func(ctx context.Context, q api.TalkQuery) (api.TalkPage, error)
	send func(ctx context.Context, body string) error
	seen map[string]bool
}

func (c *invocation) followStream(f following) error {
	f.seen = map[string]bool{}
	first, err := f.read(c.ctx, api.TalkQuery{Limit: talkBacklog})
	if err != nil {
		return err
	}
	c.printNew(f, first.Entries)
	lines := make(chan string)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(c.terminal)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-c.ctx.Done():
				return
			}
		}
		done <- scanner.Err()
	}()
	ticker := time.NewTicker(talkPoll)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return nil
		case err := <-done:
			c.catchUp(f)
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case line := <-lines:
			if strings.TrimSpace(line) == "" {
				continue
			}
			if err := f.send(c.ctx, line); err != nil {
				c.failure(err)
			}
			c.catchUp(f)
		case <-ticker.C:
			c.catchUp(f)
		}
	}
}

// catchUp prints what arrived since the last read.
func (c *invocation) catchUp(f following) {
	page, err := f.read(c.ctx, api.TalkQuery{Limit: api.TalkMaxLimit})
	if err != nil {
		return
	}
	c.printNew(f, page.Entries)
}

func (c *invocation) printNew(f following, entries []api.TalkEntry) {
	for _, entry := range entries {
		if f.seen[entry.ID] {
			continue
		}
		f.seen[entry.ID] = true
		fmt.Fprint(c.stdout, talkLine(entry))
	}
}

// interactive refuses a talk command with no terminal or with --json, as attaching does.
func (c *invocation) interactive() int {
	if c.asJSON {
		return c.usageError("talking is interactive, and prints no JSON")
	}
	if c.terminal == nil {
		return c.usageError("talking needs a terminal: run this in one")
	}
	return ExitOK
}

// chat talks to the assistant: the newest conversation, the one named, or a new one. It prints the
// conversation's newest entries and each new one as it arrives, sends each line typed as a message, and
// stops the session when input ends.
func (c *invocation) chat(args []string) int {
	set := c.flags()
	id := set.String("conversation", "", "the conversation to open, the newest when left out")
	fresh := set.Bool("new", false, "start a new conversation")
	name := set.String("name", "", "the new conversation's name")
	worker := set.String("worker", "", "the worker to run the session on, by name")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if code := c.interactive(); code != ExitOK {
		return code
	}
	if *id != "" && *fresh {
		return c.usageError("--conversation and --new name different conversations")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	opened, err := c.openChat(client, *id, *fresh, *name, *worker)
	if err != nil {
		return c.failure(err)
	}
	conversation := opened.Conversation
	c.say("Talking to the assistant in %q (%s). Type a message and press Enter; end input to stop the session.\n", conversation.Name, conversation.ID)
	for _, stopped := range opened.Stopped {
		c.say("The running conversation %s stopped first.\n", stopped.ID)
	}
	err = c.followStream(following{
		read: func(ctx context.Context, q api.TalkQuery) (api.TalkPage, error) {
			return client.ConversationEntries(ctx, conversation.ID, q)
		},
		send: func(ctx context.Context, body string) error {
			message, err := newID()
			if err != nil {
				return err
			}
			_, err = client.SendToAssistant(ctx, conversation.ID, api.ConversationMessage{ID: message, Body: body, Worker: *worker})
			return err
		},
	})
	if err != nil {
		return c.failure(err)
	}
	if _, err := client.StopConversation(context.WithoutCancel(c.ctx), conversation.ID); err != nil {
		return c.failure(err)
	}
	c.say("Stopped the session of %s.\n", conversation.ID)
	return ExitOK
}

// openChat starts the conversation chat talks in.
func (c *invocation) openChat(client *api.ProjectClient, id string, fresh bool, name, worker string) (api.ConversationOpened, error) {
	if id == "" && !fresh {
		list, err := client.Conversations(c.ctx)
		if err != nil {
			return api.ConversationOpened{}, err
		}
		if len(list.Conversations) != 0 {
			id = list.Conversations[0].ID
		}
	}
	if id != "" {
		return client.OpenConversation(c.ctx, id, api.OpenConversation{Worker: worker})
	}
	created, err := newID()
	if err != nil {
		return api.ConversationOpened{}, err
	}
	return client.CreateConversation(c.ctx, api.CreateConversation{ID: created, Name: name, Worker: worker})
}

// chatList lists the calling member's conversations and the workers that could run one.
func (c *invocation) chatList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	list, err := client.Conversations(c.ctx)
	return c.finish(err, list, func() string {
		var out strings.Builder
		if len(list.Conversations) == 0 {
			out.WriteString("No conversations.\n")
		}
		for _, conversation := range list.Conversations {
			out.WriteString(conversationLine(conversation))
		}
		for _, worker := range list.Workers {
			if worker.Available {
				fmt.Fprintf(&out, "worker %s can run the assistant\n", worker.Name)
			} else {
				fmt.Fprintf(&out, "worker %s %s\n", worker.Name, worker.Reason)
			}
		}
		return out.String()
	})
}

// chatTargets lists what the calling member can talk to in this project.
func (c *invocation) chatTargets(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	targets, err := client.ConversationTargets(c.ctx)
	return c.finish(err, targets, func() string {
		var out strings.Builder
		if targets.Assistant.Conversation != nil {
			fmt.Fprintf(&out, "assistant: %s", conversationLine(*targets.Assistant.Conversation))
		} else {
			out.WriteString("assistant: no conversation yet\n")
		}
		for _, orchestrator := range targets.Orchestrators {
			fmt.Fprintf(&out, "orchestrator on %s: %s, generation %d, serves %s\n", orchestrator.Name, orchestrator.State, orchestrator.Generation, strings.Join(orchestrator.Projects, ", "))
		}
		for _, lane := range targets.Planning {
			fmt.Fprintf(&out, "planning session %s on %s: %s, %s\n", lane.Lane, lane.WorkOrder, lane.State, cmpOr(lane.RunMode, "run mode unknown"))
		}
		for _, lane := range targets.Lanes {
			fmt.Fprintf(&out, "lane %s on %s %q: %s, %s\n", lane.Lane, lane.WorkOrder, lane.Title, lane.State, cmpOr(lane.RunMode, "run mode unknown"))
		}
		return out.String()
	})
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// chatNew starts a new conversation.
func (c *invocation) chatNew(args []string) int {
	set := c.flags()
	name := set.String("name", "", "the conversation's name")
	worker := set.String("worker", "", "the worker to run the session on, by name")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	id, err := newID()
	if err != nil {
		return c.failure(err)
	}
	opened, err := client.CreateConversation(c.ctx, api.CreateConversation{ID: id, Name: *name, Worker: *worker})
	return c.finish(err, opened, func() string { return openedLines(opened) })
}

// chatOpen starts one conversation's session, resuming what it had.
func (c *invocation) chatOpen(args []string) int {
	set := c.flags()
	worker := set.String("worker", "", "the worker to run the session on, by name")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	opened, err := client.OpenConversation(c.ctx, id, api.OpenConversation{Worker: *worker})
	return c.finish(err, opened, func() string { return openedLines(opened) })
}

// chatShow prints one conversation's entries, oldest first.
func (c *invocation) chatShow(args []string) int {
	set := c.flags()
	limit := set.Int("limit", api.TalkDefaultLimit, "how many of the newest entries")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	page, err := client.ConversationEntries(c.ctx, id, api.TalkQuery{Limit: *limit})
	return c.finish(err, page, func() string {
		var out strings.Builder
		for _, entry := range page.Entries {
			out.WriteString(talkLine(entry))
		}
		return out.String()
	})
}

// chatChange is a command that changes one conversation and prints it.
func chatChange(change func(*api.ProjectClient, context.Context, string) (api.Conversation, error)) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		set := c.flags()
		positional, ok := c.parse(set, args)
		if !ok {
			return ExitUsage
		}
		id, ok := c.oneID(positional)
		if !ok {
			return ExitUsage
		}
		client, exit := c.projectClient()
		if client == nil {
			return exit
		}
		defer client.Close()
		record, err := change(client, c.ctx, id)
		return c.finish(err, record, func() string { return conversationLine(record) })
	}
}

// chatRename names a conversation.
func (c *invocation) chatRename(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 2 {
		return c.usageError("takes a conversation and its new name")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	record, err := client.RenameConversation(c.ctx, positional[0], positional[1])
	return c.finish(err, record, func() string { return conversationLine(record) })
}

// chatDelete stops a conversation and removes it with every message it holds. What its actions did, and
// the cards it prepared, stay in the audit.
func (c *invocation) chatDelete(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	err := client.DeleteConversation(c.ctx, id)
	result := map[string]any{"id": id, "deleted": true}
	return c.finish(err, result, func() string {
		return fmt.Sprintf("Deleted conversation %s. Its actions and cards stay in the audit.\n", id)
	})
}

// chatSend sends the assistant of a conversation a message, opening a stopped conversation first.
func (c *invocation) chatSend(args []string) int {
	set := c.flags()
	body := set.String("body", "", "what the message says")
	worker := set.String("worker", "", "the worker to run the session on, when the conversation opens")
	messageID := set.String("id", "", "a client-generated ID")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if *body == "" {
		return c.usageError("--body is required")
	}
	if *messageID == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		*messageID = generated
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	sent, err := client.SendToAssistant(c.ctx, id, api.ConversationMessage{ID: *messageID, Body: *body, Worker: *worker})
	return c.finish(err, sent, func() string {
		var out strings.Builder
		out.WriteString(talkLine(sent.Entry))
		for _, stopped := range sent.Stopped {
			fmt.Fprintf(&out, "The running conversation %s stopped first.\n", stopped.ID)
		}
		return out.String()
	})
}

// orchestratorAttach follows the stream of the orchestrator of a worker that serves this project, and
// sends each line typed as a message to it, which grants nothing.
func (c *invocation) orchestratorAttach(args []string) int {
	set := c.flags()
	worker := set.String("worker", "", "the worker whose orchestrator to talk to, by name or ID")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if code := c.interactive(); code != ExitOK {
		return code
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	targets, err := client.ConversationTargets(c.ctx)
	if err != nil {
		return c.failure(err)
	}
	found := slices.IndexFunc(targets.Orchestrators, func(o api.OrchestratorTarget) bool { return o.Worker == *worker || o.Name == *worker })
	switch {
	case *worker == "" && len(targets.Orchestrators) == 1:
		found = 0
	case *worker == "" && len(targets.Orchestrators) == 0:
		return c.refuse(ExitNotFound, "no worker serves this project, so no orchestrator does")
	case *worker == "":
		return c.usageError("more than one worker serves this project: name one with --worker")
	case found < 0:
		return c.refuse(ExitNotFound, "worker %s serves no repository of this project", *worker)
	}
	target := targets.Orchestrators[found]
	c.say("Following the orchestrator on %s (%s). Type a message and press Enter; end input to leave, which stops nothing.\n", target.Name, target.State)
	err = c.followStream(following{
		read: func(ctx context.Context, q api.TalkQuery) (api.TalkPage, error) {
			return client.OrchestratorStream(ctx, target.Worker, q)
		},
		send: func(ctx context.Context, body string) error {
			id, err := newID()
			if err != nil {
				return err
			}
			_, err = client.SendMessage(ctx, messages.Draft{ID: id, To: messages.To(messages.TargetOrchestrator, target.Worker, 0), Kind: messages.KindInstruction, Body: body})
			return err
		},
	})
	if err != nil {
		return c.failure(err)
	}
	return ExitOK
}

// headless says a lane runs with no pane: its stream is what a person follows (FR-CHAT-12).
func headless(client *api.ProjectClient, ctx context.Context, lane string) (bool, error) {
	targets, err := client.ConversationTargets(ctx)
	if err != nil {
		return false, err
	}
	for _, target := range slices.Concat(targets.Planning, targets.Lanes) {
		if target.Lane == lane {
			return target.RunMode == "headless", nil
		}
	}
	return false, nil
}

// followLane follows a headless lane's stream and sends each line typed as a message, which grants nothing.
func (c *invocation) followLane(client *api.ProjectClient, lane string) int {
	c.say("Following lane %s, which runs headless. Type a message and press Enter; end input to leave, which stops nothing.\n", lane)
	err := c.followStream(following{
		read: func(ctx context.Context, q api.TalkQuery) (api.TalkPage, error) { return client.LaneStream(ctx, lane, q) },
		send: func(ctx context.Context, body string) error {
			id, err := newID()
			if err != nil {
				return err
			}
			_, err = client.SendMessage(ctx, messages.Draft{ID: id, To: messages.Target{Lane: lane}, Kind: messages.KindInstruction, Body: body})
			return err
		},
	})
	if err != nil {
		return c.failure(err)
	}
	return ExitOK
}
