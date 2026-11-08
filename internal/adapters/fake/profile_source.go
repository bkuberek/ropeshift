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
	user   string
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

// PutUser stands for the user's layer.
func (s *ProfileSource) PutUser(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = text
}

// Load resolves one lane's profile.
func (s *ProfileSource) Load(_ context.Context, repository, overrides string) (lanes.Profile, error) {
	s.mu.Lock()
	text, user := s.layers[repository], s.user
	s.mu.Unlock()
	return profiles.ResolveLayers(user, text, overrides)
}

var _ ports.ProfileSource = (*ProfileSource)(nil)
