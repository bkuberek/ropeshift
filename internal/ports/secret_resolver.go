package ports

import "context"

// SecretBinding says how the agent CLI reads a key. It names where the key is and
// never holds it (D9): no ropeshift code path reads the value, and this type has no
// field that could carry one.
type SecretBinding struct {
	// EnvName is a variable the lane's allowlist passes through.
	EnvName string
	// APIKeyHelper is a command the CLI runs to read the key. Ropeshift writes it
	// into the generated settings and never runs it.
	APIKeyHelper string
}

// SecretResolver turns a secret reference into a binding where it is used.
type SecretResolver interface {
	// Resolve returns the binding of one reference. An empty reference is a lane
	// with no key, which binds nothing.
	Resolve(ctx context.Context, reference string) (SecretBinding, error)
}
