package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// DrainingSuffix names a spool the worker has taken aside. A hook that appends during
// a drain starts a new spool for the next start.
const DrainingSuffix = ".draining"

// laneCallDeadline bounds one line. A hook gives up long before this.
const laneCallDeadline = 5 * time.Second

// maxLaneLine bounds one line on the socket.
const maxLaneLine = 1 << 20

// askDeadline bounds one ask, under the deadline of the line that carries it.
const askDeadline = 3 * time.Second

// questionIDBytes sizes the ID a worker gives a question.
const questionIDBytes = 16

// ServeLanes answers the worker socket until the listener closes. Hooks and
// `ropeshift mcp` reach the worker here, and each connection is one line and one
// answer. It stays open while the control plane is away, so a lane can still report.
func (w *Worker) ServeLanes(ctx context.Context, listener net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go w.serveLane(ctx, conn)
	}
}

func (w *Worker) serveLane(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if conn.SetDeadline(time.Now().Add(laneCallDeadline)) != nil {
		return
	}
	line, err := bufio.NewReaderSize(conn, 1<<16).ReadSlice('\n')
	if err != nil && len(line) == 0 {
		return
	}
	reply, err := json.Marshal(w.AnswerLane(ctx, line))
	if err == nil {
		_, _ = conn.Write(append(reply, '\n'))
	}
}

func refused(code, message string) lanes.Reply {
	return lanes.Reply{Code: code, Message: message}
}

// replyAlone says a request is exactly a reply, the one lane request an orchestrator's token makes: it
// carries no report, wait, or ask beside it.
func replyAlone(request lanes.Request) bool {
	return request.Reply != nil && request.Report == nil && request.Wait == nil && request.Ask == nil
}

// AnswerLane answers one line of the worker socket. It checks the token first, so a
// process that holds no token learns nothing. The token says which lane is calling, and
// whatever the line claims about its lane or generation is ignored: the worker resolves
// those on every call (ADR-0016).
func (w *Worker) AnswerLane(ctx context.Context, line []byte) lanes.Reply {
	if len(line) > maxLaneLine {
		return refused(lanes.CodeInvalid, "the line is too long")
	}
	var request lanes.Request
	if err := json.Unmarshal(line, &request); err != nil || request.Version != lanes.SocketProtocol {
		return refused(lanes.CodeInvalid, "this is not a line of the lane protocol")
	}
	lane, err := w.journal.LaneByToken(ctx, lanes.TokenDigest(request.Token))
	if errors.Is(err, ports.ErrNotFound) {
		return refused(lanes.CodeBadToken, "this token is not a running lane's")
	}
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	if lane.Owner == lanes.OwnerOrchestrator && request.LaneOnly() && !replyAlone(request) {
		return refused(lanes.CodeForbidden, "an orchestrator's token cannot do this")
	}
	if lane.Owner == ownerAssistant {
		switch {
		case request.LaneOnly(), request.Decide != nil, request.Work != nil, len(request.Handoff) != 0:
			return refused(lanes.CodeForbidden, "an assistant's token cannot do this")
		case request.Approval != nil:
			return assistantApproval()
		}
	}
	switch {
	case request.Who:
		return lanes.Reply{OK: true, Owner: lane.Owner}
	case request.Approval != nil:
		if request.Hook != nil {
			event := *request.Hook
			event.Lane, event.Generation = lane.ID, lane.Generation
			if err := w.journal.MergeHook(ctx, event); err != nil {
				return refused(lanes.CodeUnavailable, err.Error())
			}
			w.deliverHook(ctx, event)
		}
		return w.takeApproval(ctx, lane, *request.Approval)
	case request.Decide != nil:
		return w.takeDecision(ctx, lane, *request.Decide)
	case request.Work != nil:
		return w.takeWork(ctx, lane, *request.Work)
	case request.Hook != nil:
		event := *request.Hook
		event.Lane, event.Generation = lane.ID, lane.Generation
		if err := w.journal.MergeHook(ctx, event); err != nil {
			return refused(lanes.CodeUnavailable, err.Error())
		}
		w.deliverHook(ctx, event)
		w.takeAdmission(ctx, event)
		reply := lanes.Reply{OK: true, Generation: lane.Generation}
		if run := w.runOf(lane.ID); run != nil {
			reply.Generation = run.currentGeneration()
		}
		return reply
	case request.Reply != nil:
		return w.takeReply(ctx, lane, *request.Reply)
	case request.Report != nil:
		reply := w.takeReport(ctx, lane, *request.Report)
		if run := w.runOf(lane.ID); run != nil {
			run.noteLedger(reachOfCall(reply))
		}
		return reply
	case request.Ask != nil:
		return w.takeAsk(ctx, lane, *request.Ask)
	case request.Context != nil:
		return w.takeContext(ctx, lane, *request.Context)
	case len(request.Handoff) != 0:
		return w.takeHandoff(ctx, lane, request.Handoff)
	case request.Wait != nil:
		if request.Wait.Minutes <= 0 {
			return refused(lanes.CodeInvalid, "a wait needs a number of minutes")
		}
		if run := w.runOf(lane.ID); run != nil {
			run.declareWait(*request.Wait)
			run.noteLedger(lanes.Reachable)
		}
		return lanes.Reply{OK: true}
	}
	return refused(lanes.CodeInvalid, "this line asks for nothing")
}

