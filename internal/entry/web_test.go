package entry

import (
	"io"
	"net"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// A web listener asked for a port the kernel picks reports the port it bound, which the daemon's health
// says, so a command names a page where the web UI listens and not at port 0.
func TestTheWebListenerSaysTheAddressItBound(t *testing.T) {
	listener, err := listenWeb("127.0.0.1:0", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	host, port, err := net.SplitHostPort(webAddressOf(listener))
	if err != nil || host != "127.0.0.1" || port == "" || port == "0" {
		t.Errorf("the listener says %q: %v", webAddressOf(listener), err)
	}
}

func TestAWebUIThatIsOffHasNoAddress(t *testing.T) {
	listener, err := listenWeb(api.WebOff, io.Discard)
	if err != nil || listener != nil {
		t.Fatalf("listener %v: %v", listener, err)
	}
	if address := webAddressOf(listener); address != "" {
		t.Errorf("a web UI that is off says %q", address)
	}
}
