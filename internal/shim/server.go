package shim

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
)

const (
	// drainGrace bounds the wait for a child's last lines when something else it
	// started still holds its stdout.
	drainGrace = 2 * time.Second
	lockFile   = "lock"
	// maxRequest bounds one request line.
	maxRequest = 16 << 20
	// ioDeadline bounds a request that is not an attach.
	ioDeadline = 30 * time.Second
)

// ErrLocked means another shim holds this generation, so a second must not start.
var ErrLocked = errors.New("a shim already holds this generation")

// call is one request on its way through the event loop. Once the loop takes it, it owes
// an answer, and sent closes when that answer has left for the caller.
type call struct {
	request shimlink.Request
	answer  chan shimlink.Reply
	sent    chan struct{}
}

// server is one running shim. The event loop owns seen, stdinOpen, and ended, so a ping
// that answers proves the loop is alive.
type server struct {
	config  shimlink.Config
	journal *shimlink.Journal
	child   *exec.Cmd

	lines  chan string
	exited chan shimlink.Exit
	calls  chan call
	writes chan write

	seen      map[string]bool
	stdinOpen bool
	ended     bool

	mu       sync.Mutex
	attached net.Conn
}

// write is one queued act on the child's stdin. The queue keeps their order, and a full
// pipe stalls only the writer, because the loop must keep journaling what the child says.
type write struct {
	id    string
	line  string
	close bool
}

// The generations a shim of this process holds. A record lock keeps other processes out but not its
// own, and closing any descriptor of its file ends it, so a second shim here is turned away before
// it opens the file.
var (
	heldLock sync.Mutex
	held     = map[string]bool{}
)

// lock takes a generation for this shim. It is a record lock, which belongs to this process, so the
// kernel releases it when the process dies. A flock would belong to the open file instead, and the
// child a shim forks shares that file until it execs, so a shim killed in that moment would leave
// its generation held and the next shim for it refused.
func lock(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	whole, err := filepath.Abs(filepath.Join(dir, lockFile))
	if err != nil {
		return nil, err
	}
	heldLock.Lock()
	defer heldLock.Unlock()
	if held[whole] {
		return nil, fmt.Errorf("%w: by this process", ErrLocked)
	}
	file, err := os.OpenFile(whole, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	all := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	for {
		err = syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &all)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, fmt.Errorf("%w: %v", ErrLocked, err)
		}
		return nil, err
	}
	held[whole] = true
	return file, nil
}

// unlock gives a generation back.
func unlock(file *os.File) {
	heldLock.Lock()
	defer heldLock.Unlock()
	_ = file.Close()
	delete(held, file.Name())
}

// Run owns one child until it ends and the linger passes. The kernel drops the lock with
// the process, however it dies.
func Run(ctx context.Context, config shimlink.Config) error {
	if len(config.Command) == 0 {
		return errors.New("a shim needs a command")
	}
	if config.Linger == 0 {
		config.Linger = shimlink.DefaultLinger
	}
	generation, err := lock(config.Dir)
	if err != nil {
		return err
	}
	defer unlock(generation)
	if err := shimlink.WriteState(config.Dir, shimlink.State{Version: shimlink.Version, ShimPID: os.Getpid()}); err != nil {
		return err
	}
	journal, err := shimlink.OpenJournal(config.Dir, config.JournalCap)
	if err != nil {
		return err
	}
	defer journal.Close()
	s := &server{
		config: config, journal: journal, lines: make(chan string, 1024), exited: make(chan shimlink.Exit, 1),
		calls: make(chan call), writes: make(chan write, 1024), seen: map[string]bool{}, stdinOpen: true,
	}
	stdin, err := s.start()
	if err != nil {
		return err
	}
	listener, err := listen(config.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	serving, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.accept(serving, listener)
	go s.writer(stdin)
	return s.loop(serving)
}

// listen binds a socket only its owner can reach, in a directory only its owner can
// enter. The directory protects the moment before the chmod.
func listen(socket string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// start wires the child's pipes by hand, because the pipes from exec close at Wait, and
// this process must read to the very last line. The child inherits this process's
// stderr, so a file keeps taking it whichever of them dies first.
func (s *server) start() (*os.File, error) {
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		return nil, err
	}
	child := exec.Command(s.config.Command[0], s.config.Command[1:]...)
	child.Stdin, child.Stdout, child.Stderr = stdinRead, stdoutWrite, os.Stderr
	err = child.Start()
	_ = stdinRead.Close()
	_ = stdoutWrite.Close()
	if err != nil {
		_ = stdinWrite.Close()
		_ = stdoutRead.Close()
		return nil, err
	}
	s.child = child
	if err := shimlink.WriteState(s.config.Dir, shimlink.State{Version: shimlink.Version, ShimPID: os.Getpid(), ChildPID: child.Process.Pid}); err != nil {
		_ = child.Process.Kill()
		return nil, err
	}
	pumped := make(chan struct{})
	go s.pump(stdoutRead, pumped)
	go s.reap(pumped)
	return stdinWrite, nil
}

// pump never stops reading, so a child never blocks on an absent worker.
func (s *server) pump(stdout *os.File, pumped chan<- struct{}) {
	defer close(pumped)
	defer stdout.Close()
	reader := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := reader.ReadString('\n')
		if text := strings.TrimRight(line, "\r\n"); text != "" {
			s.lines <- text
		}
		if err != nil {
			return
		}
	}
}

