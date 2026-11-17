package server_test

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"testing"

	"ropeshift.com/ropeshift/internal/api/server"
)

// skipWithoutAddress skips a test on a machine that has no such address, such as one with no IPv6, or
// macOS, which assigns only 127.0.0.1 of the loopback range.
func skipWithoutAddress(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT) {
		t.Skip("this machine has no such address")
	}
}

// A group server's listener binds the address it is given, and a TLS config is what lets that be any
// address: whatever the listener accepts is then encrypted.
func TestTheNetworkListenerWithTLSBindsAnyAddress(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1:0", "localhost:0", "0.0.0.0:0", ":0", "[::]:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := server.ListenNetwork(address, server.Transport{TLS: &tls.Config{}})
			if err != nil {
				skipWithoutAddress(t, err)
				t.Fatal(err)
			}
			defer listener.Close()
			if _, _, err := net.SplitHostPort(listener.Addr().String()); err != nil {
				t.Error(err)
			}
		})
	}
	listener, err := server.ListenNetwork("0.0.0.0:0", server.Transport{TLS: &tls.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if ip := listener.Addr().(*net.TCPAddr).IP; !ip.IsUnspecified() {
		t.Errorf("0.0.0.0 bound %s", ip)
	}
}

// With no TLS config the listener is plaintext, which only loopback may be (NFR-SEC-2), so it binds nothing
// else, whatever the caller meant.
func TestTheNetworkListenerWithoutTLSBindsLoopbackOnly(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0", "127.1.2.3:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := server.ListenNetwork(address, server.Transport{})
			if err != nil {
				skipWithoutAddress(t, err)
				t.Fatal(err)
			}
			defer listener.Close()
			if ip := listener.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
				t.Errorf("bound %s", ip)
			}
		})
	}
	for _, address := range []string{"0.0.0.0:0", "[::]:0", ":0", "192.168.1.2:0", "10.0.0.1:0", "attacker.example:80", "127.0.0.1.attacker.example:80", "no port"} {
		t.Run(address, func(t *testing.T) {
			listener, err := server.ListenNetwork(address, server.Transport{})
			if err == nil {
				listener.Close()
				t.Fatalf("bound %s in plaintext", address)
			}
		})
	}
}

// Behind a proxy that terminates TLS, plaintext is allowed off loopback, and only because proxies are listed.
func TestThePlaintextListenerBindsAnyAddressWhenProxiesAreListed(t *testing.T) {
	t.Parallel()
	proxies, err := server.ParseProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"0.0.0.0:0", ":0", "127.0.0.1:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := server.ListenNetwork(address, server.Transport{Proxies: proxies})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
		})
	}
	none, err := server.ParseProxies(nil)
	if err != nil {
		t.Fatal(err)
	}
	if listener, err := server.ListenNetwork("0.0.0.0:0", server.Transport{Proxies: none}); err == nil {
		listener.Close()
		t.Error("an empty list of proxies bound a plaintext listener off loopback")
	}
}

// fakeConn is a connection from a peer, which counts what is read from it and says when it is closed.
type fakeConn struct {
	net.Conn
	remote net.Addr
	mu     sync.Mutex
	closed bool
	reads  int
}

func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }

func (c *fakeConn) Read([]byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return 0, io.EOF
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) state() (closed bool, reads int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed, c.reads
}

// fakeListener hands out the connections it is given, one for each Accept, and is closed when they are gone.
type fakeListener struct {
	conns chan net.Conn
}

func (l *fakeListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *fakeListener) Close() error   { return nil }
func (l *fakeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4zero, Port: 7400} }

func peer(address string) *fakeConn {
	return &fakeConn{remote: net.TCPAddrFromAddrPort(netip.MustParseAddrPort(address))}
}

