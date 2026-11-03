package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// paneScript says how a session behaves in a terminal. The screens follow what RS3 read on
// 2.1.316, with the trust dialog's focus and wording as the build shows them.
type paneScript struct {
	// Trust starts on the trust dialog, once per directory.
	Trust bool `json:"trust,omitempty"`
	// Stuck draws this and never reaches a prompt.
	Stuck string `json:"stuck,omitempty"`
	// ExitOnStart ends the process before a prompt, with this code.
	ExitOnStart *int `json:"exit_on_start,omitempty"`
	// NoSessionStart keeps the SessionStart hook from running.
	NoSessionStart bool `json:"no_session_start,omitempty"`
	// SessionID replaces the session ID of the hooks.
	SessionID string `json:"session_id,omitempty"`
}

const (
	screenTrustCancel = `────────────────────────────────────────────────────────────────────────────────
 Accessing workspace:

 %[1]s

 Quick safety check: Is this a project you created or one you trust?

 ❯ No, exit
   Yes, I trust this folder

 Enter to confirm · Esc to cancel
`
	screenTrustAccept = `────────────────────────────────────────────────────────────────────────────────
 Accessing workspace:

 %[1]s

 Quick safety check: Is this a project you created or one you trust?

   No, exit
 ❯ Yes, I trust this folder

 Enter to confirm · Esc to cancel
`
	screenReady = ` fakeclaude

────────────────────────────────────────────────────────────────────────────────
❯ %[1]s
────────────────────────────────────────────────────────────────────────────────
  ? for shortcuts
`
	screenPermission = ` Bash command

   %[1]s

 Do you want to proceed?
 ❯ 1. Yes
   2. No, and tell Claude what to do differently (esc)
`
)

const (
	keyUp    = "<up>"
	keyDown  = "<down>"
	keyEnter = "<enter>"
)

// witness appends one line beside the binary, where a test reads what a pane took.
func witness(kind, line string) {
	dir := filepath.Dir(os.Args[0])
	file, err := os.OpenFile(filepath.Join(dir, kind+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail("%v", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%d %s\n", os.Getpid(), line); err != nil {
		fail("%v", err)
	}
}

func draw(text string) {
	fmt.Print("\033[2J\033[H" + strings.ReplaceAll(text, "\n", "\r\n"))
}

// terminalInput turns off line buffering, so a key arrives when it is pressed. The
// interrupt key still raises SIGINT.
func terminalInput() {
	cmd := exec.Command("stty", "-icanon", "-echo", "min", "1")
	cmd.Stdin = os.Stdin
	if out, err := cmd.CombinedOutput(); err != nil {
		fail("stty: %v: %s", err, out)
	}
}

func readKeys(keys chan<- string) {
	reader := bufio.NewReader(os.Stdin)
	for {
		first, err := reader.ReadByte()
		if err != nil {
			close(keys)
			return
		}
		switch first {
		case '\r', '\n':
			keys <- keyEnter
		case 0x1b:
			if _, err := reader.ReadByte(); err != nil {
				close(keys)
				return
			}
			arrow, err := reader.ReadByte()
			if err != nil {
				close(keys)
				return
			}
			switch arrow {
			case 'A':
				keys <- keyUp
			case 'B':
				keys <- keyDown
			}
		default:
			keys <- string(first)
		}
	}
}

func trustMark() string {
	raw := strings.NewReplacer("/", "-", " ", "-").Replace(mustGetwd())
	return filepath.Join(filepath.Dir(os.Args[0]), "trusted"+raw)
}

// askTrust is the dialog as the build shows it: the focus starts on the wrong answer, so
// a bare Enter ends the process, and a test can see that it did.
func (s *session) askTrust(keys <-chan string) {
	if _, err := os.Stat(trustMark()); err == nil {
		return
	}
	accept := false
	show := func() {
		if accept {
			draw(fmt.Sprintf(screenTrustAccept, mustGetwd()))
		} else {
			draw(fmt.Sprintf(screenTrustCancel, mustGetwd()))
		}
	}
	show()
	for key := range keys {
		witness("keys", "trust "+key)
		switch key {
		case keyDown:
			accept = true
		case keyUp:
			accept = false
		case keyEnter:
			if !accept {
				witness("bare-enter", "the wrong answer")
				os.Exit(1)
			}
			if err := os.WriteFile(trustMark(), nil, 0o600); err != nil {
				fail("%v", err)
			}
			return
		}
		show()
	}
}

// awaitEnter shows a permission prompt until a person presses Enter on it.
func (s *session) awaitEnter(command string) {
	draw(fmt.Sprintf(screenPermission, command))
	for key := range s.keys {
		witness("keys", "permission "+key)
		if key == keyEnter {
			break
		}
	}
	draw(fmt.Sprintf(screenReady, ""))
}

// listen binds the session's socket where the variable says, and takes each line.
func listen(lines chan<- string) string {
	dir := os.Getenv("CLAUDE_CODE_TMPDIR")
	if dir == "" {
		// A version that gets no private directory binds in a shared one, which a double must
		// stay out of. A test names a short directory for it.
		dir = os.Getenv("FAKECLAUDE_SOCKETS")
	}
	if dir == "" {
		dir = filepath.Dir(os.Args[0])
	}
	sockets := filepath.Join(dir, "cc-socks")
	if err := os.MkdirAll(sockets, 0o700); err != nil {
		fail("%v", err)
	}
	path := filepath.Join(sockets, strconv.Itoa(os.Getpid())+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		fail("%v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				scanner.Buffer(make([]byte, 1<<16), 1<<24)
				for scanner.Scan() {
					// A message counts when the socket takes it, so a repeat shows even if
					// the session ends before a turn would have reached it.
					witness("messages", messageID(scanner.Text()))
					lines <- scanner.Text()
				}
			}()
		}
	}()
	return path
}

func (s *session) paneSessionID() string {
	if s.script.Pane.SessionID != "" {
		return s.script.Pane.SessionID
	}
	return sessionID
}

// runPane is one session in a terminal: the start screens, a socket, and a turn for each
// message or typed prompt. It writes no stream, and everything a worker learns comes
// through the generated hooks and the status line.
func (s *session) runPane() {
	s.pane = true
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT, syscall.SIGTERM)
	terminalInput()
	keys := make(chan string)
	go readKeys(keys)
	s.keys = keys
	script := s.script.Pane
	if script.ExitOnStart != nil {
		os.Exit(*script.ExitOnStart)
	}
	if script.Stuck != "" {
		s.waitStuck(script.Stuck, keys, interrupts)
		return
	}
	if script.Trust {
		s.askTrust(keys)
	}
	messages := make(chan string)
	socket := listen(messages)
	if err := os.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket); err != nil {
		fail("%v", err)
	}
	draw(fmt.Sprintf(screenReady, ""))
	sendTelemetry("")
	if !script.NoSessionStart {
		source := "startup"
		if s.opts.resume != "" {
			source = "resume"
		}
		s.hook("SessionStart", map[string]any{"source": source, "model": s.modelName()})
	}
	s.paneLoop(keys, messages, interrupts)
}

