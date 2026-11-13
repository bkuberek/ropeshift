package fake

import (
	"context"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
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

// LaneCaps reads the caps the user's layer sets.
func (s *ProfileSource) LaneCaps(context.Context) (lanes.LaneCapSettings, error) {
	s.mu.Lock()
	user := s.user
	s.mu.Unlock()
	profile, err := profiles.Resolve(user)
	if err != nil {
		return lanes.LaneCapSettings{}, err
	}
	return lanes.LaneCapSettings{MaxLanes: profile.Admission.MaxLanes, MaxLanesPerSignIn: profile.Admission.MaxLanesPerSignIn}, nil
}

// SetLaneCaps edits the user's layer with the code the filesystem source edits its file with.
func (s *ProfileSource) SetLaneCaps(_ context.Context, caps lanes.LaneCapSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	edited, err := profiles.EditLaneCaps(s.user, caps)
	if err != nil {
		return err
	}
	s.user = edited
	return nil
}

// User returns the user's layer as it stands.
func (s *ProfileSource) User() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user
}

var (
	_ ports.ProfileSource = (*ProfileSource)(nil)
	_ ports.UserProfile   = (*ProfileSource)(nil)
)
