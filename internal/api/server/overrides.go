package server

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/lanes"
)

// checkOverrides refuses a lane's own profile layer that sets a key of lanes.LaneLayerKeys, or that spells one
// key two ways, before the ledger keeps it. Any caller can send one, an outside delegate's included, so the
// caller reads the reason here rather than as a failed start on the worker. The worker's profile holds the
// layer to the user's and the repository's layers, and is the line that counts: it also refuses a layer that
// raises a cap of the breaker, which only the worker's own layers can measure it against. It has no gate to
// check, because the gates of every layer join, so a layer can only add one.
func checkOverrides(overrides string) error {
	if overrides == "" {
		return nil
	}
	var table map[string]any
	meta, err := toml.Decode(overrides, &table)
	if err != nil {
		return fmt.Errorf("a lane's overrides are not TOML: %v", err)
	}
	if err := lanes.CheckSpellings(table); err != nil {
		return fmt.Errorf("a lane's overrides are refused: %v", err)
	}
	for _, setting := range lanes.LaneLayerKeys() {
		for _, key := range meta.Keys() {
			if sets(key, setting) {
				return fmt.Errorf("a lane's overrides cannot set %s", strings.Join(setting, "."))
			}
		}
	}
	return nil
}

// sets says a key a layer wrote is the setting at path, or a key inside it. The profile's decoder takes a
// key for a setting when strings.EqualFold says the two are equal, so `[Hooks]`, `[HOOKS]`, and a `hooks`
// spelled with the Kelvin sign all set hooks, and this reads every key the same way.
func sets(key toml.Key, path []string) bool {
	if len(key) < len(path) {
		return false
	}
	for i, part := range path {
		if !strings.EqualFold(key[i], part) {
			return false
		}
	}
	return true
}
