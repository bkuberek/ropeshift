package ports

import (
	"context"

	"ropeshift.dev/ropeshift/internal/lanes"
)

// ProfileSource loads and layers TOML profiles.
type ProfileSource interface {
	// Load resolves the profile of one lane: built-in defaults, the user's layer, the
	// repository's, and the lane's own overrides, later over earlier. A layer that is not
	// there changes nothing.
	Load(ctx context.Context, repository, overrides string) (lanes.Profile, error)
}
