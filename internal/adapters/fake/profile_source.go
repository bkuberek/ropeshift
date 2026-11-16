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
	mu          sync.Mutex
	user        string
	layers      map[string]string
	defaultRoot string
}

// DefaultWorktreeRoot is the root a fake profile source gives a profile that names none: a folder under
// no clone a test places.
const DefaultWorktreeRoot = "/ropeshift/worktrees/{project}/{repo}"

// NewProfileSource returns a source with no layers, whose profiles with no root get DefaultWorktreeRoot.
func NewProfileSource() *ProfileSource {
	return &ProfileSource{layers: map[string]string{}, defaultRoot: DefaultWorktreeRoot}
}

// SetDefaultRoot sets the root a profile that names none gets, as the daemon's worktree directory does.
func (s *ProfileSource) SetDefaultRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultRoot = root
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
	text, user, defaultRoot := s.layers[repository], s.user, s.defaultRoot
	s.mu.Unlock()
	profile, err := profiles.ResolveLayers(user, text, overrides)
	if err != nil {
		return lanes.Profile{}, err
	}
	return profiles.ApplyDefaultRoot(profile, defaultRoot, user, text)
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
