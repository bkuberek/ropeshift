package localauth

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The helper processes of the terminal tests find their part here: envShell makes the test binary a
// shell that leads a terminal's session, and envPrompt the program it runs, which asks with that prompt.
// envPadding says how many times the prompt starts with padding, because Linux holds one variable to
// 128 KiB, and a prompt longer than a terminal holds is longer than that.
const (
	envShell   = "ROPESHIFT_TEST_PASSPHRASE_SHELL"
	envPrompt  = "ROPESHIFT_TEST_PASSPHRASE_PROMPT"
	envPadding = "ROPESHIFT_TEST_PASSPHRASE_PADDING"
	padding    = "a long prompt "
)

// asking is a person's terminal, with a shell that runs a program asking for a passphrase. The shell
// leads the terminal's session, as a person's does, so the terminal outlives the program.
type asking struct {
	t       *testing.T
	master  *os.File
	slave   *os.File
	shell   *exec.Cmd
	program int
	said    *bufio.Reader
	exited  chan struct{}
	shown   *bytes.Buffer
	drained chan struct{}
	reading sync.Once
	mu      sync.Mutex
}

// ask starts the shell on a new pseudo-terminal, which is its controlling terminal. The program's
// prompt is padded that many times before the prompt itself.
func ask(t *testing.T, padded int, prompt string) *asking {
	t.Helper()
	master, slave := openPTY(t)
	shell := exec.Command(os.Args[0], "-test.run=^TestHelperRunsAProgram$")
	shell.Env = append(os.Environ(), envShell+"=1", envPrompt+"="+prompt, envPadding+"="+strconv.Itoa(padded))
	shell.Stdin = slave
	shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	a := &asking{
		t: t, master: master, slave: slave, shell: shell, said: bufio.NewReader(out),
		exited: make(chan struct{}), shown: &bytes.Buffer{}, drained: make(chan struct{}),
	}
	go func() {
		_ = shell.Wait()
		close(a.exited)
	}()
	// A process that ends with output its terminal never delivered waits for it, so the terminal is
	// read before anything is killed.
	t.Cleanup(func() {
		a.read()
		if a.program != 0 {
			_ = syscall.Kill(a.program, syscall.SIGKILL)
		}
		_ = shell.Process.Kill()
		<-a.exited
		_ = slave.Close()
		a.stopReading()
		_ = master.Close()
	})
	pid, err := strconv.Atoi(strings.TrimPrefix(a.line(), "program "))
	if err != nil {
		t.Fatal(err)
	}
	a.program = pid
	return a
}

// line reads what the shell says next.
func (a *asking) line() string {
	a.t.Helper()
	line, err := a.said.ReadString('\n')
	if err != nil {
		a.t.Fatalf("the shell said %q: %v", line, err)
	}
	return strings.TrimSuffix(line, "\n")
}

// read keeps what the terminal shows, as a person would see it.
func (a *asking) read() {
	a.reading.Do(func() {
		go func() {
			defer close(a.drained)
			chunk := make([]byte, 64<<10)
			for {
				n, err := a.master.Read(chunk)
				a.mu.Lock()
				a.shown.Write(chunk[:n])
				a.mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
	})
}

func (a *asking) stopReading() {
	select {
	case <-a.drained:
	case <-time.After(5 * time.Second):
	}
}

func (a *asking) screen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.shown.String()
}

// echoes reads whether the terminal echoes what is typed now.
func (a *asking) echoes() bool {
	a.t.Helper()
	termios, err := termiosOf(a.slave)
	if err != nil {
		a.t.Fatal(err)
	}
	return termios.Lflag&unix.ECHO != 0
}

// awaitQuiet waits for the program to turn echo off. It fails when that has not come in a bound,
// which leaves room for a busy machine.
func (a *asking) awaitQuiet(what string) {
	a.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for a.echoes() {
		if time.Now().After(deadline) {
			a.t.Fatal(what)
		}
		time.Sleep(time.Millisecond)
	}
}

func termiosOf(f *os.File) (*unix.Termios, error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	var termios *unix.Termios
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) { termios, ioctlErr = unix.IoctlGetTermios(int(fd), ioctlReadTermios) }); err != nil {
		return nil, err
	}
	return termios, ioctlErr
}

// Input typed once the prompt shows must not show. The prompt here is longer than a terminal holds,
// so the program is still writing it until the test reads it: echo must be off by then.
func TestEchoIsOffBeforeThePromptShows(t *testing.T) {
	a := ask(t, 1<<16, "Passphrase: ")
	a.awaitQuiet("the prompt was on its way with echo still on")
	a.read()
	if _, err := a.master.Write([]byte("hunter2\n")); err != nil {
		t.Fatal(err)
	}
	if got := a.line(); got != "got hunter2" {
		t.Errorf("the program read %q", got)
	}
	if ended := a.line(); ended != "ended exit status 0" {
		t.Errorf("the program %s", ended)
	}
	if !a.echoes() {
		t.Error("echo stayed off after the passphrase")
	}
	if strings.Contains(a.screen(), "hunter2") {
		t.Error("the passphrase showed on the terminal")
	}
}

// An interrupt ends the program as it always did, and the terminal echoes again after it.
func TestAnInterruptPutsEchoBack(t *testing.T) {
	a := ask(t, 0, "Passphrase: ")
	a.read()
	a.awaitQuiet("echo never went off")
	if _, err := a.master.Write([]byte{0x03}); err != nil {
		t.Fatal(err)
	}
	if ended := a.line(); ended != "ended signal: interrupt" {
		t.Errorf("the program %s", ended)
	}
	if !a.echoes() {
		t.Error("an interrupt left the terminal with echo off")
	}
}

// TestHelperRunsAProgram is the person's shell. It runs the program as the terminal's foreground job,
// says its PID and how it ended, and waits to be killed.
func TestHelperRunsAProgram(t *testing.T) {
	if os.Getenv(envShell) == "" {
		t.Skip("a helper of the terminal tests")
	}
	program := exec.Command(os.Args[0], "-test.run=^TestHelperAsksForAPassphrase$")
	program.Env = append(os.Environ(), envShell+"=")
	program.Stdin, program.Stdout = os.Stdin, os.Stdout
	program.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Ctty: 0}
	if err := program.Start(); err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("program %d\n", program.Process.Pid)
	_ = program.Wait()
	fmt.Printf("ended %s\n", program.ProcessState)
	time.Sleep(time.Hour)
}

// TestHelperAsksForAPassphrase is the person's program: it asks on its terminal and says what it read.
func TestHelperAsksForAPassphrase(t *testing.T) {
	prompt, ok := os.LookupEnv(envPrompt)
	if !ok || os.Getenv(envShell) != "" {
		t.Skip("a helper of the terminal tests")
	}
	padded, err := strconv.Atoi(os.Getenv(envPadding))
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	passphrase, err := ReadPassphrase(strings.Repeat(padding, padded) + prompt)
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("got %s\n", passphrase)
	os.Exit(0)
}
