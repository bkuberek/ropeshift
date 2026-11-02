package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/ports"
)

// The fields of a receipt, as the installed build writes them. The line is undocumented, so it is
// a qualified surface: this code runs only on a version the matrix pins.
const (
	receiptType    = "control"
	receiptAction  = "peer_message_status"
	receiptExpired = "expired"
	detailRefused  = "refused"
	// receiptLineMax bounds one receipt.
	receiptLineMax = 1 << 16
	// receiptReadDeadline bounds what one receipt's connection can hold open.
	receiptReadDeadline = time.Second
)

// socketsDir is where a session binds its socket, inside CLAUDE_CODE_TMPDIR. The receiver answers
// a reply address only there.
const socketsDir = "cc-socks"

// receiptLine holds the fields of a receipt this driver reads.
type receiptLine struct {
	Type         string `json:"type"`
	Action       string `json:"action"`
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail"`
	OrigMsgID    string `json:"orig_msg_id"`
}

// refusedInput reads one line of a receipt, and names the input it says the session refused. A
// `held` receipt names nothing, because a held message stays `handed_off` until it expires.
func refusedInput(raw []byte) string {
	var line receiptLine
	if json.Unmarshal(raw, &line) != nil || line.Type != receiptType || line.Action != receiptAction {
		return ""
	}
	if line.Status != receiptExpired || line.StatusDetail != detailRefused {
		return ""
	}
	return line.OrigMsgID
}

// receiver is the socket a session's receipts come back to. It names an address of its own in the
// session's socket directory, which is the only place the session answers.
type receiver struct {
	listener net.Listener
	path     string
	done     sync.WaitGroup
}

func receiptPath(spec ports.SessionSpec) string {
	return filepath.Join(spec.TempDir, socketsDir, "r"+strconv.FormatInt(spec.Generation, 10)+".sock")
}

// openReceiver binds the address and hands each refusal to take. A version the matrix does not pin
// gets none, so `refused` is never read on a surface that passed no check, and a session with no
// private directory has none it can answer. It replaces what a killed process left at that path.
func openReceiver(spec ports.SessionSpec, qualified bool, take func(input string)) (*receiver, error) {
	if !qualified || spec.TempDir == "" {
		return nil, nil
	}
	path := receiptPath(spec)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	r := &receiver{listener: listener, path: path}
	r.done.Go(func() { r.serve(take) })
	return r, nil
}

func (r *receiver) serve(take func(string)) {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		r.done.Go(func() {
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(receiptReadDeadline))
			scanner := bufio.NewScanner(conn)
			scanner.Buffer(make([]byte, 1<<12), receiptLineMax)
			for scanner.Scan() {
				if input := refusedInput(scanner.Bytes()); input != "" {
					take(input)
				}
			}
		})
	}
}

// from is the reply address a line names, and empty when nothing listens.
func (r *receiver) from() string {
	if r == nil {
		return ""
	}
	return "uds:" + r.path
}

// close stops listening and waits for what was being read.
func (r *receiver) close() {
	if r == nil {
		return
	}
	_ = r.listener.Close()
	r.done.Wait()
}

// socketLine is one message on a session's socket. The driver builds it from a typed value, and
// the shape is a qualified surface: the documentation names only the auth line.
type socketLine struct {
	Type     string       `json:"type"`
	Message  inputMessage `json:"message"`
	Priority string       `json:"priority"`
	UUID     string       `json:"uuid"`
	MsgID    string       `json:"msg_id,omitempty"`
	From     string       `json:"from,omitempty"`
}

// writeSocket writes one line to a socket only a hook or an init of this session could have named,
// whose name carries the process that owns it. The write proves only that the socket took it.
func writeSocket(ctx context.Context, name, socket string, pid int, in ports.Input, from string) (ports.SendReceipt, error) {
	if socket == "" {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: no socket", name, ports.ErrNoSession)
	}
	if pid != 0 && filepath.Base(socket) != strconv.Itoa(pid)+".sock" {
		return ports.SendReceipt{}, fmt.Errorf("%s: the socket does not name this session's process", name)
	}
	if in.ID == "" {
		id, err := newUUID()
		if err != nil {
			return ports.SendReceipt{}, err
		}
		in.ID = id
	}
	line := socketLine{Type: "user", Message: inputMessage{Role: "user", Content: in.Text}, Priority: socketPriority, UUID: in.ID, From: from}
	if from != "" {
		line.MsgID = in.ID
	}
	raw, err := json.Marshal(line)
	if err != nil {
		return ports.SendReceipt{}, err
	}
	var dialer net.Dialer
	attempt, cancel := context.WithTimeout(ctx, socketDeadline)
	defer cancel()
	conn, err := dialer.DialContext(attempt, "unix", socket)
	if err != nil {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: %v", name, ports.ErrNoSession, err)
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(socketDeadline)); err != nil {
		return ports.SendReceipt{}, err
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: %v", name, ports.ErrNoSession, err)
	}
	return ports.SendReceipt{ID: in.ID, Path: ports.SendSocket}, nil
}
