package fake

import (
	"context"
	"sync"

	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// ProfileSource holds repository layers in memory, and layers them with the code
// the filesystem source uses.
type ProfileSource struct {
	mu     sync.Mutex
	layers map[string]string
}

// NewProfileSource returns a source with no layers.
func NewProfileSource() *ProfileSource {
	return &ProfileSource{layers: map[string]string{}}
}

// Put stands for a repository's layer.
func (s *ProfileSource) Put(repository, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.layers[repository] = text
}

// Load resolves one repository's profile.
func (s *ProfileSource) Load(_ context.Context, repository string) (lanes.Profile, error) {
	s.mu.Lock()
	text, ok := s.layers[repository]
	s.mu.Unlock()
	if !ok {
		return profiles.Resolve()
	}
	return profiles.Resolve(text)
}

var _ ports.ProfileSource = (*ProfileSource)(nil)
