package shim

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
)

// TestARepliedCallOutlivesServing covers the order a shutdown takes: the loop answers and
// returns, and Run cancels the context while the connection's goroutine is still on its
// way to the answer.
func TestARepliedCallOutlivesServing(t *testing.T) {
	s := &server{calls: make(chan call)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	near, far := net.Pipe()
	defer far.Close()
	if err := far.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go s.serve(ctx, near)
	if !shimlink.Send(far, shimlink.Request{Version: shimlink.Version, Op: shimlink.OpShutdown}) {
		t.Fatal("the request did not go out")
	}
	taken := <-s.calls
	cancel()
	time.Sleep(20 * time.Millisecond)
	taken.answer <- shimlink.Reply{OK: true, Version: shimlink.Version}
	line, err := bufio.NewReader(far).ReadBytes('\n')
	if err != nil {
		t.Fatalf("a call the loop took got no reply: %v", err)
	}
	var reply shimlink.Reply
	if err := json.Unmarshal(line, &reply); err != nil || !reply.OK {
		t.Errorf("reply %s: %v", line, err)
	}
}

// TestShutdownEndsTheLoopAfterItsReplyLeaves covers the process: Main exits when the loop
// returns, so a reply still in memory would die with it.
func TestShutdownEndsTheLoopAfterItsReplyLeaves(t *testing.T) {
	s := &server{calls: make(chan call), ended: true}
	ended := make(chan error, 1)
	go func() { ended <- s.loop(t.Context()) }()
	shutdown := call{
		request: shimlink.Request{Version: shimlink.Version, Op: shimlink.OpShutdown},
		answer:  make(chan shimlink.Reply, 1), sent: make(chan struct{}),
	}
	s.calls <- shutdown
	if reply := <-shutdown.answer; !reply.OK {
		t.Fatalf("reply %+v", reply)
	}
	select {
	case err := <-ended:
		t.Fatalf("the loop ended before the reply left: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(shutdown.sent)
	select {
	case err := <-ended:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not end after the reply left")
	}
}
