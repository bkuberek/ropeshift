package fake

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// GitProbe answers with the version it holds, or its error, so the core checks the minimum with no git.
type GitProbe struct {
	// Found is the release this machine's git is.
	Found lanes.GitVersion
	// Err, when set, is a git that could not be run, and Found is not read.
	Err error
}

var _ ports.GitProbe = GitProbe{}

// Version returns the held release, or the held error.
func (p GitProbe) Version(context.Context) (lanes.GitVersion, error) {
	if p.Err != nil {
		return lanes.GitVersion{}, p.Err
	}
	return p.Found, nil
}
