// Package pathmask writes text a person reads, or a report carries, without the paths of the machine it came from.
package pathmask

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CheckoutPlaceholder stands for the directory of a checkout.
const CheckoutPlaceholder = "<checkout>"

// Text returns text with every spelling of the checkout's directory replaced by CheckoutPlaceholder: the directory
// as it was given, as it reads cleaned, and as symbolic links resolve it, which is the one git names. It also
// replaces the temporary directory by `<temp>`, the person's home folder by `~`, and the configuration folder by
// `<config>`, each as given and as symbolic links resolve it. A relative XDG_CONFIG_HOME is no configuration folder
// and is left. The longest path is replaced first, so a path inside another is replaced whole. What it returns
// names nothing of the layout of the machine.
func Text(text, dir string) string {
	if text == "" {
		return text
	}
	var paths []placeholder
	paths = appendSpellings(paths, CheckoutPlaceholder, dir, filepath.Clean(dir))
	paths = appendSpellings(paths, "<temp>", filepath.Clean(os.TempDir()))
	if home, err := os.UserHomeDir(); err == nil {
		paths = appendSpellings(paths, "~", home)
	}
	if config := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(config) {
		paths = appendSpellings(paths, "<config>", config)
	}
	slices.SortStableFunc(paths, func(a, b placeholder) int { return len(b.path) - len(a.path) })
	for _, one := range paths {
		text = strings.ReplaceAll(text, one.path, one.stands)
	}
	return text
}

// placeholder is a path a text does not name, and what stands for it.
type placeholder struct{ path, stands string }

// appendSpellings adds each of the paths, and each as symbolic links resolve it, as spellings of what the placeholder
// stands for. A path that is empty or is the root is left out, because replacing it would leave nothing of the text.
func appendSpellings(paths []placeholder, stands string, spellings ...string) []placeholder {
	for _, spelled := range slices.Clone(spellings) {
		if resolved, err := filepath.EvalSymlinks(spelled); err == nil {
			spellings = append(spellings, resolved)
		}
	}
	for _, spelled := range spellings {
		if len(spelled) > 1 && spelled != string(filepath.Separator) && !slices.Contains(paths, placeholder{spelled, stands}) {
			paths = append(paths, placeholder{spelled, stands})
		}
	}
	return paths
}
