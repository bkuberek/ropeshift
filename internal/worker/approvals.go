package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const (
	// defaultApprovalWait is what a profile that sets nothing it can read gets.
	defaultApprovalWait = 5 * time.Minute
	// lookDeadline bounds one read of the ledger for a hook that waits, and peekDeadline one for a
	// hook inside its budget, which goes on without it.
	lookDeadline = 2 * time.Second
	peekDeadline = 30 * time.Millisecond
	// maxLinks ends a walk through links that never arrives, past what a kernel would follow.
	maxLinks = 64
)

// What a closed request says of how a person answered at a terminal.
const (
	noteToolRan = "the tool ran with no decision on record"
	noteNoTool  = "the turn ended with no tool run"
)

func answer(decision, reason, id string) lanes.Reply {
	return lanes.Reply{OK: true, Approval: &lanes.ApprovalReply{Decision: decision, Reason: reason, ID: id}}
}

// takeApproval answers one look at a call. The token said which lane is calling, so everything a
// request records comes from the worker's own record and the call's own input: a lane names neither
// its principal nor what a reviewer reads, and a line that claims to is ignored.
func (w *Worker) takeApproval(ctx context.Context, lane lanes.Record, request lanes.ApprovalRequest) lanes.Reply {
	run := w.runOf(lane.ID)
	if run == nil {
		return refused(lanes.CodeUnavailable, "this lane has no run on this worker")
	}
	call, err := authority.ReadToolCall(request.Tool, request.Input)
	if err != nil {
		return refused(lanes.CodeInvalid, err.Error())
	}
	return run.lookAt(ctx, lane, call.Lands(insideWorktree(lane.Worktree, call.Path)), request)
}

// insideWorktree reads where a call lands, which is what a decision binds to. This runs on every
// look, so what a decision covered is read again at its use, and a link moved after a request was
// raised makes it another call. A path a class may cover comes back relative to the worktree, under
// the one name its file has. Anything else comes back absolute, which no class covers: a landing
// outside the worktree, a file with a second name no class can see, a path that cannot be walked, and
// a climb, because a tool reads `..` off the text and this reads it off the disk.
func insideWorktree(worktree, target string) string {
	if target == "" || worktree == "" {
		return target
	}
	absolute := target
	if !filepath.IsAbs(absolute) {
		absolute = worktree + string(filepath.Separator) + target
	}
	root, rootWalked := walkPath(worktree)
	landing, walked := walkPath(absolute)
	if !rootWalked || !walked {
		return filepath.Clean(absolute)
	}
	relative, err := filepath.Rel(root, landing)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || climbs(target) || namedTwice(landing) {
		return landing
	}
	return filepath.ToSlash(relative)
}

func climbs(target string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(target), "/"), "..")
}

// namedTwice says a file has another name, which can lie anywhere on its volume.
func namedTwice(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

// walkPath follows a path one part at a time, the way the kernel does. A link to nothing yet is
// followed too, because a write creates its file through the link, and the parts past what exists
// are kept as they read, because a file a call is about to write does not exist yet. It says false
// of a path it cannot follow to its end.
func walkPath(path string) (string, bool) {
	done, rest := string(filepath.Separator), strings.Split(filepath.ToSlash(path), "/")
	for links := 0; len(rest) != 0; {
		part := rest[0]
		rest = rest[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			done = filepath.Dir(done)
			continue
		}
		next := filepath.Join(done, part)
		info, err := os.Lstat(next)
		switch {
		case errors.Is(err, os.ErrNotExist):
			done = next
			continue
		case err != nil:
			return "", false
		case info.Mode()&os.ModeSymlink == 0:
			done = next
			continue
		}
		if links++; links > maxLinks {
			return "", false
		}
		link, err := os.Readlink(next)
		if err != nil {
			return "", false
		}
		if filepath.IsAbs(link) {
			done = string(filepath.Separator)
		}
		rest = append(strings.Split(filepath.ToSlash(link), "/"), rest...)
	}
	return done, true
}

// approvalWait reads a profile's limit, the way askWait does.
func (s *laneState) approvalWait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvalLimit <= 0 {
		return defaultApprovalWait
	}
	return s.approvalLimit
}

func (s *laneState) noteApprovals(open int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals = open
}

func (s *laneState) approvalsOpen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvals
}

// principal names who this run's session is, in a request.
func (r *laneRun) principal(lane lanes.Record) ledger.Actor {
	if lane.Owner == lanes.OwnerOrchestrator {
		return ledger.Actor{Kind: ledger.ActorOrchestrator, ID: r.w.Auth().WorkerID}
	}
	return ledger.Actor{Kind: ledger.ActorLane, ID: lane.ID}
}

