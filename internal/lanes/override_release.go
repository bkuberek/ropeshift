//go:build !realsession

package lanes

import "time"

// ReadsVendorStatus is true in a normal build. A real-session build reads no status.
const ReadsVendorStatus = true

// SessionOverride is empty in a normal build. The guard flags of a real-session
// suite exist only under the realsession tag, so a release holds no way to change
// what a session starts with.
type SessionOverride struct{}

// ParseOverride reads nothing in a normal build.
func ParseOverride(string) (SessionOverride, error) { return SessionOverride{}, nil }

// Args adds nothing in a normal build.
func (SessionOverride) Args() []string { return nil }

// PaneArgs adds nothing in a normal build.
func (SessionOverride) PaneArgs() []string { return nil }

// Env adds nothing in a normal build.
func (SessionOverride) Env() []string { return nil }

// StreamLog keeps nothing in a normal build.
func (SessionOverride) StreamLog() string { return "" }

// Inbound is what a lane is generated with, in a normal build.
func (SessionOverride) Inbound(generated string) string { return generated }

// CacheLifetime is what a driver documents, in a normal build.
func (SessionOverride) CacheLifetime(documented time.Duration) time.Duration { return documented }
