package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// DrainingSuffix names a spool the worker has taken aside. A hook that appends during
// a drain starts a new spool for the next start.
const DrainingSuffix = ".draining"

// laneCallDeadline bounds one line. A hook gives up long before this.
const laneCallDeadline = 5 * time.Second

// maxLaneLine bounds one line on the socket.
const maxLaneLine = 1 << 20

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
	switch {
	case request.Hook != nil:
		event := *request.Hook
		event.Lane, event.Generation = lane.ID, lane.Generation
		if err := w.journal.MergeHook(ctx, event); err != nil {
			return refused(lanes.CodeUnavailable, err.Error())
		}
		if run := w.runOf(lane.ID); run != nil {
			run.observeHook(ctx, event)
		}
		return lanes.Reply{OK: true}
	case request.Report != nil:
		reply := w.takeReport(ctx, lane, *request.Report)
		if run := w.runOf(lane.ID); run != nil {
			run.noteLedger(reachOfCall(reply))
		}
		return reply
	}
	return refused(lanes.CodeInvalid, "this line asks for nothing")
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
	raw, err := json.Marshal(report)
	if err != nil {
		return refused(lanes.CodeInvalid, err.Error())
	}
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

// DrainSpool takes what hooks spooled while the worker was down. A crash during a
// drain leaves the file aside, and the next start finishes it. Delivery is at-least-
// once, and a repeat merges by meaning, so reading a line twice is safe.
func (w *Worker) DrainSpool(ctx context.Context) error {
	path := w.config.Spool
	if path == "" {
		return nil
	}
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

func (w *Worker) drainFile(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		var event lanes.HookEvent
		if json.Unmarshal(line, &event) != nil || event.Event == "" {
			continue
		}
		if err := w.journal.MergeHook(ctx, event); err != nil {
			return err
		}
	}
	return os.Remove(path)
}