// lookAt reads where a call stands. The ledger comes first, because a decision can reach a call that
// no request on this machine stands for: a lane that resumed elsewhere, or a retry after a denial.
func (r *laneRun) lookAt(ctx context.Context, lane lanes.Record, call authority.ToolCall, request lanes.ApprovalRequest) lanes.Reply {
	found, err := r.askLedger(ctx, lane, call, request)
	var fenced *workorders.FencedError
	if errors.As(err, &fenced) {
		return refused(lanes.CodeFenced, err.Error())
	}
	if err != nil && request.Peek {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	if err == nil {
		switch found.State {
		case authority.ApprovalApproved:
			return answer(lanes.ApprovalGoesOn, "", found.ID)
		case authority.ApprovalUsed, authority.ApprovalVoid, authority.ApprovalExpired, authority.ApprovalAnswered:
			if reply, ended := r.closedRequest(ctx, lane, call, found, request); ended {
				return reply
			}
		case authority.ApprovalDenied:
			r.closeCall(ctx, lane.ID, call.Digest)
			return answer(lanes.ApprovalDeny, deniedReason(found.Reason), found.ID)
		case "":
			r.sendRequests()
		}
	}
	return r.keepWaiting(ctx, lane, call, request.Raised)
}

// sendRequests hands the ledger a request it has not heard of, on a goroutine of its own, because a
// hook has no time to wait on it. A request a kill left in the journal goes the same way, or a
// member would wait for the next heartbeat to read it. One look at a time does this.
func (r *laneRun) sendRequests() {
	if !r.sending.CompareAndSwap(false, true) {
		return
	}
	r.sequence.Go(func() {
		defer r.sending.Store(false)
		if r.life != nil {
			r.uploadBounded(r.life)
		}
	})
}

// closedRequest reads a request that ended. A decision spent on this call lets it run. Any other end
// tells a hook already waiting on it to stop, and lets a new call raise a request of its own.
func (r *laneRun) closedRequest(ctx context.Context, lane lanes.Record, call authority.ToolCall, found authority.ApprovalAnswer, request lanes.ApprovalRequest) (lanes.Reply, bool) {
	r.closeCall(ctx, lane.ID, call.Digest)
	if found.Allowed {
		return answer(lanes.ApprovalAllow, "", found.ID), true
	}
	if request.Raised {
		return answer(lanes.ApprovalDeny, lanes.ReasonRequestClosed, found.ID), true
	}
	return lanes.Reply{}, false
}

func deniedReason(reason string) string {
	if reason == "" {
		return "ropeshift policy: a member denied this call."
	}
	return "ropeshift policy: a member denied this call: " + reason
}

// askLedger is one read of where a call stands. A hook that looks before a turn ends has little time.
func (r *laneRun) askLedger(ctx context.Context, lane lanes.Record, call authority.ToolCall, request lanes.ApprovalRequest) (authority.ApprovalAnswer, error) {
	limit := lookDeadline
	if request.Peek {
		limit = peekDeadline
	}
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return r.w.link.UseApproval(bounded, authority.ApprovalUse{
		Auth: r.w.Auth(), Epoch: r.w.epoch(), WorkOrder: lane.WorkOrder, Fence: lane.Fence, Lane: lane.ID, Generation: lane.Generation,
		Subject: call.Tool, Digest: call.Digest, Call: request.Call, Peek: request.Peek,
	})
}

// keepWaiting keeps a request on record and says how much longer a call waits. Past its bound a hook
// denies, and the lane is on its way to parking, so the request stands for when it comes back.
func (r *laneRun) keepWaiting(ctx context.Context, lane lanes.Record, call authority.ToolCall, raised bool) lanes.Reply {
	open, err := r.standingRequest(ctx, lane, call, raised)
	if errors.Is(err, errRequestGone) {
		return answer(lanes.ApprovalDeny, lanes.ReasonRequestClosed, "")
	}
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	left := time.UnixMilli(open.DeadlineMs).Sub(r.w.clock.Now())
	if left <= 0 {
		return answer(lanes.ApprovalDeny, lanes.ReasonApprovalWait, open.ID)
	}
	reply := answer(lanes.ApprovalWaiting, "", open.ID)
	reply.Approval.WaitMs = left.Milliseconds()
	return reply
}

// errRequestGone says a hook that was already waiting has no request left, because the call it held
// was answered at a terminal and the ledger has not heard yet. Only a new call raises one.
var errRequestGone = errors.New("this call has no request left")

// standingRequest finds what this lane asked of this call, or raises it. The row and the event that
// carries it are one journal write, so a kill leaves a request the ledger hears of or none at all. A
// generation that goes on from the one that asked waits for itself.
func (r *laneRun) standingRequest(ctx context.Context, lane lanes.Record, call authority.ToolCall, raised bool) (lanes.OpenApproval, error) {
	rows, err := r.w.journal.OpenApprovals(ctx, lane.ID)
	if err != nil {
		return lanes.OpenApproval{}, err
	}
	deadline := r.w.clock.Now().Add(r.state.approvalWait()).UnixMilli()
	for _, row := range rows {
		if row.Digest != call.Digest {
			continue
		}
		if row.Generation != lane.Generation {
			row.Generation, row.DeadlineMs = lane.Generation, deadline
			if err := r.w.journal.RenewApproval(ctx, row.ID, row.Generation, row.DeadlineMs); err != nil {
				return lanes.OpenApproval{}, err
			}
			r.noteRequest(ctx, len(rows))
		}
		return row, nil
	}
	if raised {
		return lanes.OpenApproval{}, errRequestGone
	}
	id, err := newToken(questionIDBytes)
	if err != nil {
		return lanes.OpenApproval{}, err
	}
	open := lanes.OpenApproval{ID: id, Lane: lane.ID, Generation: lane.Generation, Tool: call.Tool, Digest: call.Digest, Seen: call.Seen, DeadlineMs: deadline}
	payload, err := json.Marshal(authority.Approval{
		ID: id, Lane: lane.ID, WorkOrder: lane.WorkOrder, Generation: lane.Generation, Kind: authority.KindToolCall, Subject: call.Tool,
		SubjectDigest: call.Digest, Input: call.Input, Command: call.Command, Path: call.Path, Summary: call.Summary,
		RequestedBy: authority.Principal(r.principal(lane)),
	})
	if err != nil {
		return lanes.OpenApproval{}, err
	}
	if _, err := r.keepRequest(ctx, open, lane, payload); err != nil {
		return lanes.OpenApproval{}, err
	}
	crashpoint.Hit("worker.approval", "kept")
	r.noteRequest(ctx, len(rows)+1)
	r.sendRequests()
	return open, nil
}

// keepRequest is one journal write, because a request a lane parked on and the ledger never heard of
// would leave nothing a member could decide.
func (r *laneRun) keepRequest(ctx context.Context, open lanes.OpenApproval, lane lanes.Record, payload []byte) (bool, error) {
	event := r.w.event(ledger.UploadApprovalRaised, lane, payload)
	if !splitsApproval() {
		return r.w.journal.BeginApproval(ctx, open, event)
	}
	if _, err := r.w.journal.BeginApproval(ctx, open, r.w.event(ledger.UploadLaneStatus, lane, nil)); err != nil {
		return false, err
	}
	crashpoint.Hit("worker.approval", "split")
	_, err := r.w.journal.Append(ctx, event)
	return true, err
}

func (r *laneRun) lifeOr(ctx context.Context) context.Context {
	if r.life != nil {
		return r.life
	}
	return ctx
}

// noteRequest moves a lane to `needs_decision` and starts the wait that bounds it.
func (r *laneRun) noteRequest(ctx context.Context, open int) {
	r.state.noteApprovals(open)
	r.signal(ctx, lanes.OnDecisionPending)
	r.awaitApproval(ctx)
}

// closeCall stops following what a lane asked of one call, once it has an answer.
func (r *laneRun) closeCall(ctx context.Context, lane, digest string) {
	rows, err := r.w.journal.OpenApprovals(ctx, lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", lane, err))
		return
	}
	for _, row := range rows {
		if row.Digest != digest {
			continue
		}
		if err := r.w.journal.CloseApproval(ctx, row.ID); err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", lane, err))
			return
		}
		crashpoint.Hit("worker.approval", "closed")
		r.decisionsLeft(ctx, len(rows)-1)
	}
}

