package top

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// Msg is one thing that happened. A model changes only by taking one.
type Msg any

// Cmd is work that ends in a message. It runs apart from the model, so a slow read holds no key back.
type Cmd func() Msg

type batchMsg []Cmd

type quitMsg struct{}

func quit() Msg { return quitMsg{} }

// batch runs commands side by side, and leaves out those with nothing to do.
func batch(cmds ...Cmd) Cmd {
	var running []Cmd
	for _, cmd := range cmds {
		if cmd != nil {
			running = append(running, cmd)
		}
	}
	switch len(running) {
	case 0:
		return nil
	case 1:
		return running[0]
	}
	return func() Msg { return batchMsg(running) }
}

// Each runs a command, and each one it batched, and hands over what they said.
func (c Cmd) Each(take func(Msg)) {
	msg := c()
	if cmds, ok := msg.(batchMsg); ok {
		for _, cmd := range cmds {
			cmd.Each(take)
		}
		return
	}
	take(msg)
}

// IsQuit says a message ends the view.
func IsQuit(msg Msg) bool {
	_, quitting := msg.(quitMsg)
	return quitting
}

// The sequences a terminal needs around a live view: a screen of its own, no cursor, and the way back.
const (
	enterScreen = "\x1b[?1049h\x1b[?25l"
	leaveScreen = "\x1b[?25h\x1b[?1049l"
	homeCursor  = "\x1b[H"
	clearLine   = "\x1b[K"
	clearBelow  = "\x1b[J"
)

// The keys a person can press, as the model reads them.
const (
	KeyUp    = "up"
	KeyDown  = "down"
	KeyEsc   = "esc"
	KeyCtrlC = "ctrl+c"
)

// readKeys turns what a terminal sends into keys. A raw terminal sends ctrl+c as a byte and an arrow as
// an escape sequence. An escape alone arrives in a read of its own, and anything else is ignored,
// because a view that only moves a cursor has no use for the rest.
func readKeys(chunk []byte) []string {
	var keys []string
	for i := 0; i < len(chunk); i++ {
		switch b := chunk[i]; {
		case b == 0x03:
			keys = append(keys, KeyCtrlC)
		case b == 0x1b && i+2 < len(chunk) && chunk[i+1] == '[':
			switch chunk[i+2] {
			case 'A':
				keys = append(keys, KeyUp)
			case 'B':
				keys = append(keys, KeyDown)
			}
			i += 2
		case b == 0x1b && len(chunk) == 1:
			keys = append(keys, KeyEsc)
		case b == 0x1b:
			return keys
		case b >= 0x20 && b < 0x7f:
			keys = append(keys, string(rune(b)))
		}
	}
	return keys
}

// paint draws a frame over the last one, so a screen that changes one line does not flicker. A raw
// terminal does not turn a newline into a carriage return.
func paint(out io.Writer, frame string) error {
	var text strings.Builder
	text.WriteString(homeCursor)
	for i, line := range strings.Split(frame, "\n") {
		if i > 0 {
			text.WriteString("\r\n")
		}
		text.WriteString(line)
		text.WriteString(clearLine)
	}
	text.WriteString(clearBelow)
	_, err := io.WriteString(out, text.String())
	return err
}

// measured says how big a terminal is. A pipe has no size, and a frame takes its plain width.
func measured(out io.Writer) (Msg, bool) {
	file, ok := out.(*os.File)
	if !ok {
		return nil, false
	}
	width, height, err := term.GetSize(int(file.Fd()))
	if err != nil {
		return nil, false
	}
	return Resized(width, height), true
}

// rawMode gives a person's keys to this program, and returns how to give them back.
func rawMode(in io.Reader) (func(), error) {
	file, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(int(file.Fd()), state) }, nil
}

func readInput(ctx context.Context, in io.Reader, msgs chan<- Msg) {
	chunk := make([]byte, 64)
	for {
		n, err := in.Read(chunk)
		for _, key := range readKeys(chunk[:n]) {
			select {
			case msgs <- Pressed(key):
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func dispatch(ctx context.Context, cmd Cmd, msgs chan<- Msg) {
	cmd.Each(func(msg Msg) {
		select {
		case msgs <- msg:
		case <-ctx.Done():
		}
	})
}

// Run shows the view until a person leaves it, or the process is told to stop. The terminal comes back
// however this ends, because a shell left in raw mode prints nothing a person types.
func Run(ctx context.Context, config Config, in io.Reader, out io.Writer) error {
	restore, err := rawMode(in)
	if err != nil {
		return err
	}
	defer restore()
	running, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if _, err := io.WriteString(out, enterScreen); err != nil {
		return err
	}
	defer func() { _, _ = io.WriteString(out, leaveScreen) }()
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer signal.Stop(resized)
	msgs := make(chan Msg)
	go readInput(running, in, msgs)
	m := New(config)
	if size, ok := measured(out); ok {
		m, _ = m.Update(size)
	}
	if err := paint(out, m.View()); err != nil {
		return err
	}
	go dispatch(running, m.Init(), msgs)
	for {
		select {
		case <-running.Done():
			return nil
		case <-resized:
			if size, ok := measured(out); ok {
				m, _ = m.Update(size)
			}
		case msg := <-msgs:
			if IsQuit(msg) {
				return nil
			}
			var cmd Cmd
			m, cmd = m.Update(msg)
			if cmd != nil {
				go dispatch(running, cmd, msgs)
			}
		}
		if err := paint(out, m.View()); err != nil {
			return err
		}
	}
}

// readEvery is how often a frame reads with no event to go on. A broken stream costs this much
// latency, and it keeps every age on the screen moving.
const readEvery = 5 * time.Second

func tick() Msg {
	time.Sleep(readEvery)
	return tickMsg{}
}
