// Package hook is `ropeshift hook <event>`, which an agent CLI runs synchronously. A
// slow or failing hook would block or break the agent, so it always exits 0, does no
// work beyond one short request, and spools what the worker could not take. It reads
// only `ROPESHIFT_*`, because nothing else reaches every hook.
package hook

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/lanes"
)

const (
	// maxInput caps what a hook reads from stdin.
	maxInput = 4 << 20
	// dialDeadline and ackDeadline keep the worst case under NFR-PERF-2.
	dialDeadline = 15 * time.Millisecond
	ackDeadline  = 25 * time.Millisecond
	// hardDeadline ends a hook that passed everything else, below the 100 ms limit.
	hardDeadline = 90 * time.Millisecond
	// groundAck is how long a hook that re-grounds a session waits for its generation. A clear's
	// answer follows two journal commits, and a session that hears none starts with no generation,
	// so this takes most of a hook's time, and with dialDeadline it stays under hardDeadline.
	groundAck = 60 * time.Millisecond
	// peekDeadline bounds a hook's look at a gated call, inside hardDeadline.
	peekDeadline = 45 * time.Millisecond
	// approvalPoll is how often a waiting hook looks, and the two after it bound one look.
	approvalPoll   = 250 * time.Millisecond
	approvalDial   = time.Second
	approvalAnswer = 4 * time.Second
	// hookWaitGrace lets a worker say a wait ran out before a hook stops asking.
	hookWaitGrace = 2 * time.Second
	// unknownWait bounds a hook with no policy to read, which has no worker that set a limit.
	unknownWait = 5 * time.Second
	// callBytes sizes the name a hook gives one call.
	callBytes = 8
)

const (
	eventPreToolUse         = "PreToolUse"
	eventPostToolUse        = "PostToolUse"
	eventPostToolUseFailure = lanes.EventToolFailed
	eventPermissionDenied   = lanes.EventToolDenied
	eventPermissionRequest  = "PermissionRequest"
	eventSessionStart       = "SessionStart"
)

const unavailablePolicy = "the policy file cannot be read"

// config is one invocation, from its argument and its environment.
type config struct {
	event      string
	socket     string
	session    string
	spool      string
	token      string
	policy     string
	lane       string
	workOrder  string
	generation int64
	// out is what the hook took and has not handed on, which its deadline settles.
	out *outbox
}

func read(event string, getenv func(string) string) config {
	generation, _ := strconv.ParseInt(getenv(api.EnvGeneration), 10, 64)
	return config{
		event: event, socket: getenv(api.EnvWorkerSocket), spool: getenv(api.EnvSpool), token: getenv(api.EnvLaneToken),
		policy: getenv(api.EnvPolicy), lane: getenv(api.EnvLaneID), workOrder: getenv(api.EnvWorkOrder),
		session: getenv(lanes.EnvMessagingSocket), generation: generation,
	}
}

// Main runs one hook. It has no failure path: whatever happens, the process exits 0. The deadline
// starts once the hook has taken its event, because a hook ended before then has nothing to spool,
// and the agent CLI closes a hook's input as soon as it has written it.
func Main(args []string, stdin io.Reader, stdout io.Writer, getenv func(string) string) int {
	defer func() { _ = recover() }()
	if len(args) == 0 {
		return 0
	}
	cfg := read(args[0], getenv)
	cfg.out = new(outbox)
	event, input := take(cfg, stdin)
	if cfg.event != eventPermissionRequest {
		timer := time.AfterFunc(hardDeadline, func() { expire(cfg, stdout) })
		defer timer.Stop()
	}
	run(cfg, event, input, stdout)
	return 0
}

// expire ends a hook that passed its deadline. A policy hook fails closed. A loaded machine can hold
// a hook past its deadline with an event it took and has not handed on, and delivery is at least
// once, so that event reaches the spool first. The append is local, and the agent CLI's own limit on
// a hook bounds it.
func expire(cfg config, stdout io.Writer) {
	if cfg.event == eventPreToolUse && cfg.policy != "" {
		deny(stdout, "the policy check ran out of time")
	}
	cfg.out.settle(cfg)
	os.Exit(0)
}

