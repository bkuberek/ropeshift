package server

import (
	"crypto/tls"
	"fmt"
	"net"
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

// ListenNetwork binds a group server's one listener (ADR-0031). With a TLS config it binds any address, and
// everything it accepts is encrypted. With none it serves plaintext, which only loopback may carry (NFR-SEC-2),
// so it binds a loopback address and nothing else.
func ListenNetwork(address string, config *tls.Config) (net.Listener, error) {
	if config == nil {
		return listenLoopback("server listener without TLS", address)
	}
	listener, err := listenTCP(address)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(listener, config), nil
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
