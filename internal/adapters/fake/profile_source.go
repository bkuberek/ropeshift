package fake

import (
	"context"
	"fmt"
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
	at          map[layerAt]string
	held        map[string]map[string]bool
	loaded      []Load
	defaultRoot string
}

// layerAt names the layer a repository holds at one commit.
type layerAt struct{ repository, commit string }

// Load is one read of a repository's layer: the repository and the commit the caller asked for.
type Load struct {
	Repository, Base string
}

// DefaultWorktreeRoot is the root a fake profile source gives a profile that names none: a folder beside the
// repository's clone, never inside it, so a test that makes real directories makes them beside its own
// temporary clone and not under a path it does not own. SetDefaultRoot gives the daemon's own shape.
const DefaultWorktreeRoot = "../{repo}-lanes"

// NewProfileSource returns a source with no layers, whose profiles with no root get DefaultWorktreeRoot.
func NewProfileSource() *ProfileSource {
	return &ProfileSource{layers: map[string]string{}, at: map[layerAt]string{}, held: map[string]map[string]bool{}, defaultRoot: DefaultWorktreeRoot}
}

// SetDefaultRoot sets the root a profile that names none gets, as the daemon's worktree directory does.
func (s *ProfileSource) SetDefaultRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultRoot = root
}

// Put stands for a repository's layer at every commit that has none of its own.
func (s *ProfileSource) Put(repository, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.layers[repository] = text
}

// PutAt stands for the layer a repository holds at one commit, which wins over Put's at that commit. The
// repository then holds the commit.
func (s *ProfileSource) PutAt(repository, commit, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at[layerAt{repository, commit}] = text
	s.hold(repository, commit)
}

// Hold says a repository holds commits that carry no layer of their own. Once a repository holds any commit,
// a read at another commit is ErrNotFound, as it is for a repository that does not have it. A repository with
// none held answers every commit.
func (s *ProfileSource) Hold(repository string, commits ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, commit := range commits {
		s.hold(repository, commit)
	}
}

func (s *ProfileSource) hold(repository, commit string) {
	if s.held[repository] == nil {
		s.held[repository] = map[string]bool{}
	}
	s.held[repository][commit] = true
}

// PutUser stands for the user's layer.
func (s *ProfileSource) PutUser(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = text
}

// Loaded lists every read that named a repository, in order, so a test can say which commit a lane's layer
// was read at.
func (s *ProfileSource) Loaded() []Load {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Load(nil), s.loaded...)
}

// Load resolves one lane's profile.
func (s *ProfileSource) Load(_ context.Context, repository, base, overrides string) (lanes.Profile, error) {
	s.mu.Lock()
	if held := s.held[repository]; base != "" && len(held) != 0 && !held[base] {
		s.mu.Unlock()
		return lanes.Profile{}, fmt.Errorf("%w: %s", ports.ErrNotFound, base)
	}
	text, found := s.at[layerAt{repository, base}]
	if !found {
		text = s.layers[repository]
	}
	user, defaultRoot := s.user, s.defaultRoot
	if repository != "" {
		s.loaded = append(s.loaded, Load{Repository: repository, Base: base})
	}
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
