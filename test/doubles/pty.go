package doubles

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The size of a new terminal, which tmux needs to draw anything.
const (
	ptyRows    = 45
	ptyColumns = 130
)

// PTY is a person's terminal as a test sees it. A client attaches with Terminal as its keyboard and
// its screen, and the test types on Master and reads what the client drew there.
type PTY struct {
	Master   *os.File
	Terminal *os.File

	mu     sync.Mutex
	screen bytes.Buffer
}

// NewPTY opens a terminal that nobody has a session on, and closes it when the test ends.
func NewPTY(t testing.TB) *PTY {
	t.Helper()
	master, terminal, err := openPTY()
	if err != nil {
		t.Skipf("no pseudo-terminal on this machine: %v", err)
	}
	p := &PTY{Master: master, Terminal: terminal}
	t.Cleanup(func() {
		_ = terminal.Close()
		_ = master.Close()
	})
	go p.drain()
	return p
}

// drain keeps what the terminal draws, because a client that cannot write stops.
func (p *PTY) drain() {
	buf := make([]byte, 4096)
	for {
		n, err := p.Master.Read(buf)
		p.mu.Lock()
		p.screen.Write(buf[:n])
		p.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Type enters keys as a person would, such as "\x02d" for the detach binding.
func (p *PTY) Type(t testing.TB, keys string) {
	t.Helper()
	if _, err := p.Master.WriteString(keys); err != nil {
		t.Fatal(err)
	}
}

// Drawn is everything the client has drawn so far.
func (p *PTY) Drawn() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.screen.String()
}

// WaitFor holds a test until the client has drawn a piece of text.
func (p *PTY) WaitFor(t testing.TB, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(p.Drawn(), text) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%q was never drawn: %q", text, p.Drawn())
}