// reap reports the child's end after its last line, or after a grace when something it
// left behind still holds the pipe.
func (s *server) reap(pumped <-chan struct{}) {
	err := s.child.Wait()
	select {
	case <-pumped:
	case <-time.After(drainGrace):
	}
	s.exited <- exitOf(err)
}

func exitOf(err error) shimlink.Exit {
	var failed *exec.ExitError
	switch {
	case err == nil:
		return shimlink.Exit{}
	case errors.As(err, &failed):
		if status, ok := failed.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return shimlink.Exit{Signal: status.Signal().String()}
		}
		return shimlink.Exit{Code: failed.ExitCode()}
	}
	return shimlink.Exit{Code: -1, Error: err.Error()}
}

// writer is the one writer of stdin. An input goes on record before it is written, so no
// line it causes can come first in the journal.
func (s *server) writer(stdin *os.File) {
	defer stdin.Close()
	for act := range s.writes {
		if act.close {
			return
		}
		if _, err := s.journal.Append(shimlink.Record{Input: act.id}); err != nil {
			fmt.Fprintf(os.Stderr, "lane-shim: journal: %v\n", err)
		}
		if _, err := stdin.WriteString(act.line + "\n"); err != nil {
			fmt.Fprintf(os.Stderr, "lane-shim: stdin: %v\n", err)
		}
	}
}

// loop is the shim. Everything a client can change passes through here.
func (s *server) loop(ctx context.Context) error {
	var linger <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line := <-s.lines:
			if _, err := s.journal.Append(shimlink.Record{Line: line}); err != nil {
				return err
			}
		case exit := <-s.exited:
			for drained := false; !drained; {
				select {
				case line := <-s.lines:
					if _, err := s.journal.Append(shimlink.Record{Line: line}); err != nil {
						return err
					}
				default:
					drained = true
				}
			}
			if _, err := s.journal.Append(shimlink.Record{Exit: &exit}); err != nil {
				return err
			}
			s.ended = true
			linger = time.After(s.config.Linger)
		case c := <-s.calls:
			reply := s.handle(c.request)
			c.answer <- reply
			if c.request.Op == shimlink.OpShutdown && reply.OK {
				// Main exits when this returns, so the reply leaves first. The
				// connection's deadline bounds the wait.
				<-c.sent
				return nil
			}
		case <-linger:
			return nil
		}
	}
}

func (s *server) refuse(code, format string, args ...any) shimlink.Reply {
	return shimlink.Reply{Version: shimlink.Version, Code: code, Message: fmt.Sprintf(format, args...)}
}

// handle runs on the event loop. A request names the version it speaks, and any version
// from the oldest to this one is served.
func (s *server) handle(request shimlink.Request) shimlink.Reply {
	if request.Op != shimlink.OpHello && (request.Version < shimlink.OldestVersion || request.Version > shimlink.Version) {
		return s.refuse(shimlink.CodeBadVersion, "this shim speaks %d to %d", shimlink.OldestVersion, shimlink.Version)
	}
	ok := shimlink.Reply{OK: true, Version: shimlink.Version}
	switch request.Op {
	case shimlink.OpHello:
		ok.PID = os.Getpid()
		return ok
	case shimlink.OpPing:
		ok.Nonce = request.Nonce
		return ok
	case shimlink.OpInput:
		return s.input(request)
	case shimlink.OpSignal:
		return s.signal(request.Signal)
	case shimlink.OpCloseStdin:
		if s.stdinOpen {
			if !s.queue(write{close: true}) {
				return s.refuse(shimlink.CodeBusy, "the child is not reading its stdin")
			}
			s.stdinOpen = false
		}
		return ok
	case shimlink.OpShutdown:
		if !s.ended {
			return s.refuse(shimlink.CodeBadRequest, "the child is still running")
		}
		return ok
	}
	return s.refuse(shimlink.CodeBadRequest, "no such operation: %q", request.Op)
}