// decisionsLeft moves a lane on once nothing it asked is open.
func (r *laneRun) decisionsLeft(ctx context.Context, open int) {
	r.state.noteApprovals(open)
	if open == 0 {
		r.signal(ctx, lanes.OnDecisionReturned)
	}
}

// awaitApproval holds a lane on what it asked for `approval_wait`, then parks it, because no process
// waits on a person past its bound. One goroutine follows a lane, and it reads the journal, so a new
// worker goes on from the deadline the last one set.
func (r *laneRun) awaitApproval(ctx context.Context) {
	if !r.approving.CompareAndSwap(false, true) {
		return
	}
	r.sequence.Go(func() {
		defer r.approving.Store(false)
		life := r.lifeOr(ctx)
		for life.Err() == nil && !r.sessionOver.Load() && !r.fenced.Load() {
			rows, err := r.requestsOfThisGeneration(life)
			if err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
				return
			}
			if len(rows) == 0 {
				return
			}
			if r.w.clock.Now().UnixMilli() >= rows[0].DeadlineMs {
				r.parkOnApprovals(life, rows, false)
				return
			}
			select {
			case <-life.Done():
				return
			case <-time.After(superviseEvery):
			}
		}
	})
}

// parkOnApprovals parks a lane on what it asked. A wake holds each request, so an answer that comes
// while the lane parks fires at its commit.
func (r *laneRun) parkOnApprovals(ctx context.Context, rows []lanes.OpenApproval, skipNarrative bool) {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	r.parkReported(ctx, lanes.ParkRequest{Trigger: lanes.ParkApproval, Expr: lanes.ApprovalWake(ids), SkipNarrative: skipNarrative})
}