// deliverHook hands one event to the run that holds its lane, if a run does. An event of an
// earlier generation says nothing about the session this run follows. A driver knows a session by
// its process, which a clear does not change.
func (w *Worker) deliverHook(ctx context.Context, event lanes.HookEvent) {
	if run := w.runOf(event.Lane); run != nil && run.currentGeneration() == event.Generation {
		run.observeHook(ctx, event)
		event.Generation = run.processGeneration()
		run.rt.Driver.TakeHook(ctx, event)
		if len(event.Asked) != 0 {
			go w.convertAsked(ctx, run.record(), event.Asked, run.askCategories())
		}
	}
}

// askCategories are the categories a converted question carries. A planning lane's question goes
// straight to the person, and the control plane takes it only with a category a member can find it
// by, which the agent's own tool call has none of. Every other lane's climbs a ladder that names its
// own.
func (r *laneRun) askCategories() []string {
	if r.planning() {
		return []string{questions.CategoryProduct}
	}
	return nil
}

// convertAsked records what a deferred question tool call asked. It runs after the hook's
// answer, because a hook has milliseconds and this reaches the control plane. A repeat
// attaches to what the first wrote, and a call that failed fires its hook again on resume.
func (w *Worker) convertAsked(ctx context.Context, lane lanes.Record, asked []lanes.AskedQuestion, categories []string) {
	for _, question := range asked {
		reply := w.takeAsk(ctx, lane, lanes.AskRequest{Text: question.Text, Options: question.Options, Categories: categories, Blocking: true})
		if !reply.OK {
			w.config.Report(fmt.Errorf("lane %s: the question tool's question stayed unrecorded: %s", lane.ID, reply.Message))
		}
	}
}

// takeAsk carries one question to the control plane and answers at once. The bound is the
// rule that no call stays open: with the control plane away, a lane learns that and goes on.
func (w *Worker) takeAsk(ctx context.Context, lane lanes.Record, request lanes.AskRequest) lanes.Reply {
	id, err := newToken(questionIDBytes)
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	bounded, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()
	result, err := w.link.Ask(bounded, questions.LaneAsk{
		Auth: w.Auth(), Epoch: w.epoch(), Project: lane.Project, WorkOrder: lane.WorkOrder, Fence: lane.Fence,
		Ask: questions.Ask{
			ID: id, Text: request.Text, Categories: request.Categories, Blocking: request.Blocking,
			Options: request.Options, Lane: lane.ID, Generation: lane.Generation,
		},
	})
	var fenced *workorders.FencedError
	switch {
	case err == nil:
		if run := w.runOf(lane.ID); run != nil {
			run.noteLedger(lanes.Reachable)
			if request.Blocking {
				run.noteAsked(ctx, result)
			}
		}
		return lanes.Reply{OK: true, Ask: &result}
	case errors.Is(err, questions.ErrInvalid):
		return refused(lanes.CodeInvalid, err.Error())
	case errors.As(err, &fenced):
		return refused(lanes.CodeFenced, err.Error())
	}
	if run := w.runOf(lane.ID); run != nil {
		run.noteLedger(lanes.Impaired)
	}
	return refused(lanes.CodeUnavailable, err.Error())
}

func (w *Worker) runOf(lane string) *laneRun {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running[lane]
}

// reachOfCall reads one `ropeshift mcp` call. A refusal of what it said still shows the
// path works, and only a store that failed under it is impaired.
func reachOfCall(reply lanes.Reply) lanes.Reachability {
	if reply.Code == lanes.CodeUnavailable {
		return lanes.Impaired
	}
	return lanes.Reachable
}

