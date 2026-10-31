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

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

//go:embed builtin.toml
var builtin string

// RepositoryFile is a repository's layer, relative to its root.
const RepositoryFile = ".ropeshift/profile.toml"

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

// Filesystem reads a repository's layer from its working tree.
type Filesystem struct{}

// Load resolves one repository's profile. A repository with no layer gets the
// defaults, and a layer that cannot be read stops the lane.
func (Filesystem) Load(_ context.Context, repository string) (lanes.Profile, error) {
	raw, err := os.ReadFile(filepath.Join(repository, RepositoryFile))
	if errors.Is(err, os.ErrNotExist) {
		return Resolve()
	}
	if err != nil {
		return lanes.Profile{}, err
	}
	return Resolve(string(raw))
}

var _ ports.ProfileSource = Filesystem{}
