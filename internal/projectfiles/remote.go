package projectfiles

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// remoteSchemes are the URL schemes a remote may name.
var remoteSchemes = []string{"https", "ssh", "git", "file"}

// checkRemote refuses a remote that is not one line git clones from: a URL with a scheme in remoteSchemes, or
// scp-like [user@]host:path. The characters are a short allowlist, because the remote is written into a
// TOML string in the manifest and, unquoted, into a Makefile variable that the setup recipe expands into a
// shell word: nothing outside the list can end the string or the line, start a comment, a substitution, or a
// second command. A host that starts with a dash is refused so no remote reads as an option of git or ssh, and a
// remote that holds `::` is refused because git reads `transport::address` as a remote helper, such as `ext::`,
// which runs a command. A URL with a password, user:secret@host, is refused without echoing it.
func checkRemote(remote string) error {
	if scheme, rest, found := strings.Cut(remote, "://"); found && scheme != "file" {
		authority, _, _ := strings.Cut(rest, "/")
		if at := strings.LastIndex(authority, "@"); at >= 0 && strings.Contains(authority[:at], ":") {
			return errors.New("a remote's URL holds no password: it is stored in the installation, in reports, and in the manifest, so give the URL without one and let git's credential helper or an SSH key supply the credential")
		}
	}
	if i := strings.IndexFunc(remote, func(r rune) bool { return !remoteRune(r) }); i >= 0 {
		bad := []rune(remote[i:])[0]
		return fmt.Errorf("a remote holds only letters, digits, and - . _ ~ / : @ %% +, and %q has %q", remote, bad)
	}
	if strings.Contains(remote, "::") {
		return fmt.Errorf("a remote names no remote helper, which git reads from transport::address, and %q names one", remote)
	}
	if scheme, rest, found := strings.Cut(remote, "://"); found {
		if !slices.Contains(remoteSchemes, scheme) {
			return fmt.Errorf("a remote's scheme is https, ssh, git, or file, and %q has %q", remote, scheme)
		}
		if scheme == "file" {
			if !strings.HasPrefix(rest, "/") || strings.Trim(rest, "/") == "" {
				return fmt.Errorf("a file remote names an absolute path after file://, and %q has none", remote)
			}
			return nil
		}
		authority, _, _ := strings.Cut(rest, "/")
		if host := hostOf(authority); host == "" || strings.HasPrefix(host, "-") {
			return fmt.Errorf("a remote's URL names a host, and %q has none", remote)
		}
		return nil
	}
	authority, path, found := strings.Cut(remote, ":")
	if host := hostOf(authority); !found || path == "" || host == "" || strings.HasPrefix(remote, "-") || strings.HasPrefix(host, "-") || strings.Contains(authority, "/") {
		return fmt.Errorf("a remote is a URL or scp-like user@host:path, and %q is neither; for a local repository use a file:// URL", remote)
	}
	return nil
}

// hostOf drops a user and a port from an authority.
func hostOf(authority string) string {
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	host, _, _ := strings.Cut(authority, ":")
	return host
}

// remoteRune says r may be in a remote.
func remoteRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~/:@%+", r)
}