// take reads a hook's input into the event it reports. Only a policy hook's verdict changes the event
// after this.
func take(cfg config, stdin io.Reader) (lanes.HookEvent, []byte) {
	input, _ := io.ReadAll(io.LimitReader(stdin, maxInput))
	event := lanes.NewHookEvent(cfg.event, input)
	event.Lane, event.Generation, event.AtMs = cfg.lane, cfg.generation, time.Now().UnixMilli()
	switch cfg.event {
	case eventSessionStart:
		event.Socket = cfg.session
	case eventPreToolUse, eventPostToolUse, eventPostToolUseFailure, eventPermissionDenied, eventPermissionRequest:
		nameCall(&event, input)
	}
	return event, input
}

// run hands on the event a hook took.
func run(cfg config, event lanes.HookEvent, input []byte, stdout io.Writer) {
	switch cfg.event {
	case eventPreToolUse:
		verdict := decide(cfg, input, stdout)
		if verdict != questionDeferred {
			event.Asked = nil
		}
		if verdict == callDenied {
			event.Detail = lanes.DetailDenied
		}
	case eventSessionStart:
		ack := ackDeadline
		if lanes.Regrounds(event.Detail) {
			ack = groundAck
		}
		reground(cfg, event, forwardWithin(cfg, event, ack), stdout)
		return
	case eventPermissionRequest:
		awaitDecision(cfg, event, input, stdout)
		return
	}
	forward(cfg, event)
}

// verdict is what a policy hook made of a call.
type verdict int

const (
	callPassed verdict = iota
	questionDeferred
	callDenied
)

// nameCall gives an event the digest of its call, which is all a hook forwards of its input outside
// a request. It ties a `PermissionRequest`, which names no call, to the start of the one it holds.
func nameCall(event *lanes.HookEvent, input []byte) {
	if call, err := readCall(input); err == nil {
		event.Digest = call.Digest
	}
}

// requestedCall is the part of a hook's input a request reads.
type requestedCall struct {
	Tool  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input"`
}

// readCall reads the digest of the call a hook is about, which is all a hook forwards of its input
// outside a request.
func readCall(input []byte) (authority.ToolCall, error) {
	var call requestedCall
	if err := json.Unmarshal(input, &call); err != nil {
		return authority.ToolCall{}, err
	}
	return authority.ReadToolCall(call.Tool, call.Input)
}

// awaitDecision is the one hook that waits (NFR-PERF-2). The worker reads the call and raises the
// request, and this looks until a decision comes or the wait runs out. A worker that goes away
// comes back to what its journal holds, so a look that fails is tried again, and only the hook's
// own bound ends the wait. Nothing here can allow a call the worker did not.
func awaitDecision(cfg config, event lanes.HookEvent, input []byte, stdout io.Writer) {
	var call requestedCall
	if json.Unmarshal(input, &call) != nil {
		answerPermission(stdout, lanes.ApprovalDeny, lanes.ReasonNoApproval)
		return
	}
	request := lanes.ApprovalRequest{Tool: call.Tool, Input: call.Input, Call: newCallID()}
	line := lanes.Request{Version: lanes.SocketProtocol, Token: cfg.token, Hook: &event, Approval: &request}
	deadline := time.Now().Add(waitOf(cfg) + hookWaitGrace)
	reason := lanes.ReasonNoApproval
	for time.Now().Before(deadline) {
		reply, err := api.CallLane(cfg.socket, line, approvalDial, approvalAnswer)
		if err == nil && !reply.OK && reply.Code != lanes.CodeUnavailable {
			answerPermission(stdout, lanes.ApprovalDeny, reply.Message)
			return
		}
		if err == nil && reply.OK && reply.Approval != nil {
			request.Raised, line.Hook = true, nil
			switch reply.Approval.Decision {
			case lanes.ApprovalAllow, lanes.ApprovalDeny:
				answerPermission(stdout, reply.Approval.Decision, reply.Approval.Reason)
				return
			}
			reason = lanes.ReasonApprovalWait
		}
		time.Sleep(approvalPoll)
	}
	if line.Hook != nil {
		spool(cfg, event)
	}
	answerPermission(stdout, lanes.ApprovalDeny, reason)
}

