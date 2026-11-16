// Package profiles is the ProfileSource on TOML files. A repository's layer lives in
// the repository, so it is reviewed with the code it configures.
package profiles

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"maps"
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
// worse than a lane that does not start. So is a key spelled two ways, because the
// decoder reads keys in any case and would take whichever it met first. The gates of
// every layer join, so no layer drops one of another's.
func Resolve(layers ...string) (lanes.Profile, error) {
	tables := make([]map[string]any, 0, len(layers)+1)
	for _, text := range append([]string{builtin}, layers...) {
		var table map[string]any
		if _, err := toml.Decode(text, &table); err != nil {
			return lanes.Profile{}, fmt.Errorf("%w: %v", lanes.ErrBadProfile, err)
		}
		tables = append(tables, table)
	}
	merged, err := lanes.MergeLayers(tables...)
	if err != nil {
		return lanes.Profile{}, err
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(merged); err != nil {
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
	// No layer, the user's included, names a mode that bypasses every check (NFR-SEC-3).
	if err := lanes.CheckPermissionMode(profile.Agent.PermissionMode); err != nil {
		return lanes.Profile{}, err
	}
	if err := profile.Admission.Check(); err != nil {
		return lanes.Profile{}, err
	}
	if _, _, err := profile.Resources.Reporting(); err != nil {
		return lanes.Profile{}, err
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
// trust. Every layer's gates join the layers below it, so a repository's layer keeps every gate
// of the user's and may add its own. A lane's overrides can come from any caller of the API, so
// they are held to the user's and the repository's layers too: they change no hook, credential,
// or place of the worktree or its branch, they keep every gate of both and may add one, and they
// raise no cap of the breaker.
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
	// How often the worker measures its machine, and where a measure warns, are the machine's.
	if !lanes.SameMeasuring(below.Resources, profile.Resources) {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot change how this machine is measured", lanes.ErrBadProfile)
	}
	// Content in telemetry is a person's choice, and the worker's store and export are the machine's.
	if profile.Telemetry.Content && !below.Telemetry.Content {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot turn content capture on", lanes.ErrBadProfile)
	}
	if profile.Telemetry.WorkerSettings() != below.Telemetry.WorkerSettings() {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot change the worker's telemetry store or export", lanes.ErrBadProfile)
	}
	// What each class of agent costs is the user's choice alone (FR-AGENT-2).
	if !maps.Equal(profile.Agent.Classes, below.Agent.Classes) {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot change [agent.classes]", lanes.ErrBadProfile)
	}
	// A push sends work off this machine, which is the user's choice alone. A worker reads it
	// with no lane's layers, because a removal can come long after a lane's lease went.
	if profile.Worktree.PushOnStop != below.Worktree.PushOnStop {
		return lanes.Profile{}, fmt.Errorf("%w: a layer past the user's cannot change push_on_stop", lanes.ErrBadProfile)
	}
	layered := profile
	if overrides != "" {
		if layered, err = Resolve(user, repository); err != nil {
			return lanes.Profile{}, err
		}
	}
	// A gate asks a person before an action runs. The merge joins every layer's gates, so none is dropped,
	// and this keeps it so if the merge ever changes.
	if gate, dropped := lanes.DroppedGate(below.Agent.Gates, layered.Agent.Gates); dropped {
		return lanes.Profile{}, fmt.Errorf("%w: a repository's layer cannot drop the user's gate %q of agent.gates", lanes.ErrBadProfile, gate)
	}
	if overrides != "" {
		if err := lanes.CheckLaneLayer(layered, profile); err != nil {
			return lanes.Profile{}, err
		}
	}
	return profile, nil
}

// Filesystem reads a user's layer and a repository's layer from its working tree.
type Filesystem struct {
	// User is the user's layer. An empty path or an absent file has none.
	User string
	// DefaultRoot is the worktree root of a profile whose layers name none, a template that may hold
	// {project} and {repo}. The daemon sets it to its worktree directory's {project}/{repo}, so a lane lands
	// outside every clone (FR-PROF-3). Empty leaves such a profile with no root.
	DefaultRoot string
}

// ApplyDefaultRoot gives a profile that names no worktree root defaultRoot, and records which layer set the
// root it has: the repository's when its layer names one, else the user's, else the default. A lane's own
// layer cannot set a root. user and repository are the layers the profile was resolved from.
func ApplyDefaultRoot(profile lanes.Profile, defaultRoot, user, repository string) (lanes.Profile, error) {
	if profile.Worktree.Root == "" {
		profile.Worktree.Root, profile.Worktree.RootSource = defaultRoot, lanes.RootFromDefault
		return profile, nil
	}
	layer, err := Resolve(repository)
	if err != nil {
		return lanes.Profile{}, err
	}
	profile.Worktree.RootSource = lanes.RootFromUser
	if layer.Worktree.Root != "" {
		profile.Worktree.RootSource = lanes.RootFromRepository
	}
	return profile, nil
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
	profile, err := ResolveLayers(user, layer, overrides)
	if err != nil {
		return lanes.Profile{}, err
	}
	return ApplyDefaultRoot(profile, f.DefaultRoot, user, layer)
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
