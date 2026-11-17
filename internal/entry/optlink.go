package entry

import (
	"os"
	"path/filepath"
	"strings"
)

// preferOptLink names the binary at path by the opt link of its Homebrew keg, when path is inside a Cellar
// and that link names the same binary there. The Cellar path names one version, which an upgrade replaces
// and `brew cleanup` removes, and the opt link names whichever version is installed. Any other path, and a
// Cellar path with no opt link, stays as it is.
func preferOptLink(path string) string {
	separator := string(filepath.Separator)
	prefix, kept, found := strings.Cut(path, separator+"Cellar"+separator)
	if !found {
		return path
	}
	// What is left names the formula, then its version, then the file inside the keg.
	parts := strings.SplitN(kept, separator, 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return path
	}
	link := filepath.Join(prefix, "opt", parts[0], parts[2])
	if _, err := os.Stat(link); err != nil {
		return path
	}
	return link
}

// executablePath is this command's path, named by the opt link of its Homebrew keg when it has one. On
// Linux the operating system resolves the path of a running binary to its keg, so a service written from
// it would name a version that the next upgrade replaces.
func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return preferOptLink(path), nil
}