// waitOf reads a lane's bound from the file a denial reads. With no file, a hook gives up soon,
// because a lane with no policy has no worker that set one.
func waitOf(cfg config) time.Duration {
	var policy lanes.DenyPolicy
	raw, err := os.ReadFile(cfg.policy)
	if err == nil {
		err = json.Unmarshal(raw, &policy)
	}
	if err != nil || policy.ApprovalWaitMs <= 0 {
		return unknownWait
	}
	return time.Duration(policy.ApprovalWaitMs) * time.Millisecond
}

func newCallID() string {
	raw := make([]byte, callBytes)
	if _, err := rand.Read(raw); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(raw)
}

// answerPermission is a PermissionRequest hook's answer, which has a shape of its own.
func answerPermission(stdout io.Writer, decision, reason string) {
	behavior := map[string]string{"behavior": decision}
	if decision == lanes.ApprovalDeny {
		behavior["message"] = reason
	}
	write(stdout, map[string]any{"hookEventName": eventPermissionRequest, "decision": behavior})
}

// deferGated ends a turn on a call a person must approve, once a request stands for it, so a lane
// parks with no process waiting. A worker that does not answer in time leaves the call to
// PermissionRequest, which waits, so this can make a lane slower and never let a call through.
func deferGated(cfg config, input []byte, stdout io.Writer) verdict {
	var call requestedCall
	if cfg.socket == "" || json.Unmarshal(input, &call) != nil {
		return callPassed
	}
	request := lanes.Request{
		Version: lanes.SocketProtocol, Token: cfg.token,
		Approval: &lanes.ApprovalRequest{Tool: call.Tool, Input: call.Input, Call: newCallID(), Peek: true},
	}
	reply, err := api.CallLane(cfg.socket, request, dialDeadline, peekDeadline)
	if err != nil || !reply.OK || reply.Approval == nil {
		return callPassed
	}
	switch reply.Approval.Decision {
	case lanes.ApprovalWaiting:
		write(stdout, map[string]string{"hookEventName": eventPreToolUse, "permissionDecision": "defer"})
	case lanes.ApprovalDeny:
		deny(stdout, reply.Approval.Reason)
		return callDenied
	}
	return callPassed
}

