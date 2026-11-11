package top_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/top"
	"ropeshift.com/ropeshift/test/apitest"
)

// lockedBuffer is a terminal a test reads while a program writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.String()
}

func (b *lockedBuffer) waitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%q never reached the screen:\n%s", text, b.String())
}

// The whole program, on a real daemon: a change in the ledger reaches the screen by the stream, well
// inside the time a poll would take, and a key leaves.
func TestALiveFrameFollowsTheLedger(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Running("wo-first", "Rotate the keys").End("blocked", "no_outcome")
	client := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer client.Close()
	config := top.Config{Read: top.Reader(client), Follow: client.Events, Now: time.Now}
	keys, press := io.Pipe()
	defer press.Close()
	var screen lockedBuffer
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- top.Run(ctx, config, keys, &screen) }()
	screen.waitFor(t, "wo-first")

	w.Running("wo-second", "Move the archive").End("blocked", "no_outcome")
	screen.waitFor(t, "wo-second")

	if _, err := press.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("q did not leave")
	}
}
