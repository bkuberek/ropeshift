package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/api"
)

// privateDir and privateSocket keep a socket to its owner (NFR-SEC-2).
const (
	privateDir    = 0o700
	privateSocket = 0o600
)

// ListenWeb binds the web listener, on a loopback address and nothing else, because solo mode binds no
// public listener.
func ListenWeb(address string) (net.Listener, error) { return listenLoopback("web listener", address) }

// Transport is how a group server's listener is secured (ADR-0031): by TLS, or, with no TLS config, by serving
// plaintext to loopback and to the proxies it lists.
type Transport struct {
	TLS     *tls.Config
	Proxies *Proxies
}

// ListenNetwork binds a group server's one listener. With a TLS config it binds any address, and everything it
// accepts is encrypted. With none it serves plaintext, which only loopback and a reverse proxy that terminates TLS
// may carry (NFR-SEC-2): it binds a loopback address and nothing else unless proxies are listed, and it takes a
// connection only from loopback and from a listed proxy, and closes any other before a byte is read.
func ListenNetwork(address string, transport Transport) (net.Listener, error) {
	if transport.TLS == nil && transport.Proxies.Len() == 0 {
		return listenLoopback("server listener without TLS", address)
	}
	listener, err := listenTCP(address)
	if err != nil {
		return nil, err
	}
	if transport.TLS != nil {
		return tls.NewListener(listener, transport.TLS), nil
	}
	return FilterPeers(listener, transport.Proxies), nil
}

// FilterPeers is a listener that takes a connection from loopback and from a listed proxy, and closes any other
// at once, before anything is read from it, so a peer that is not one of ours gets no part of the server, a
// credential or a body included.
func FilterPeers(listener net.Listener, proxies *Proxies) net.Listener {
	return &peerFilter{Listener: listener, proxies: proxies}
}

type peerFilter struct {
	net.Listener
	proxies *Proxies
}

// Accept returns the next connection from a peer that is ours. A peer whose address cannot be read is no peer
// of ours, and a connection that is not ours is closed at once, with nothing read, and the next is awaited.
func (f *peerFilter) Accept() (net.Conn, error) {
	for {
		conn, err := f.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if f.ours(conn.RemoteAddr()) {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func (f *peerFilter) ours(remote net.Addr) bool {
	if remote == nil {
		return false
	}
	pair, err := netip.ParseAddrPort(remote.String())
	if err != nil {
		return false
	}
	peer := clean(pair.Addr())
	return peer.IsLoopback() || f.proxies.Contains(peer)
}

// Loopback says an address names this machine alone. A name other than `localhost` could resolve anywhere, so
// only literal addresses count.
func Loopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ListenTelemetry binds the worker's OTLP receiver, on a loopback address and nothing else. An agent's
// OTLP exporter speaks HTTP to a host and port, and cannot reach a unix socket, so the receiver is the
// second TCP listener the design names (FR-OBS-3).
func ListenTelemetry(address string) (net.Listener, error) {
	return listenLoopback("telemetry receiver", address)
}

func listenLoopback(what, address string) (net.Listener, error) {
	if !Loopback(address) {
		return nil, fmt.Errorf("the %s binds a loopback address, and %q is none", what, address)
	}
	return listenTCP(address)
}

// listenTCP is the one TCP bind in the module.
func listenTCP(address string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return net.Listen("tcp", net.JoinHostPort(host, port))
}

// PrivateDir creates a directory only its owner can enter. It tightens one that
// already exists, because an earlier version or a person may have made it wider.
func PrivateDir(dir string) error {
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return err
	}
	return os.Chmod(dir, privateDir)
}

// Listen binds a unix socket in a private directory. The directory protects the
// moment between the bind and the chmod. The caller holds the installation's lock,
// so a socket already at the path is a leftover from a process that died.
func Listen(socket string) (net.Listener, error) {
	if len(socket) > api.MaxSocketPath {
		return nil, api.SocketPathTooLong(socket)
	}
	if err := PrivateDir(filepath.Dir(socket)); err != nil {
		return nil, err
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, privateSocket); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}
