package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
)

// ProfileSource loads and layers TOML profiles.
type ProfileSource interface {
	// Load resolves the profile of one lane: built-in defaults, the user's layer, the
	// repository's, and the lane's own overrides, later over earlier. A layer that is not
	// there changes nothing.
	Load(ctx context.Context, repository, overrides string) (lanes.Profile, error)
}

// UserProfile is the user's own layer on the machine of the worker this daemon runs, which holds that
// machine's lane caps, so the web UI, the CLI, and the file are one setting (FR-PROJ-7).
type UserProfile interface {
	// LaneCaps reads the caps the layer sets.
	LaneCaps(ctx context.Context) (lanes.LaneCapSettings, error)
	// SetLaneCaps writes them into the layer's [admission] table, keeps every other line, and replaces
	// the layer whole. A layer that does not parse, or that sets its caps where an edit cannot reach, is
	// refused and left as it was.
	SetLaneCaps(ctx context.Context, caps lanes.LaneCapSettings) error
}
