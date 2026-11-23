package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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

// What a closed request says of how a person answered at a terminal, and of why ropeshift closed one
// itself.
const (
	noteToolRan   = "the tool ran with no decision on record"
	noteNoTool    = "the turn ended with no tool run"
	noteCallMoved = "the call this request covered lands somewhere else now"
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
	// A push of commits that hold another's files is answered before it asks anyone, while the lane can still fix
	// its commits (FR-LANE-20).
	if lanes.MayPush(call.Tool, call.Command) {
		if reason := w.refusedPush(ctx, lane); reason != "" {
			run.callRefused(ctx, lane, call.Digest)
			return answer(lanes.ApprovalDeny, reason, "")
		}
	}
	reply := run.lookAt(ctx, lane, call.Lands(insideWorktree(lane.Worktree, call.Path)), request)
	if reply.Approval != nil && reply.Approval.Decision == lanes.ApprovalDeny {
		run.callRefused(ctx, lane, call.Digest)
	}
	return reply
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
	s.approvalsSeq++
}

// approvalsMark reads the sequence of the notes of open requests, which a read of the journal's requests takes before
// it reads them.
func (s *laneState) approvalsMark() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvalsSeq
}

// noteApprovalsSince notes what a read of the journal found open, unless a live hook noted a count since the mark: that
// count is newer than the read, and the read would put the stale one back.
func (s *laneState) noteApprovalsSince(mark uint64, open int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvalsSeq != mark {
		return
	}
	s.approvals = open
	s.approvalsSeq++
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
	if reply, moved := r.movedCall(ctx, lane, call, request); moved {
		return reply
	}
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
	return r.keepWaiting(ctx, lane, call, request)
}

// movedCall reads a look of a hook that holds a request, at a call that lands somewhere else than that
// request says. The hook is told the request closed, and the request closes for good: it is void in the
// journal and in the ledger, which hears of it in the write that closes it. Left open, a request
// nobody waits on could still be approved, and the end of the turn would record a person's answer at a
// terminal that ropeshift's own hook never asked for. This comes before the ledger is read, because
// what the ledger holds for the call as it lands now says nothing of the call this hook held.
func (r *laneRun) movedCall(ctx context.Context, lane lanes.Record, call authority.ToolCall, request lanes.ApprovalRequest) (lanes.Reply, bool) {
	if request.Held == "" {
		return lanes.Reply{}, false
	}
	rows, err := r.w.journal.OpenApprovals(ctx, lane.ID)
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error()), true
	}
	for _, row := range rows {
		if row.ID != request.Held || row.Digest == call.Digest {
			continue
		}
		payload, err := json.Marshal(authority.MovedCall{Digest: row.Digest, Note: noteCallMoved})
		if err != nil {
			return refused(lanes.CodeUnavailable, err.Error()), true
		}
		if err := r.w.journal.CloseApproval(ctx, row.ID, r.w.event(ledger.UploadApprovalVoid, lane, payload)); err != nil {
			return refused(lanes.CodeUnavailable, err.Error()), true
		}
		r.decisionsLeft(ctx, len(rows)-1)
		return answer(lanes.ApprovalDeny, lanes.ReasonRequestClosed, row.ID), true
	}
	return lanes.Reply{}, false
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
		Auth: r.w.Auth(), Epoch: r.w.epoch(), Project: lane.Project, WorkOrder: lane.WorkOrder, Fence: lane.Fence, Lane: lane.ID, Generation: lane.Generation,
		Subject: call.Tool, Digest: call.Digest, Call: request.Call, Peek: request.Peek,
	})
}

// keepWaiting keeps a request on record and says how much longer a call waits. Past its bound a hook
// denies, and the lane parks before the hook hears of it, so the request stands for when it comes
// back. Parking starts here whether or not the hook's peek missed, because a hook can look after the
// deadline and before `awaitApproval` reads its clock, and a session that hears the denial calls for
// its handoff at once.
func (r *laneRun) keepWaiting(ctx context.Context, lane lanes.Record, call authority.ToolCall, request lanes.ApprovalRequest) lanes.Reply {
	open, err := r.standingRequest(ctx, lane, call, request)
	if errors.Is(err, errRequestGone) {
		return answer(lanes.ApprovalDeny, lanes.ReasonRequestClosed, "")
	}
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	left := time.UnixMilli(open.DeadlineMs).Sub(r.w.clock.Now())
	if left <= 0 {
		r.parkOnWaitRanOut(ctx)
		return answer(lanes.ApprovalDeny, lanes.ReasonApprovalWait, open.ID)
	}
	reply := answer(lanes.ApprovalWaiting, "", open.ID)
	reply.Approval.WaitMs = left.Milliseconds()
	return reply
}

