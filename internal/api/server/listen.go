package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"ropeshift.dev/ropeshift/internal/api"
)

// privateDir and privateSocket keep a socket to its owner (NFR-SEC-2).
const (
	privateDir    = 0o700
	privateSocket = 0o600
)

// ListenWeb binds the web listener, on a loopback address and nothing else, because solo mode binds no
// public listener.
func ListenWeb(address string) (net.Listener, error) { return listenLoopback("web listener", address) }

// ListenTelemetry binds the worker's OTLP receiver, on a loopback address and nothing else. An agent's
// OTLP exporter speaks HTTP to a host and port, and cannot reach a unix socket, so the receiver is the
// second TCP listener the design names (FR-OBS-3).
func ListenTelemetry(address string) (net.Listener, error) {
	return listenLoopback("telemetry receiver", address)
}

// listenLoopback is the one TCP bind in the module. A name other than `localhost` could resolve
// anywhere, so only literal addresses count.
func listenLoopback(what, address string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("the %s binds a loopback address, and %q is none", what, address)
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
		return nil, fmt.Errorf("socket path is over %d bytes: %s", api.MaxSocketPath, socket)
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
