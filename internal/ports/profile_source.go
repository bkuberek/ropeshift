package ports

import (
	"context"

	"ropeshift.dev/ropeshift/internal/lanes"
)

// ProfileSource loads and layers TOML profiles.
type ProfileSource interface {
	// Load resolves the profile of one repository: built-in defaults, then the
	// repository's layer. A repository with no layer gets the defaults.
	Load(ctx context.Context, repository string) (lanes.Profile, error)
}