// errRequestGone says a hook that was already waiting has no request left, because the call it held
// was answered at a terminal and the ledger has not heard yet. Only a new call raises one.
var errRequestGone = errors.New("this call has no request left")

// waitOf says how long a look lets a call wait on a person. A look whose peek missed gets none: the
// hook did not defer the call, so `PermissionRequest` holds a headless lane's process and quota for
// the whole `approval_wait` unless the lane parks now, and a deferral would have parked it at once.
// The look is told the wait ran out, which parks the lane the way any wait that ran out does.
func (r *laneRun) waitOf(request lanes.ApprovalRequest) time.Duration {
	if request.Missed {
		return 0
	}
	return r.state.approvalWait()
}

// standingRequest finds what this lane asked of this call, or raises it. The row and the event that
// carries it are one journal write, so a kill leaves a request the ledger hears of or none at all. A
// generation that goes on from the one that asked waits for itself, and so does a request that a late
// peek raised with the whole wait, once the look after that peek says it missed.
func (r *laneRun) standingRequest(ctx context.Context, lane lanes.Record, call authority.ToolCall, request lanes.ApprovalRequest) (lanes.OpenApproval, error) {
	rows, err := r.w.journal.OpenApprovals(ctx, lane.ID)
	if err != nil {
		return lanes.OpenApproval{}, err
	}
	deadline := r.w.clock.Now().Add(r.waitOf(request)).UnixMilli()
	for _, row := range rows {
		if row.Digest != call.Digest {
			continue
		}
		if row.Generation != lane.Generation || (request.Missed && row.DeadlineMs > deadline) {
			row.Generation, row.DeadlineMs = lane.Generation, deadline
			if err := r.w.journal.RenewApproval(ctx, row.ID, row.Generation, row.DeadlineMs); err != nil {
				return lanes.OpenApproval{}, err
			}
			r.noteRequest(ctx, len(rows))
		}
		return row, nil
	}
	if request.Raised {
		return lanes.OpenApproval{}, errRequestGone
	}
	id, err := newToken(questionIDBytes)
	if err != nil {
		return lanes.OpenApproval{}, err
	}
	open := lanes.OpenApproval{ID: id, Lane: lane.ID, Generation: lane.Generation, Tool: call.Tool, Digest: call.Digest, Seen: call.Seen, DeadlineMs: deadline}
	payload, err := json.Marshal(authority.Approval{
		ID: id, Lane: lane.ID, WorkOrder: lane.WorkOrder, Generation: lane.Generation, Kind: authority.KindToolCall, Subject: call.Tool,
		SubjectDigest: call.Digest, Input: call.Input, Command: call.Command, Path: call.Path, Agent: call.Agent, Summary: call.Summary,
		RequestedBy: authority.Principal(r.principal(lane)), Gate: r.gated(call),
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

// gated says a call is a gate: one this lane's profile gates, or a push, each of which asks in every mode (FR-LANE-19)
// and which a member approves one call at a time. A shell command is a gate when any command in it is one, or when
// the hook asks for it as one.
func (r *laneRun) gated(call authority.ToolCall) bool {
	r.handleLock.Lock()
	rules := r.gates
	r.handleLock.Unlock()
	gates := make([]lanes.GateRule, 0, len(rules))
	for _, rule := range rules {
		gates = append(gates, lanes.ReadGate(rule))
	}
	command := strings.TrimSpace(call.Command)
	policy := lanes.DenyPolicy{Gates: gates}
	if policy.Gated(lanes.ToolCall{Tool: call.Tool, Command: command}) {
		return true
	}
	if _, asks := policy.Asks(lanes.ToolCall{Tool: call.Tool, Command: call.Command}); asks {
		return true
	}
	return lanes.RunsShell(call.Tool) && command != "" && shellGated(command, gates)
}

// shellGated says a shell command is a gate: one of its commands is a push or a call a gate names, whatever stands
// before it, such as an assignment, `env`, `cd … &&`, a wrapper such as `nohup`, or git's own options. A command it
// cannot read with confidence is a gate too, because a gate it missed would reach Approve all.
func shellGated(command string, gates []lanes.GateRule) bool {
	return gatedText(command, gates, 0)
}

// maxGateDepth bounds how deep a command inside a word, such as the text of `sh -c`, is read. Past it, a command
// reads as a gate.
const maxGateDepth = 4

// shellSyntax are the characters that make a word worth reading as a command of its own: a word a shell, `eval`,
// `env -S`, or a remote shell may run.
const shellSyntax = " \t\r\n;&|()<>$`'\"\\"

func gatedText(text string, gates []lanes.GateRule, depth int) bool {
	if depth > maxGateDepth {
		return true
	}
	commands, ok := readShell(text, depth > 0)
	if !ok {
		return true
	}
	for _, words := range commands {
		if gatedWords(words, gates) {
			return true
		}
		for _, word := range words {
			// A word that reads back as itself holds no command of its own.
			if word.text != text && strings.ContainsAny(word.text, shellSyntax) && gatedText(word.text, gates, depth+1) {
				return true
			}
		}
	}
	return false
}

// gatedWords says one simple command is a gate. Whatever stands before a program may run the words after it, as an
// assignment, `env`, `nohup`, or `timeout 5` does, so every word is read as a place a command could start. `xargs`
// builds its command from input no reading of the call can see.
func gatedWords(words []shellWord, gates []lanes.GateRule) bool {
	texts := make([]string, len(words))
	for i, word := range words {
		if word.glob && globMeans(word.text, gates) {
			return true
		}
		texts[i] = word.text
	}
	policy := lanes.DenyPolicy{Gates: gates}
	gatedLine := func(words []string) bool {
		return policy.Gated(lanes.ToolCall{Tool: lanes.ToolBash, Command: strings.Join(words, " ")})
	}
	for i, text := range texts {
		name := path.Base(text)
		if strings.EqualFold(name, "xargs") || gatedLine(texts[i:]) {
			return true
		}
		if !strings.EqualFold(name, "git") {
			continue
		}
		rest, ok := gitSubcommand(texts[i+1:])
		if !ok || (len(rest) != 0 && rest[0] == "push") || gatedLine(append([]string{"git"}, rest...)) {
			return true
		}
	}
	return false
}

// globMeans says a word a shell may expand could become git, push, or a word a gate names.
func globMeans(pattern string, gates []lanes.GateRule) bool {
	if strings.Contains(pattern, "{") && strings.Contains(pattern, "}") && (strings.Contains(pattern, ",") || strings.Contains(pattern, "..")) {
		return true
	}
	targets := []string{"git", "push"}
	for _, gate := range gates {
		targets = append(targets, strings.Fields(gate.CommandPrefix)...)
	}
	for _, target := range targets {
		for _, form := range []string{pattern, path.Base(pattern)} {
			if matched, err := path.Match(form, target); err == nil && matched {
				return true
			}
		}
	}
	return false
}

// gitValueOptions are git's options before a subcommand that take the next word when no `=` joins them.
var gitValueOptions = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"}

// gitSubcommand takes git's own options away from the words after `git`, and returns its subcommand and the rest. ok
// is false for a setting that makes a word git reads as a subcommand run another one.
func gitSubcommand(args []string) ([]string, bool) {
	for len(args) != 0 && strings.HasPrefix(args[0], "-") {
		name, value, joined := strings.Cut(args[0], "=")
		if !joined && slices.Contains(gitValueOptions, args[0]) && len(args) > 1 {
			value = args[1]
			args = args[1:]
		}
		if (name == "-c" || name == "--config-env") && renamesCommands(value) {
			return nil, false
		}
		args = args[1:]
	}
	return args, true
}

// renamesCommands says a setting makes a subcommand stand for another: an alias, or autocorrect, which runs the
// subcommand a mistyped one is near.
func renamesCommands(setting string) bool {
	key, _, _ := strings.Cut(strings.ToLower(setting), "=")
	return strings.HasPrefix(key, "alias.") || key == "help.autocorrect"
}

// shellWord is one word of a command as a shell passes it, with its quotes taken away. Glob says it holds an unquoted
// pattern or brace a shell may expand into other words.
type shellWord struct {
	text string
	glob bool
}

// shellReader splits a command line the way a shell does, and no further: it holds to what a gate needs to see.
type shellReader struct {
	text string
	at   int
	// lenient reads a quote that never closes to the end of the text, for a word read again as a command, which is
	// more often prose with an apostrophe than a command a shell would refuse.
	lenient  bool
	commands [][]shellWord
	words    []shellWord
	word     strings.Builder
	inWord   bool
	glob     bool
	// redirect says the next word is a redirect's target, which is no word of the command.
	redirect bool
	// unread says the text holds what this reading does not follow.
	unread bool
}

// readShell splits a command line into its simple commands, at `&&`, `||`, `;`, `|`, `&`, a newline, and the
// parentheses of a subshell, and leaves out comments and redirects. ok is false for what it does not follow: an
// expansion or a substitution, a here-document or here-string, a process substitution, a redirect with no target,
// and, unless lenient, a quote that never closes.
func readShell(text string, lenient bool) ([][]shellWord, bool) {
	r := &shellReader{text: text, lenient: lenient}
	for r.at < len(r.text) && !r.unread {
		r.step()
	}
	r.endCommand()
	return r.commands, !r.unread
}

func (r *shellReader) peek() byte {
	if r.at < len(r.text) {
		return r.text[r.at]
	}
	return 0
}

func (r *shellReader) add(text string) {
	r.word.WriteString(text)
	r.inWord = true
}

func (r *shellReader) endWord() {
	if r.inWord {
		if r.redirect {
			r.redirect = false
		} else {
			r.words = append(r.words, shellWord{text: r.word.String(), glob: r.glob})
		}
	}
	r.word.Reset()
	r.inWord, r.glob = false, false
}

func (r *shellReader) endCommand() {
	r.endWord()
	if r.redirect {
		r.unread = true
	}
	if len(r.words) != 0 {
		r.commands = append(r.commands, r.words)
	}
	r.words = nil
}

func (r *shellReader) step() {
	c := r.text[r.at]
	r.at++
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		r.endWord()
	case strings.IndexByte(";|()\n", c) >= 0:
		r.endCommand()
	case c == '&' && r.peek() == '>':
		r.readRedirect()
	case c == '&':
		r.endCommand()
	case c == '<' || c == '>':
		r.at--
		r.readRedirect()
	case c == '#' && !r.inWord:
		for r.at < len(r.text) && r.text[r.at] != '\n' {
			r.at++
		}
	case c == '\\':
		if next := r.peek(); next != 0 {
			r.at++
			if next != '\n' {
				r.add(string(next))
			}
		}
	case c == '\'':
		r.readSingleQuoted()
	case c == '"':
		r.readDoubleQuoted()
	case c == '`', c == '$' && expands(r.peek(), true):
		r.unread = true
	default:
		if strings.IndexByte("*?[{", c) >= 0 {
			r.glob = true
		}
		r.add(string(c))
	}
}

// readRedirect reads a redirect's operator, whose target is the next word. A number just before it names the file
// descriptor and is part of it.
func (r *shellReader) readRedirect() {
	if r.inWord && strings.Trim(r.word.String(), "0123456789") == "" {
		r.word.Reset()
		r.inWord, r.glob = false, false
	} else {
		r.endWord()
	}
	op := r.text[r.at]
	r.at++
	next := r.peek()
	switch {
	case r.redirect, next == '(', op == '<' && next == '<':
		r.unread = true
	case op == '>' && strings.IndexByte(">&|", next) >= 0, op == '<' && (next == '&' || next == '>'):
		r.at++
	}
	r.redirect = true
}

func (r *shellReader) readSingleQuoted() {
	end := strings.IndexByte(r.text[r.at:], '\'')
	if end < 0 {
		r.unread = !r.lenient
		r.add(r.text[r.at:])
		r.at = len(r.text)
		return
	}
	r.add(r.text[r.at : r.at+end])
	r.at += end + 1
}

func (r *shellReader) readDoubleQuoted() {
	r.inWord = true
	for r.at < len(r.text) {
		c := r.text[r.at]
		r.at++
		switch {
		case c == '"':
			return
		case c == '\\' && strings.IndexByte("$`\"\\\n", r.peek()) >= 0:
			if r.peek() != '\n' {
				r.word.WriteByte(r.peek())
			}
			r.at++
		case c == '`', c == '$' && expands(r.peek(), false):
			r.unread = true
			return
		default:
			r.word.WriteByte(c)
		}
	}
	r.unread = !r.lenient
}

// expands says a `$` before this byte starts an expansion: of a name, a parameter, or a substitution, and outside
// double quotes a quote of its own, which a shell decodes.
func expands(next byte, unquoted bool) bool {
	switch {
	case next == '_', next >= 'a' && next <= 'z', next >= 'A' && next <= 'Z', next >= '0' && next <= '9':
		return true
	case strings.IndexByte("{([@*#?$!-", next) >= 0:
		return true
	}
	return unquoted && (next == '\'' || next == '"')
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

// parkOnWaitRanOut begins parking before a hook hears that its wait ran out, where `awaitApproval`
// would begin it at its next look at the clock. A hook told the wait ran out lets the turn go on, so
// the session's handoff follows, and one that finds no parking asked of it is refused. A turn that
// ends with a request open and no parking begun reads as a person's answer at a terminal, which
// closes the request and leaves the lane to go on. Begun twice, the second finds it begun.
func (r *laneRun) parkOnWaitRanOut(ctx context.Context) {
	rows, err := r.requestsOfThisGeneration(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if len(rows) != 0 {
		r.parkOnApprovals(r.lifeOr(ctx), rows, false)
	}
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

// resumeApprovals restores what a journal held of a lane that goes on, and returns the calls it still waits
// on, each as a hook names its call, and whether it could read them.
func (r *laneRun) resumeApprovals(ctx context.Context) ([]string, bool) {
	mark := r.state.approvalsMark()
	rows, err := r.requestsOfThisGeneration(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return nil, false
	}
	r.state.noteApprovalsSince(mark, len(rows))
	if len(rows) != 0 && !r.parkingOf().Open() {
		r.awaitApproval(ctx)
	}
	return seenOf(rows), true
}

// openApprovals reads the calls this session waits on a decision for, as a hook names each, and whether it could.
func (r *laneRun) openApprovals(ctx context.Context) ([]string, bool) {
	rows, err := r.requestsOfThisGeneration(ctx)
	if err != nil {
		return nil, false
	}
	return seenOf(rows), true
}

func seenOf(rows []lanes.OpenApproval) []string {
	waits := make([]string, 0, len(rows))
	for _, row := range rows {
		waits = append(waits, row.Seen)
	}
	return waits
}

// tellDriver hands the driver one hook. A driver drops a hook of a session it does not hold yet, so until an
// adoption ends the run also keeps the ends of turns, the prompts, the calls asked that have not ended, and the
// Notifications of a dialog. An end discards only what is not newer than it, and goes ahead of the rest, because a
// spool can deliver one late, and a dialog no newer than an end kept already closed.
func (r *laneRun) tellDriver(ctx context.Context, event lanes.HookEvent) {
	r.untoldLock.Lock()
	defer r.untoldLock.Unlock()
	if r.untold != nil {
		switch event.Event {
		case hookPermissionRequest:
			if !r.closedByKeptEnd(event) {
				r.untold = append(r.untold, event)
			}
		case hookNotification:
			if (lanes.DialogOpens(event.Detail) || lanes.DialogCloses(event.Detail)) && !r.closedByKeptEnd(event) {
				r.untold = append(r.untold, event)
			}
		case lanes.EventUserPromptSubmit:
			r.untold = append(r.untold, event)
		case hookPostToolUse, lanes.EventToolFailed:
			r.untold = slices.DeleteFunc(r.untold, func(kept lanes.HookEvent) bool {
				return kept.Event == hookPermissionRequest && kept.Digest == event.Digest
			})
		case lanes.EventStop, hookStopFailure:
			r.untold = slices.Insert(slices.DeleteFunc(r.untold, func(kept lanes.HookEvent) bool {
				return kept.AtMs <= event.AtMs
			}), 0, event)
		}
	}
	r.rt.Driver.TakeHook(ctx, event)
}

// closedByKeptEnd says the end of a turn the run keeps fired at or after a dialog's hook, so the dialog closed. A hook
// with no time cannot be placed. The caller holds untoldLock.
func (r *laneRun) closedByKeptEnd(event lanes.HookEvent) bool {
	return event.AtMs > 0 && slices.ContainsFunc(r.untold, func(kept lanes.HookEvent) bool {
		return (kept.Event == lanes.EventStop || kept.Event == hookStopFailure) && kept.AtMs >= event.AtMs
	})
}

// adopting says an adoption is under way, so the driver may not hold the session yet.
func (r *laneRun) adopting() bool {
	r.untoldLock.Lock()
	defer r.untoldLock.Unlock()
	return r.untold != nil
}

// tellUntold ends what tellDriver keeps, and hands it to the driver in order. A second read of the approvals
// would not do in its place: a look raises its request after its hook is delivered, so maybe after that read.
func (r *laneRun) tellUntold(ctx context.Context) {
	r.untoldLock.Lock()
	defer r.untoldLock.Unlock()
	for _, event := range r.untold {
		r.rt.Driver.TakeHook(ctx, event)
	}
	r.untold = nil
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
	if err := w.namesProject(ctx, request.Project); err != nil {
		return refused(lanes.CodeInvalid, err.Error())
	}
	bounded, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()
	_, err := w.link.DecideApproval(bounded, authority.WorkerDecision{
		Auth: w.Auth(), Epoch: w.epoch(), Project: request.Project,
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
