package projectfiles

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// remoteSchemes are the URL schemes a remote may name.
var remoteSchemes = []string{"https", "ssh", "git", "file"}

// credentialRefusal is what a remote that holds a credential is refused with. It repeats nothing of the remote.
const credentialRefusal = "a remote's URL holds no user or password in an http, https, or file URL, and no password in any other: the URL is stored in the installation, in reports, and in the manifest, so give it without the credential and let git's credential helper or an SSH key supply it"

// checkRemote refuses a remote that is not one line git clones from: a URL with a scheme in remoteSchemes, or
// scp-like [user@]host:path. The characters are a short allowlist, because the remote is written into a
// TOML string in the manifest and, unquoted, into a Makefile variable that the setup recipe expands into a
// shell word: nothing outside the list can end the string or the line, start a comment, a substitution, or a
// second command. A host that starts with a dash is refused so no remote reads as an option of git or ssh, and a
// remote that holds `::` is refused because git reads `transport::address` as a remote helper, such as `ext::`,
// which runs a command. An https, http, or file URL with a user, and any URL with a password, are refused, and so is a
// user that holds a colon or an at sign written as %3A or %40. The scheme is read in lower case for those checks and
// accepted only in lower case, and no refusal repeats a user part.
func checkRemote(remote string) error {
	shown := redacted(remote)
	scheme, rest, url := strings.Cut(remote, "://")
	lower := strings.ToLower(scheme)
	if url {
		authority, _, _ := strings.Cut(rest, "/")
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			web := lower == "https" || lower == "http" || lower == "file"
			if web || strings.Contains(authority[:at], ":") || encodedSeparator(authority[:at]) {
				return errors.New(credentialRefusal)
			}
		}
	} else if !url {
		// An scp-like remote is user@host:path, and an @ after its first colon is a password written as user:password@host.
		if _, path, colon := strings.Cut(remote, ":"); colon && strings.Contains(path, "@") {
			return errors.New(credentialRefusal)
		}
	}
	if i := strings.IndexFunc(remote, func(r rune) bool { return !remoteRune(r) }); i >= 0 {
		bad := []rune(remote[i:])[0]
		return fmt.Errorf("a remote holds only letters, digits, and - . _ ~ / : @ %% +, and %q has %q", shown, bad)
	}
	if strings.Contains(remote, "::") {
		return fmt.Errorf("a remote names no remote helper, which git reads from transport::address, and %q names one", shown)
	}
	if url {
		// A scheme is accepted only in lower case: git hands another case to a remote helper of that name.
		if scheme != lower || !slices.Contains(remoteSchemes, scheme) {
			return fmt.Errorf("a remote's scheme is https, ssh, git, or file, and %q has %q", shown, scheme)
		}
		if scheme == "file" {
			if !strings.HasPrefix(rest, "/") || strings.Trim(rest, "/") == "" {
				return fmt.Errorf("a file remote names an absolute path after file://, and %q has none", shown)
			}
			return nil
		}
		authority, _, _ := strings.Cut(rest, "/")
		if host := hostOf(authority); host == "" || strings.HasPrefix(host, "-") {
			return fmt.Errorf("a remote's URL names a host, and %q has none", shown)
		}
		return nil
	}
	authority, path, found := strings.Cut(remote, ":")
	if host := hostOf(authority); !found || path == "" || host == "" || strings.HasPrefix(remote, "-") || strings.HasPrefix(host, "-") || strings.Contains(authority, "/") {
		return fmt.Errorf("a remote is a URL or scp-like user@host:path, and %q is neither; for a local repository use a file:// URL", shown)
	}
	return nil
}

// redacted is a remote as a message may repeat it: the user part before the last at sign of its authority, which for a
// remote with no scheme is everything before its first slash, is replaced with ***, so no refusal carries a credential.
func redacted(remote string) string {
	head, rest := "", remote
	if i := strings.Index(remote, "://"); i >= 0 {
		head, rest = remote[:i+3], remote[i+3:]
	}
	authority, tail := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		return head + "***" + authority[at:] + tail
	}
	return remote
}

// encodedSeparator says a user holds a colon or an at sign written as a percent escape, in either case.
func encodedSeparator(user string) bool {
	lower := strings.ToLower(user)
	return strings.Contains(lower, "%3a") || strings.Contains(lower, "%40")
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