// A plaintext connection from an address that is neither loopback nor listed is closed as it is accepted, before
// anything is read from it, so the server never sees a credential or a body from it. A peer that is one of ours
// goes through.
func TestAPlaintextConnectionFromAPeerThatIsNotOursIsClosedBeforeAnythingIsRead(t *testing.T) {
	t.Parallel()
	proxies, err := server.ParseProxies([]string{"10.0.0.0/8", "fd00::/16"})
	if err != nil {
		t.Fatal(err)
	}
	ours := []*fakeConn{peer("10.0.0.2:4000"), peer("127.0.0.1:4000"), peer("[::1]:4000"), peer("[::ffff:10.0.0.3]:4000"), peer("[fd00::9]:4000"), peer("127.1.2.3:4000")}
	strangers := []*fakeConn{
		peer("203.0.113.9:4000"), peer("11.0.0.1:4000"), peer("[2001:db8::1]:4000"), peer("192.168.1.5:4000"), peer("[fe80::1]:4000"),
		{remote: &net.UnixAddr{Name: "a", Net: "unix"}}, {remote: nil},
	}
	conns := make(chan net.Conn, len(ours)+len(strangers))
	// A stranger comes before and between ours, so a filter that stopped at the first would show.
	order := []*fakeConn{strangers[0], ours[0], strangers[1], strangers[2], ours[1], ours[2], strangers[3], ours[3], strangers[4], strangers[5], strangers[6], ours[4], ours[5]}
	for _, conn := range order {
		conns <- conn
	}
	close(conns)
	filtered := server.FilterPeers(&fakeListener{conns: conns}, proxies)
	var accepted []net.Conn
	for {
		conn, err := filtered.Accept()
		if err != nil {
			break
		}
		accepted = append(accepted, conn)
	}
	if len(accepted) != len(ours) {
		t.Fatalf("%d connections were taken, and %d are ours", len(accepted), len(ours))
	}
	for i, conn := range ours {
		if accepted[i] != net.Conn(conn) {
			t.Errorf("connection %d taken is not the one of ours that was expected", i)
		}
		if closed, _ := conn.state(); closed {
			t.Errorf("a peer of ours, %v, was closed", conn.remote)
		}
	}
	for _, conn := range strangers {
		if closed, reads := conn.state(); !closed || reads != 0 {
			t.Errorf("the peer %v was closed %v after %d reads, and wants closed with none", conn.remote, closed, reads)
		}
	}
}

// With no proxy listed, only loopback is let in, so a plaintext listener that was bound wider than it should
// have been, by a mistake of the caller's, still serves nothing to another machine.
func TestWithNoProxiesListedOnlyLoopbackIsLetIn(t *testing.T) {
	t.Parallel()
	conns := make(chan net.Conn, 4)
	for _, conn := range []*fakeConn{peer("10.0.0.2:4000"), peer("127.0.0.1:4000"), peer("[2001:db8::1]:4000")} {
		conns <- conn
	}
	close(conns)
	filtered := server.FilterPeers(&fakeListener{conns: conns}, nil)
	conn, err := filtered.Accept()
	if err != nil || conn.RemoteAddr().String() != "127.0.0.1:4000" {
		t.Fatalf("the first connection taken is %v, %v", conn, err)
	}
	if conn, err := filtered.Accept(); err == nil {
		t.Errorf("a second connection was taken: %v", conn.RemoteAddr())
	}
}

func TestLoopbackNamesTheAddressesThatNeedNoTLS(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"127.0.0.1:7400": true, "127.0.0.1:0": true, "localhost:7400": true, "[::1]:7400": true, "127.1.2.3:80": true,
		"0.0.0.0:7400": false, "[::]:7400": false, ":7400": false, "192.168.1.5:7400": false, "10.0.0.1:7400": false,
		"server.example:7400": false, "127.0.0.1.example:7400": false, "LOCALHOST:7400": false, "no port": false, "": false,
	}
	for address, want := range tests {
		if got := server.Loopback(address); got != want {
			t.Errorf("Loopback(%q) is %v", address, got)
		}
	}
}