// parkDeferred parks a lane whose turn ended on a deferred call, and reports whether it did. The
// turn ended with no process waiting, so there is nothing to ask the session for.
func (r *laneRun) parkDeferred(ctx context.Context) bool {
	rows, err := r.requestsOfThisGeneration(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return false
	}
	if len(rows) == 0 {
		return false
	}
	r.parkOnApprovals(ctx, rows, true)
	return true
}

// resumeApprovals restores what a journal held of a lane that goes on.
func (r *laneRun) resumeApprovals(ctx context.Context) {
	rows, err := r.requestsOfThisGeneration(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	r.state.noteApprovals(len(rows))
	if len(rows) != 0 && !r.parkingOf().Open() {
		r.awaitApproval(ctx)
	}
}

// requestsOfThisGeneration reads what this session is waiting on. A request an earlier generation
// left stands in the ledger, and a retry takes it up.
func (r *laneRun) requestsOfThisGeneration(ctx context.Context) ([]lanes.OpenApproval, error) {
	rows, err := r.w.journal.OpenApprovals(ctx, r.lease.Lane)
	if err != nil {
		return nil, err
	}
	generation := r.currentGeneration()
	return slices.DeleteFunc(rows, func(row lanes.OpenApproval) bool { return row.Generation != generation }), nil
}

// terminalAnswer records what a tool's end showed of a call with an open request: a person answered
// on the screen the hook is waiting behind. It closes the request and grants nothing, and the ledger
// hears of it in the write that closes it. A hook names a call by its input alone, so seen is that.
func (r *laneRun) terminalAnswer(ctx context.Context, seen, note string) {
	if r.state.approvalsOpen() == 0 {
		return
	}
	rows, err := r.requestsOfThisGeneration(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	left := len(rows)
	for _, row := range rows {
		if seen != "" && row.Seen != seen {
			continue
		}
		payload, err := json.Marshal(authority.TerminalAnswer{Digest: row.Digest, Note: note})
		if err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
			return
		}
		if err := r.w.journal.CloseApproval(ctx, row.ID, r.w.event(ledger.UploadApprovalAnswered, r.record(), payload)); err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
			return
		}
		left--
		r.decisionsLeft(ctx, left)
	}
}

// turnEndedWithRequests reads a turn that ended with a request open. A lane on its way to parking
// left it open on purpose, and so did a deferred call. Otherwise nothing is left to decide: a hook
// holds a turn open, so only a person's answer on the screen lets it end with no tool run.
func (r *laneRun) turnEndedWithRequests(ctx context.Context, turn *ports.TurnResult) {
	if turn.Deferred || r.parkingOf().Open() {
		return
	}
	r.terminalAnswer(ctx, "", noteNoTool)
}

// takeDecision carries what an orchestrator decided. Only a token that stands for one reaches the
// ledger, so a lane's token is refused here, and every other rule is the ledger's.
func (w *Worker) takeDecision(ctx context.Context, lane lanes.Record, request lanes.DecideRequest) lanes.Reply {
	if lane.Owner != lanes.OwnerOrchestrator {
		return refused(lanes.CodeForbidden, "this token cannot decide a request")
	}
	bounded, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()
	_, err := w.link.DecideApproval(bounded, authority.WorkerDecision{
		Auth: w.Auth(), Epoch: w.epoch(),
		Decision: authority.Decision{ID: request.ID, Digest: request.Digest, Allow: request.Allow, Reason: request.Reason},
	})
	switch {
	case err == nil:
		return lanes.Reply{OK: true}
	case errors.Is(err, authority.ErrSelfApproval), errors.Is(err, authority.ErrNotDelegated), errors.Is(err, authority.ErrDeciderKind):
		return refused(lanes.CodeForbidden, err.Error())
	case errors.Is(err, authority.ErrStaleDigest), errors.Is(err, authority.ErrApprovalClosed):
		return refused(lanes.CodeInvalid, err.Error())
	}
	return refused(lanes.CodeUnavailable, err.Error())
}
