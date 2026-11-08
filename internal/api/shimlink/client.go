package shimlink

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// callDeadline bounds one request with no deadline of its own.
const callDeadline = 5 * time.Second

// Client reaches one shim. Each call is one connection, so a client holds nothing that a
// shim's death can leave half used.
type Client struct {
	socket  string
	version int
	pid     int
}

// PID is the process that answered.
func (c *Client) PID() int { return c.pid }

// Dial finds the shim at a socket and agrees a version. A shim older than this build is
// served in its own version, and only a shim too old for this build is refused.
func Dial(ctx context.Context, socket string) (*Client, error) {
	client := &Client{socket: socket, version: Version}
	reply, err := client.call(ctx, Request{Op: OpHello})
	if err != nil {
		return nil, err
	}
	if client.version, err = Negotiate(Version, reply.Version); err != nil {
		return nil, err
	}
	client.pid = reply.PID
	return client, nil
}

func (c *Client) open(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return conn, nil
}

func (c *Client) call(ctx context.Context, request Request) (Reply, error) {
	if _, set := ctx.Deadline(); !set {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callDeadline)
		defer cancel()
	}
	conn, err := c.open(ctx)
	if err != nil {
		return Reply{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return Reply{}, err
		}
	}
	request.Version = c.version
	if !Send(conn, request) {
		return Reply{}, fmt.Errorf("%w: the request did not go out", ErrUnreachable)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Reply{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	var reply Reply
	if err := json.Unmarshal(line, &reply); err != nil {
		return Reply{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if !reply.OK {
		return reply, &RefusedError{Code: reply.Code, Message: reply.Message}
	}
	return reply, nil
}

// Ping is the application handshake: its nonce travels through the shim's event loop, so
// an answer proves more than a socket that accepts.
func (c *Client) Ping(ctx context.Context) error {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	nonce := hex.EncodeToString(raw)
	reply, err := c.call(ctx, Request{Op: OpPing, Nonce: nonce})
	if err != nil {
		return err
	}
	if reply.Nonce != nonce {
		return fmt.Errorf("%w: a ping came back with another nonce", ErrUnreachable)
	}
	return nil
}

// Input hands one line to the child, at most once per ID. It reports a repeat.
func (c *Client) Input(ctx context.Context, id, line string) (bool, error) {
	reply, err := c.call(ctx, Request{Op: OpInput, ID: id, Line: line})
	return reply.Duplicate, err
}

// Signal asks the shim to signal its child.
func (c *Client) Signal(ctx context.Context, name string) error {
	_, err := c.call(ctx, Request{Op: OpSignal, Signal: name})
	return err
}

// CloseStdin closes the child's stdin after what is queued.
func (c *Client) CloseStdin(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpCloseStdin})
	return err
}

// Shutdown ends a shim whose child has ended.
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpShutdown})
	return err
}

// Send writes one line of the protocol.
func Send(conn net.Conn, value any) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = conn.Write(append(raw, '\n'))
	return err == nil
}

// Stream is one attach.
type Stream struct {
	conn   net.Conn
	reader *bufio.Reader
}

// Attach follows the journal after since. It takes over from any earlier attach.
func (c *Client) Attach(ctx context.Context, since int64) (*Stream, error) {
	conn, err := c.open(ctx)
	if err != nil {
		return nil, err
	}
	if !Send(conn, Request{Version: c.version, Op: OpAttach, Since: since}) {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: the request did not go out", ErrUnreachable)
	}
	reader := bufio.NewReaderSize(conn, 1<<20)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	var reply Reply
	if err := json.Unmarshal(line, &reply); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if !reply.OK {
		_ = conn.Close()
		return nil, &RefusedError{Code: reply.Code, Message: reply.Message}
	}
	return &Stream{conn: conn, reader: reader}, nil
}

// Next returns the next record, and fails when the shim is gone or another client took
// over.
func (s *Stream) Next() (Record, error) {
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		return Record{}, errors.Join(ErrUnreachable, err)
	}
	var record Record
	err = json.Unmarshal(line, &record)
	return record, err
}

// Close ends the attach.
func (s *Stream) Close() error { return s.conn.Close() }
