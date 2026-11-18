package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
)

// GitProbe reads which git this machine runs, for the check that holds it to the minimum ropeshift runs on.
type GitProbe interface {
	// Version says the release of the git on this machine. An error says git could not be run, or printed no
	// version, and never a release that was guessed.
	Version(ctx context.Context) (lanes.GitVersion, error)
}
