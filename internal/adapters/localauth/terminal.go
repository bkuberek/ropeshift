package localauth

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// controllingTerminal is the process's own terminal, whatever its standard streams are.
	controllingTerminal = "/dev/tty"
	// signalLanding bounds the wait for a signal this process sends itself to end it.
	signalLanding = time.Second
)

// ErrNoTerminal means this process has no terminal to ask on.
var ErrNoTerminal = errors.New("no terminal to ask on")

// ErrInterrupted means a signal ended the question, in a process that outlived it.
var ErrInterrupted = errors.New("the question was interrupted")

// ReadPassphrase asks on the controlling terminal, with echo off. It is the only source of
// a passphrase: a pipe, a variable, and an argument can all be written by a lane, or read
// from a process list or a shell's history. Echo goes off before the prompt shows, so nothing
// typed once it shows is echoed, and the terminal gets its echo back however the question ends.
func ReadPassphrase(prompt string) (string, error) {
	tty, err := os.OpenFile(controllingTerminal, os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	defer tty.Close()
	signals := heed()
	defer signal.Stop(signals)
	restore, err := echoOff(tty)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	defer restore()
	type answer struct {
		line []byte
		err  error
	}
	answered := make(chan answer, 1)
	// The prompt goes out with the read, so a terminal that holds its output back cannot keep a
	// signal from being heard.
	go func() {
		if _, err := io.WriteString(tty, prompt); err != nil {
			answered <- answer{err: err}
			return
		}
		line, err := readLine(tty)
		answered <- answer{line: line, err: err}
	}()
	select {
	case got := <-answered:
		restore()
		_, _ = fmt.Fprintln(tty)
		if got.err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoTerminal, got.err)
		}
		return string(got.line), nil
	case received := <-signals:
		restore()
		// The signal does what it would have done, now the terminal is back. It reaches this
		// process on a thread of its own, and a process that listens for it elsewhere goes on.
		signal.Stop(signals)
		if sig, ok := received.(syscall.Signal); ok {
			_ = syscall.Kill(os.Getpid(), sig)
			time.Sleep(signalLanding)
		}
		return "", ErrInterrupted
	}
}

// heed listens for the signals that end a process from its terminal or its session, other than
// those the process was started to ignore.
func heed() chan os.Signal {
	signals := make(chan os.Signal, 1)
	var heeded []os.Signal
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT} {
		if !signal.Ignored(sig) {
			heeded = append(heeded, sig)
		}
	}
	if len(heeded) != 0 {
		signal.Notify(signals, heeded...)
	}
	return signals
}

// echoOff turns a terminal's echo off, and returns what puts it back, which is safe to call again.
func echoOff(tty *os.File) (func(), error) {
	conn, err := tty.SyscallConn()
	if err != nil {
		return nil, err
	}
	var before *unix.Termios
	var ioctlErr error
	err = conn.Control(func(fd uintptr) {
		before, ioctlErr = unix.IoctlGetTermios(int(fd), ioctlReadTermios)
		if ioctlErr != nil {
			return
		}
		quiet := *before
		quiet.Lflag &^= unix.ECHO
		quiet.Lflag |= unix.ICANON | unix.ISIG
		quiet.Iflag |= unix.ICRNL
		ioctlErr = unix.IoctlSetTermios(int(fd), ioctlWriteTermios, &quiet)
	})
	if err == nil {
		err = ioctlErr
	}
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = conn.Control(func(fd uintptr) { _ = unix.IoctlSetTermios(int(fd), ioctlWriteTermios, before) })
		})
	}, nil
}

// readLine reads up to a line feed, one byte at a time so nothing past the line is taken, and
// applies a backspace the terminal passed on.
func readLine(r io.Reader) ([]byte, error) {
	var one [1]byte
	var line []byte
	for {
		n, err := r.Read(one[:])
		if n > 0 {
			switch one[0] {
			case '\b':
				if len(line) > 0 {
					line = line[:len(line)-1]
				}
			case '\n':
				return line, nil
			case '\r':
				// The terminal maps a return to the line feed that ends the line.
			default:
				line = append(line, one[0])
			}
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return line, err
		}
	}
}
