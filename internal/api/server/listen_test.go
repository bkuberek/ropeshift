package server_test

import (
	"crypto/tls"
	"errors"
	"net"
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
			listener, err := server.ListenNetwork(address, &tls.Config{})
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
	listener, err := server.ListenNetwork("0.0.0.0:0", &tls.Config{})
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
			listener, err := server.ListenNetwork(address, nil)
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
			listener, err := server.ListenNetwork(address, nil)
			if err == nil {
				listener.Close()
				t.Fatalf("bound %s in plaintext", address)
			}
		})
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
