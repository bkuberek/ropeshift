// Package profiles is the ProfileSource on TOML files. A repository's layer lives in
// the repository, so it is reviewed with the code it configures.
package profiles

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

//go:embed builtin.toml
var builtin string

// RepositoryFile is a repository's layer, relative to its root, and UserFile the user's,
// relative to the state directory.
const (
	RepositoryFile = ".ropeshift/profile.toml"
	UserFile       = "profile.toml"
)

// Resolve folds layers of TOML over the built-in defaults. A key the settings do not
// name is refused, because a misspelled threshold that silently keeps its default is
// worse than a lane that does not start.
func Resolve(layers ...string) (lanes.Profile, error) {
	tables := make([]map[string]any, 0, len(layers)+1)
	for _, text := range append([]string{builtin}, layers...) {
		var table map[string]any
		if _, err := toml.Decode(text, &table); err != nil {
			return lanes.Profile{}, fmt.Errorf("%w: %v", lanes.ErrBadProfile, err)
		}
		tables = append(tables, table)
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(lanes.MergeLayers(tables...)); err != nil {
		return lanes.Profile{}, fmt.Errorf("%w: %v", lanes.ErrBadProfile, err)
	}
	var profile lanes.Profile
	meta, err := toml.Decode(encoded.String(), &profile)
	if err != nil {
		return lanes.Profile{}, fmt.Errorf("%w: %v", lanes.ErrBadProfile, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return lanes.Profile{}, fmt.Errorf("%w: unknown setting %s", lanes.ErrBadProfile, unknown[0])
	}
	return profile, nil
}

// ResolveRepository folds a user's layer and a repository's.
func ResolveRepository(user, repository string) (lanes.Profile, error) {
	return ResolveLayers(user, repository, "")
}

// ResolveLayers folds every layer over the built-in defaults, later over earlier. Only the
// user's layer can widen what a lane may do. A repository is code from somewhere else, and a
// lane's overrides can come from an orchestrator, so either can narrow it and neither can
// raise the permission mode, raise a lane cap, change any other part of what this machine
// admits, or change whether its work is pushed. Any file a repository names is the user's to
// trust.
func ResolveLayers(user, repository, overrides string) (lanes.Profile, error) {
	below, err := Resolve(user)
	if err != nil {
		return lanes.Profile{}, err
	}
	profile, err := Resolve(user, repository, overrides)
	if err != nil {
		return lanes.Profile{}, err
	}
	if lanes.RaisesPermission(below.Agent.PermissionMode, profile.Agent.PermissionMode) {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot raise the permission mode from %s to %s",
			lanes.ErrBadProfile, below.Agent.PermissionMode, profile.Agent.PermissionMode)
	}
	if err := lanes.CheckAdmission(below.Admission, profile.Admission); err != nil {
		return lanes.Profile{}, err
	}
	// Content in telemetry is a person's choice, and the worker's store and export are the machine's.
	if profile.Telemetry.Content && !below.Telemetry.Content {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot turn content capture on", lanes.ErrBadProfile)
	}
	if profile.Telemetry.WorkerSettings() != below.Telemetry.WorkerSettings() {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot change the worker's telemetry store or export", lanes.ErrBadProfile)
	}
	// A push sends work off this machine, which is the user's choice alone. A worker reads it
	// with no lane's layers, because a removal can come long after a lane's lease went.
	if profile.Worktree.PushOnStop != below.Worktree.PushOnStop {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot change push_on_stop", lanes.ErrBadProfile)
	}
	return profile, nil
}

// Filesystem reads a user's layer and a repository's layer from its working tree.
type Filesystem struct {
	// User is the user's layer. An empty path or an absent file has none.
	User string
}

func readLayer(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

// Load resolves one lane's profile. A repository with no layer gets the defaults, an empty
// repository names none, so a setting only the user's layer makes reads without one, and a
// layer that cannot be read stops the lane.
func (f Filesystem) Load(_ context.Context, repository, overrides string) (lanes.Profile, error) {
	user, err := readLayer(f.User)
	if err != nil {
		return lanes.Profile{}, err
	}
	layer, err := readLayer(repositoryLayer(repository))
	if err != nil {
		return lanes.Profile{}, err
	}
	return ResolveLayers(user, layer, overrides)
}

// repositoryLayer is where a repository keeps its layer. An empty repository is none, and not
// the directory the process happens to run in.
func repositoryLayer(repository string) string {
	if repository == "" {
		return ""
	}
	return filepath.Join(repository, RepositoryFile)
}

var _ ports.ProfileSource = Filesystem{}
