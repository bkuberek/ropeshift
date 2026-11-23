package claudecode_test

import (
	"context"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// A message sent by the socket is confirmed by the ID its sender chose, whether the replay keeps that ID as its `uuid`
// or, as Claude Code 2.1.330 does, gives the replay a `uuid` of its own and names the sender's ID in its origin. The
// send names a reply address, and the session's receiver is the sender the origin names.
func TestAPeerMessageIsConfirmedByTheIDItsSenderChose(t *testing.T) {
	for _, tt := range []struct{ name, shape string }{
		{"a replay that keeps the sender's uuid", ""},
		{"a replay whose origin names the sender's ID", "origin"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			driver, path, spec := session(t, "peer-messages", nil)
			editFixture(t, path, func(script map[string]any) { script["peer_shape"] = tt.shape })
			handle, err := driver.Start(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = driver.Stop(context.Background(), handle, ports.StopNow) })
			events, err := driver.Events(t.Context(), handle)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.After(20 * time.Second)
			awaitEvent := func(kind ports.EventKind, input string) {
				t.Helper()
				for {
					select {
					case event, open := <-events:
						if !open {
							t.Fatalf("the stream ended before %s", kind)
						}
						if event.Kind == kind && event.InputID == input {
							return
						}
					case <-deadline:
						t.Fatalf("no %s for input %q", kind, input)
					}
				}
			}
			awaitEvent(ports.EventTurnEnded, "")
			input := ports.Input{
				ID:   "6f1f3e0e-5b0e-4c1d-9a55-0c0a5d0b7e11",
				Text: "[ropeshift message m-1 kind=notice age=0m01s tier=fresh from=member]\nA notice.", Peer: true,
			}
			receipt, err := driver.Send(t.Context(), handle, input)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Path != ports.SendSocket || receipt.ID != input.ID {
				t.Errorf("receipt %+v", receipt)
			}
			awaitEvent(ports.EventInputAdmitted, input.ID)
		})
	}
}
