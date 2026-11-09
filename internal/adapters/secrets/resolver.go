// Package secrets is the SecretResolver for a worker. It turns a reference into a
// binding the agent CLI reads, and no code path in it reads a value (D9).
package secrets

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// SecretRefKeychain is the form of a reference that names an OS keychain entry.
const SecretRefKeychain = "keychain:"

// Resolver resolves `env:` and `keychain:` references. `k8s:` belongs to cluster
// mode.
type Resolver struct {
	// GOOS picks the keychain's command. Empty means this machine.
	GOOS string
}

// Resolve returns a binding and never a value. An `env:` reference names a variable
// the allowlist passes through, and a `keychain:` reference generates a command the
// CLI runs for itself.
func (r Resolver) Resolve(_ context.Context, reference string) (ports.SecretBinding, error) {
	if reference == "" {
		return ports.SecretBinding{}, nil
	}
	if name, ok := strings.CutPrefix(reference, lanes.SecretRefEnv); ok && name != "" {
		return ports.SecretBinding{EnvName: name}, nil
	}
	if name, ok := strings.CutPrefix(reference, SecretRefKeychain); ok && name != "" {
		helper, err := r.helper(name)
		return ports.SecretBinding{APIKeyHelper: helper}, err
	}
	return ports.SecretBinding{}, fmt.Errorf("%w: %q", lanes.ErrBadReference, reference)
}

func (r Resolver) helper(name string) (string, error) {
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "darwin":
		return "/usr/bin/security find-generic-password -s " + lanes.ShellQuote(name) + " -w", nil
	case "linux":
		return "secret-tool lookup service " + lanes.ShellQuote(name), nil
	}
	return "", fmt.Errorf("%w: no keychain on %s", lanes.ErrBadReference, goos)
}

var _ ports.SecretResolver = Resolver{}
