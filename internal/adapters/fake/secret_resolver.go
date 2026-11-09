package fake

import (
	"context"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// SecretResolver binds an `env:` reference to its name and refuses anything else.
type SecretResolver struct{}

// Resolve returns a binding and never a value.
func (SecretResolver) Resolve(_ context.Context, reference string) (ports.SecretBinding, error) {
	if reference == "" {
		return ports.SecretBinding{}, nil
	}
	name, ok := strings.CutPrefix(reference, lanes.SecretRefEnv)
	if !ok {
		return ports.SecretBinding{}, fmt.Errorf("%w: %q", lanes.ErrBadReference, reference)
	}
	return ports.SecretBinding{EnvName: name}, nil
}

var _ ports.SecretResolver = SecretResolver{}
