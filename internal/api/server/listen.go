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