func (s *session) modelName() string {
	if s.script.Model != "" {
		return s.script.Model
	}
	return "claude-fixture-5-5"
}

func (s *session) waitStuck(text string, keys <-chan string, interrupts <-chan os.Signal) {
	draw(text + "\n")
	for {
		select {
		case <-interrupts:
			os.Exit(130)
		case key, open := <-keys:
			if !open {
				select {}
			}
			witness("keys", "stuck "+key)
		}
	}
}

func (s *session) paneLoop(keys <-chan string, messages <-chan string, interrupts <-chan os.Signal) {
	turn := 0
	typed := ""
	take := func(text string) {
		s.hook("UserPromptSubmit", map[string]any{"prompt": text})
		if turn < len(s.script.Turns) {
			s.play(s.script.Turns[turn], interrupts)
			turn++
		}
		// A turn that ended on StopFailure has no Stop.
		if !s.failedTurn {
			s.hook("Stop", s.stopInput())
		}
		s.failedTurn = false
	}
	for {
		select {
		case <-interrupts:
			s.hook("SessionEnd", map[string]any{"reason": "other"})
			if s.server != nil {
				s.server.close()
			}
			os.Exit(0)
		case line := <-messages:
			if s.admitsPane(line) {
				take(messageText(line))
			}
		case key, open := <-keys:
			if !open {
				keys = nil
				continue
			}
			witness("keys", "prompt "+key)
			if key != keyEnter {
				typed += key
				draw(fmt.Sprintf(screenReady, typed))
				continue
			}
			text := typed
			typed = ""
			draw(fmt.Sprintf(screenReady, ""))
			switch text {
			case "":
			case "/clear":
				s.clear()
			case "/compact":
				s.compact()
			default:
				take(text)
			}
		}
	}
}

// admitsPane is the inbound policy in a pane, where `take` runs the prompt's own hook.
func (s *session) admitsPane(line string) bool {
	policy := s.script.Inbound
	if policy == "" {
		policy = s.settings.CrossSessionInbound
	}
	switch policy {
	case "refuse":
		s.receipt(line, "expired", "refused")
		return false
	case "hold":
		s.receipt(line, "held", "")
		return false
	}
	return true
}

func (s *session) compact() { s.compactBy("manual") }

type socketLine struct {
	UUID    string `json:"uuid"`
	MsgID   string `json:"msg_id"`
	From    string `json:"from"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// messageFrom reads a line's reply address, and what a receipt names it by.
func messageFrom(line string) (from, msgID string) {
	var parsed socketLine
	if json.Unmarshal([]byte(line), &parsed) != nil {
		return "", ""
	}
	return parsed.From, parsed.MsgID
}

func messageID(line string) string {
	var parsed socketLine
	if json.Unmarshal([]byte(line), &parsed) != nil {
		return "malformed"
	}
	return parsed.UUID
}

func messageText(line string) string {
	var parsed socketLine
	if json.Unmarshal([]byte(line), &parsed) != nil {
		return ""
	}
	return parsed.Message.Content
}

// statusLine runs the configured command with a payload on stdin, and keeps what it
// printed, which is what a person would see.
func (s *session) statusLine(payload map[string]any) {
	if s.settings.StatusLine.Command == "" {
		return
	}
	merged := map[string]any{"session_id": s.paneSessionID(), "model": map[string]any{"id": s.modelName()}}
	for key, value := range payload {
		merged[key] = value
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		fail("%v", err)
	}
	cmd := exec.Command("/bin/sh", "-c", s.settings.StatusLine.Command)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		fail("status line: %v", err)
	}
	witness("status-line", strings.TrimSpace(string(out)))
}
