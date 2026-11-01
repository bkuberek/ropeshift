//go:build !realsession

package lanes

// SessionOverride is empty in a normal build. The guard flags of a real-session
// suite exist only under the realsession tag, so a release holds no way to change
// what a session starts with.
type SessionOverride struct{}

// ParseOverride reads nothing in a normal build.
func ParseOverride(string) (SessionOverride, error) { return SessionOverride{}, nil }

// Args adds nothing in a normal build.
func (SessionOverride) Args() []string { return nil }

// Env adds nothing in a normal build.
func (SessionOverride) Env() []string { return nil }

// StreamLog keeps nothing in a normal build.
func (SessionOverride) StreamLog() string { return "" }