// takeReport keeps a lane's first outcome. A retry of the same report is answered as
// a success, because the first answer may have been lost, and a different one stands
// refused, because an outcome is typed and terminal.
func (w *Worker) takeReport(ctx context.Context, lane lanes.Record, report workorders.Report) lanes.Reply {
	if err := report.Validate(); err != nil {
		return refused(lanes.CodeInvalid, err.Error())
	}
	if run := w.runOf(lane.ID); run != nil {
		if err := run.holdsToItsWork(report); err != nil {
			return refused(lanes.CodeInvalid, err.Error())
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return refused(lanes.CodeInvalid, err.Error())
	}
	w.drainBeforeActing(ctx)
	first, err := w.journal.SaveReport(ctx, lane.ID, raw)
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	if first {
		return lanes.Reply{OK: true}
	}
	current, err := w.journal.Lane(ctx, lane.ID)
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	if bytes.Equal(current.Report, raw) {
		return lanes.Reply{OK: true}
	}
	return refused(lanes.CodeAlreadyReported, "this lane already reported its outcome")
}

// DrainSpool takes what hooks spooled. Most of it was written while the worker was down,
// and a hook that missed its ack under load spools with the worker alive, which a pane
// with no stream would otherwise never tell again, so a live worker drains on every tick.
// A crash during a drain leaves the file aside, and the next start finishes it. Delivery
// is at-least-once, and a repeat merges by meaning, so reading a line twice is safe. A
// spool is a file anything this user runs can write, so a line counts only if its seal
// comes from the token of the lane it names, as a call on the socket does.
//
// Passes take turns. One that comes while another is partway through the file waits for it, so it returns
// with every line the spool held when it came applied, and none removes a file another is reading. An empty
// spool costs a failed open and a failed rename.
func (w *Worker) DrainSpool(ctx context.Context) error {
	path := w.config.Spool
	if path == "" {
		return nil
	}
	w.spoolLock.Lock()
	defer w.spoolLock.Unlock()
	draining := path + DrainingSuffix
	if err := w.drainFile(ctx, draining); err != nil {
		return err
	}
	if err := os.Rename(path, draining); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return w.drainFile(ctx, draining)
}

// drainBeforeActing takes the spool ahead of a decision about a lane. A hook waits milliseconds for its ack and
// spools its event when none comes, and the next hook, report, or end of turn then reaches the worker ahead of
// the pass that takes the spool. A clear whose hook spooled reads as no clear until then: the turn it ends draws
// a nudge, the cleared session is never briefed, a report is kept ahead of the clear, and the session's end
// ends the token its line needs.
//
// spoolLock is the outermost lock. Its callers are a run's goroutine between two stream events, a socket
// handler, and the worker's pass, and none holds another lock here. A drain writes the journal, holds a run's or
// the worker's mutex briefly, and starts goroutines it does not wait for, so it waits on nothing its callers
// hold. It delivers hooks, which a driver turns into stream events for the run's goroutine to read once the
// drain has returned, so it never asks for the lock it holds.
func (w *Worker) drainBeforeActing(ctx context.Context) {
	if err := w.DrainSpool(ctx); err != nil && ctx.Err() == nil {
		w.config.Report(err)
	}
}

func (w *Worker) drainFile(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dropped := 0
	digestOf := func(lane string) (string, error) { return w.tokenDigestOf(ctx, lane) }
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		event, err := lanes.OpenHook(line, digestOf)
		if errors.Is(err, lanes.ErrSpoolLine) {
			dropped++
			continue
		}
		if err != nil {
			return err
		}
		event = w.ofItsGeneration(ctx, event)
		if err := w.journal.MergeHook(ctx, event); err != nil {
			return err
		}
		w.deliverHook(ctx, event)
		w.takeAdmission(ctx, event)
	}
	if dropped != 0 {
		w.config.Report(fmt.Errorf("the spool held %d lines that prove no lane, and they count for nothing", dropped))
	}
	return os.Remove(path)
}

// tokenDigestOf reads what a line's seal must come from. A lane this journal does not hold has
// none, and neither does one whose process ended, because its token ended with it (ADR-0016).
func (w *Worker) tokenDigestOf(ctx context.Context, lane string) (string, error) {
	record, err := w.journal.Lane(ctx, lane)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return record.TokenDigest, err
}

// ofItsGeneration reads a spooled line against the process it came from. Its tags are the ones that
// process started with, and a seal proves the lane, which is all a token could say of a generation
// after a clear. A generation this journal does not hold stays as it reads.
func (w *Worker) ofItsGeneration(ctx context.Context, event lanes.HookEvent) lanes.HookEvent {
	record, err := w.journal.Lane(ctx, event.Lane)
	if err == nil && event.Generation == record.ProcessGeneration() {
		event.Generation = record.Generation
	}
	return event
}
