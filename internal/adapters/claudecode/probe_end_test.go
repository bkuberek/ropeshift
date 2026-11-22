package claudecode

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// stubbornProcess is a session's process that ends only on the signal it is told to end on, and records
// every signal it was sent.
type stubbornProcess struct {
	endsOn  ports.Signal
	exited  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	signals []ports.Signal
}

func newStubbornProcess(endsOn ports.Signal) *stubbornProcess {
	return &stubbornProcess{endsOn: endsOn, exited: make(chan struct{})}
}

func (p *stubbornProcess) PID() int { return os.Getpid() }

func (p *stubbornProcess) Signal(signal ports.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, signal)
	p.mu.Unlock()
	if signal == p.endsOn {
		p.once.Do(func() { close(p.exited) })
	}
	return nil
}

func (p *stubbornProcess) Wait() (ports.ExitStatus, error) {
	<-p.exited
	return ports.ExitStatus{}, nil
}

func (p *stubbornProcess) sent() []ports.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.signals)
}

// probeSessionOn is a probe session on a process, as startAs makes one: it learns the process ended when
// that process does, and its stream ends with streamEnds, which a test leaves open to stand for a stream a
// child of the session still holds.
func probeSessionOn(t *testing.T, process *stubbornProcess, streamEnds bool) *probeSession {
	t.Helper()
	_, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	s := &probeSession{process: process, input: input, exited: make(chan struct{}), read: make(chan struct{})}
	if streamEnds {
		close(s.read)
	}
	go func() {
		_, _ = process.Wait()
		close(s.exited)
	}()
	return s
}

// endsWithin runs end and fails the test when it has not returned within limit.
func endsWithin(t *testing.T, ctx context.Context, limit time.Duration, s *probeSession) time.Duration {
	t.Helper()
	began := time.Now()
	returned := make(chan struct{})
	go func() {
		s.end(ctx)
		close(returned)
	}()
	select {
	case <-returned:
		return time.Since(began)
	case <-time.After(limit):
		t.Fatalf("a session's end had not returned %v after it began", limit)
		return 0
	}
}

// A probe that is stopped ends its sessions soon: it sends no interrupt a session may ignore and waits only its
// stop grace after the close of stdin before the kill, because the worker that stops it has a bounded time to end
// itself.
func TestAStoppedProbeKillsItsSessionAfterItsGrace(t *testing.T) {
	const wait = 100 * time.Millisecond
	t.Cleanup(ShortenProbeWaits(wait))
	process := newStubbornProcess(ports.SignalKill)
	s := probeSessionOn(t, process, true)
	ctx, stop := context.WithCancel(t.Context())
	stop()
	took := endsWithin(t, ctx, wait+KillWait+time.Second, s)
	if want := []ports.Signal{ports.SignalKill}; !slices.Equal(process.sent(), want) {
		t.Errorf("a stopped probe sent %v to its session, want %v", process.sent(), want)
	}
	if took < wait {
		t.Errorf("a stopped probe killed its session %v after the close of its stdin, before its grace of %v", took, wait)
	}
}

// A stopped probe ends its sessions within its stop grace and a kill's wait, and its pane within the close's bound,
// and both fit in the grace a stopping daemon gives its worker, well inside the limit `ropeshift init` gives its
// daemon to stop.
func TestAStoppedProbeEndsWithinTheBoundItsWorkerHas(t *testing.T) {
	if probeStopGrace < time.Second || probeStopGrace > 2*time.Second {
		t.Errorf("a stopped probe gives a session %v to end on its own after the close of its stdin, outside one to two seconds", probeStopGrace)
	}
	if got := max(probeStopGrace+killWait, probeCloseWait); got > 10*time.Second {
		t.Errorf("a stopped probe takes up to %v to end, longer than a stopping daemon waits for its worker", got)
	}
}

// A stopped probe's session that ends on its own soon after its stdin closes ends so, and is not killed. The agent
// CLI runs with the person's own home directory, so a session that is ending may be writing its configuration there,
// and a kill in the middle of that write would leave the file broken.
func TestAStoppedProbeLetsItsSessionEndOnItsOwnFirst(t *testing.T) {
	process := newStubbornProcess(ports.SignalKill)
	s := probeSessionOn(t, process, true)
	ctx, stop := context.WithCancel(t.Context())
	stop()
	time.AfterFunc(200*time.Millisecond, func() { process.once.Do(func() { close(process.exited) }) })
	endsWithin(t, ctx, 2*time.Second+KillWait+time.Second, s)
	if sent := process.sent(); len(sent) != 0 {
		t.Errorf("a stopped probe sent %v to a session that was ending on its own", sent)
	}
}

// A probe's session that stays through the close of its stdin and an interrupt is killed, and the end of all
// three steps is bounded by the waits of the steps, so a probe that ends on its own ends within them too.
func TestAProbeSessionThatIgnoresEverythingButKillIsKilledWithinItsWaits(t *testing.T) {
	const wait = 100 * time.Millisecond
	t.Cleanup(ShortenProbeWaits(wait))
	process := newStubbornProcess(ports.SignalKill)
	s := probeSessionOn(t, process, true)
	took := endsWithin(t, t.Context(), 2*wait+KillWait+time.Second, s)
	if want := []ports.Signal{ports.SignalInterrupt, ports.SignalKill}; !slices.Equal(process.sent(), want) {
		t.Errorf("a probe sent %v to a stubborn session, want %v", process.sent(), want)
	}
	if took < 2*wait {
		t.Errorf("a probe ended a session after %v, before the waits of its two gentler steps", took)
	}
}

// A session that ended on the close of its stdin but whose stream a child of it still holds open does not hold
// the probe: the end waits for the stream as long as it waits for the process, and no longer.
func TestAProbeSessionWhoseStreamIsHeldOpenDoesNotHoldTheProbe(t *testing.T) {
	const wait = 100 * time.Millisecond
	t.Cleanup(ShortenProbeWaits(wait))
	process := newStubbornProcess(ports.SignalKill)
	process.once.Do(func() { close(process.exited) })
	s := probeSessionOn(t, process, false)
	endsWithin(t, t.Context(), 2*wait+KillWait+time.Second, s)
}

// hungTerminal answers a close only when the call's context ends, as a tmux that does not answer does.
type hungTerminal struct{ ports.Terminal }

func (hungTerminal) Close(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// The closing of a stopped probe's pane has a bound of its own. The call outlives the probe's context, so a
// terminal that never answers would otherwise hold the stopping worker until a service manager killed it.
func TestAStoppedProbeDoesNotWaitForeverOnATerminalThatDoesNotAnswer(t *testing.T) {
	const wait = 100 * time.Millisecond
	t.Cleanup(ShortenProbeWaits(wait))
	p := &prober{d: New(nil, Options{Terminal: hungTerminal{}})}
	ctx, stop := context.WithCancel(t.Context())
	stop()
	returned := make(chan struct{})
	go func() {
		p.closePane(ctx, "probe-screens")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(wait + 3*time.Second):
		t.Fatal("closing a stopped probe's pane never returned on a terminal that does not answer")
	}
}