// validLine holds a line to what the CLI can take: one line, one JSON object, with a
// type. One that does not parse would end the session, so it never reaches the child. A
// type the CLI does not know passes, because the CLI ignores it.
func validLine(line string) bool {
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return false
	}
	var probe struct {
		Type *string `json:"type"`
	}
	return json.Unmarshal([]byte(line), &probe) == nil && probe.Type != nil && *probe.Type != ""
}

// input validates and queues one line, at most once per ID. A refused line spends no ID,
// so a corrected retry can use it.
func (s *server) input(request shimlink.Request) shimlink.Reply {
	switch {
	case request.ID == "":
		return s.refuse(shimlink.CodeBadRequest, "an input needs an ID")
	case s.seen[request.ID]:
		return shimlink.Reply{OK: true, Version: shimlink.Version, Duplicate: true}
	case !validLine(request.Line):
		return s.refuse(shimlink.CodeBadLine, "this line would end the session")
	case !s.stdinOpen || s.ended:
		return s.refuse(shimlink.CodeStdinClosed, "the child's stdin is closed")
	}
	if !s.queue(write{id: request.ID, line: request.Line}) {
		return s.refuse(shimlink.CodeBusy, "the child is not reading its stdin")
	}
	s.seen[request.ID] = true
	return shimlink.Reply{OK: true, Version: shimlink.Version}
}

// queue never waits, because the event loop must keep taking what the child writes.
func (s *server) queue(act write) bool {
	select {
	case s.writes <- act:
		return true
	default:
		return false
	}
}

func (s *server) signal(name string) shimlink.Reply {
	signals := map[string]os.Signal{
		shimlink.SignalInterrupt: syscall.SIGINT, shimlink.SignalTerminate: syscall.SIGTERM, shimlink.SignalKill: syscall.SIGKILL,
	}
	value, ok := signals[name]
	if !ok {
		return s.refuse(shimlink.CodeBadSignal, "no such signal: %q", name)
	}
	if err := s.child.Process.Signal(value); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return s.refuse(shimlink.CodeBadSignal, "%v", err)
	}
	return shimlink.Reply{OK: true, Version: shimlink.Version}
}

func (s *server) accept(ctx context.Context, listener net.Listener) {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go s.serve(ctx, conn)
	}
}

// serve answers one connection, which carries one request.
func (s *server) serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if conn.SetDeadline(time.Now().Add(ioDeadline)) != nil {
		return
	}
	reader := bufio.NewReaderSize(conn, 1<<16)
	line, err := reader.ReadSlice('\n')
	for errors.Is(err, bufio.ErrBufferFull) && len(line) < maxRequest {
		var more []byte
		more, err = reader.ReadSlice('\n')
		line = append(append([]byte{}, line...), more...)
	}
	if err != nil {
		return
	}
	var request shimlink.Request
	if err := json.Unmarshal(line, &request); err != nil {
		shimlink.Send(conn, s.refuse(shimlink.CodeBadRequest, "this is not a line of the shim protocol"))
		return
	}
	if request.Op == shimlink.OpAttach {
		s.attach(ctx, conn, reader, request)
		return
	}
	answer, sent := make(chan shimlink.Reply, 1), make(chan struct{})
	defer close(sent)
	select {
	case s.calls <- call{request: request, answer: answer, sent: sent}:
	case <-ctx.Done():
		return
	}
	// The loop answers whatever it takes, and a shutdown ends serving as it answers, so a
	// cancelled context here must not drop the reply.
	shimlink.Send(conn, <-answer)
}

// attach follows the journal for one client. A new attach takes over from the last, so a
// returning worker needs nothing from the one it replaced. A record reaches the client
// from disk whether it is old or new, which is what keeps catch-up and live one path.
func (s *server) attach(ctx context.Context, conn net.Conn, reader *bufio.Reader, request shimlink.Request) {
	if request.Version < shimlink.OldestVersion || request.Version > shimlink.Version {
		shimlink.Send(conn, s.refuse(shimlink.CodeBadVersion, "this shim speaks %d to %d", shimlink.OldestVersion, shimlink.Version))
		return
	}
	s.mu.Lock()
	if s.attached != nil {
		_ = s.attached.Close()
	}
	s.attached = conn
	s.mu.Unlock()
	if conn.SetDeadline(time.Time{}) != nil {
		return
	}
	if !shimlink.Send(conn, shimlink.Reply{OK: true, Version: shimlink.Version}) {
		return
	}
	follow, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		cancel()
	}()
	tail := s.journal.Tail(request.Since)
	defer tail.Close()
	for {
		record, err := tail.Next(follow)
		if err != nil || !shimlink.Send(conn, record) {
			return
		}
	}
}