// decide comes first, so a denial never depends on the worker. Only a deferred question tool forwards
// tool input, and a denied call is one no later hook reports.
func decide(cfg config, input []byte, stdout io.Writer) verdict {
	if cfg.policy == "" {
		return callPassed
	}
	raw, err := os.ReadFile(cfg.policy)
	if err != nil {
		deny(stdout, unavailablePolicy)
		return callDenied
	}
	var policy lanes.DenyPolicy
	if json.Unmarshal(raw, &policy) != nil {
		deny(stdout, unavailablePolicy)
		return callDenied
	}
	var call struct {
		Tool  string `json:"tool_name"`
		Cwd   string `json:"cwd"`
		Input struct {
			Command  string `json:"command"`
			File     string `json:"file_path"`
			Notebook string `json:"notebook_path"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(input, &call) != nil {
		deny(stdout, unavailablePolicy)
		return callDenied
	}
	if reason, held := policy.HoldsTool(call.Tool); held {
		deny(stdout, reason)
		return callDenied
	}
	if call.Tool == lanes.ToolQuestion {
		switch policy.QuestionTool {
		case lanes.QuestionToolDefer:
			write(stdout, map[string]string{"hookEventName": eventPreToolUse, "permissionDecision": "defer"})
			return questionDeferred
		case lanes.QuestionToolDeny:
			deny(stdout, lanes.ReasonUseAsk)
			return callDenied
		}
	}
	target := call.Input.File
	if target == "" {
		target = call.Input.Notebook
	}
	tool := lanes.ToolCall{Tool: call.Tool, Command: call.Input.Command, Path: target, Cwd: call.Cwd}
	reason, denied := policy.Decide(tool)
	if denied {
		deny(stdout, reason)
		return callDenied
	}
	if policy.Gated(tool) {
		return deferGated(cfg, input, stdout)
	}
	return callPassed
}

func deny(stdout io.Writer, reason string) {
	write(stdout, map[string]string{
		"hookEventName": eventPreToolUse, "permissionDecision": "deny", "permissionDecisionReason": reason,
	})
}

// reground tells a session that lost its place who it is. SessionStart is the one event that can
// add context after a compaction. Its work comes from the file a denial reads, so this needs no
// worker. Its generation comes from the worker, because the tags of a process that was cleared are
// out of date, and with no worker a clear names none rather than a wrong one.
func reground(cfg config, event lanes.HookEvent, generation int64, stdout io.Writer) {
	if !lanes.Regrounds(event.Detail) {
		return
	}
	if generation == 0 && event.Detail != lanes.SourceClear {
		generation = cfg.generation
	}
	var policy lanes.DenyPolicy
	if raw, err := os.ReadFile(cfg.policy); err == nil {
		_ = json.Unmarshal(raw, &policy)
	}
	write(stdout, map[string]string{
		"hookEventName":     eventSessionStart,
		"additionalContext": policy.Ground.Text(cfg.lane, cfg.workOrder, generation),
	})
}

func write(stdout io.Writer, specific any) {
	line, err := json.Marshal(map[string]any{"hookSpecificOutput": specific})
	if err == nil {
		_, _ = stdout.Write(line)
	}
}

// forward sends one short request, and says which generation the worker has the lane in. A refusal
// is an answer, so it spools nothing, and a hook that missed its ack spools an event the worker may
// have, which is safe because the worker merges by meaning.
func forward(cfg config, event lanes.HookEvent) int64 {
	return forwardWithin(cfg, event, ackDeadline)
}

func forwardWithin(cfg config, event lanes.HookEvent, ack time.Duration) int64 {
	if cfg.out == nil {
		cfg.out = new(outbox)
	}
	cfg.out.hold(&event)
	if cfg.socket != "" {
		request := lanes.Request{Version: lanes.SocketProtocol, Token: cfg.token, Hook: &event}
		if reply, err := api.CallLane(cfg.socket, request, dialDeadline, ack); err == nil {
			cfg.out.hold(nil)
			return reply.Generation
		}
	}
	cfg.out.flush(cfg)
	return 0
}

// outbox holds the event a hook is handing on until the worker acks it or the spool has it, so the
// deadline can tell an event still to write from one already down.
type outbox struct {
	mu    sync.Mutex
	event *lanes.HookEvent
}

// hold keeps an event, and nil lets the one it kept go.
func (o *outbox) hold(event *lanes.HookEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.event = event
}

// flush spools the event the outbox holds, unless the deadline spooled it first.
func (o *outbox) flush(cfg config) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.spoolHeld(cfg)
}

// settle is the deadline's flush. It waits out a write in progress and keeps the outbox locked, so no
// write starts while the process exits.
func (o *outbox) settle(cfg config) {
	o.mu.Lock()
	o.spoolHeld(cfg)
}

func (o *outbox) spoolHeld(cfg config) {
	if o.event != nil {
		spool(cfg, *o.event)
		o.event = nil
	}
}

// spool appends one line with one write, so parallel hooks cannot interleave. The line holds no
// token, because only the session's environment does, and it carries a seal from that token, because
// a worker takes nothing from a file on its word. A hook with no token has nothing to seal with.
func spool(cfg config, event lanes.HookEvent) {
	if cfg.spool == "" {
		return
	}
	line, err := lanes.SealHook(cfg.token, event)
	if err != nil {
		return
	}
	file, err := os.OpenFile(cfg.spool, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(line, '\n'))
}
