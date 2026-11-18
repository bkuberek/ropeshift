package practices

import (
	"fmt"
	"strings"
)

// DigestIDLength is how many hexadecimal characters of a secret's digest a finding's ID holds.
const DigestIDLength = 12

// Finding is one thing a practice found wanting.
type Finding struct {
	// ID is stable across runs, so an acknowledgement keeps holding: see FindingID.
	ID       string   `json:"id"`
	Practice ID       `json:"practice"`
	Subject  string   `json:"subject,omitempty"`
	Severity Severity `json:"severity"`
	Detail   string   `json:"detail"`
	// Rule, Path, Line, Commit, and Digest describe a `secrets` finding, and never hold the matched text.
	Rule   string `json:"rule,omitempty"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Commit string `json:"commit,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// FindingID builds a finding's ID: the practice's ID, and its subject parts joined by `:` where one practice
// can find several things.
//
//	env-ignored:ignore:<folder>, with . for the root
//	env-ignored:tracked:<path>
//	secrets:<rule>:<path>:<first 12 hex characters of the digest>
//	lockfile:<manager>, or lockfile:<manager>:<folder> for a manifest below the root
//	tests:<manager>
//
// Every other practice's ID is its practice's ID.
func FindingID(practice ID, subject ...string) string {
	return strings.Join(append([]string{string(practice)}, subject...), ":")
}

// ParseFindingID returns the practice a finding ID names. It refuses an unknown practice and a subject that the
// practice's grammar does not allow.
func ParseFindingID(id string) (ID, error) {
	head, rest, hasSubject := strings.Cut(id, ":")
	practice := ID(head)
	if _, ok := Lookup(practice); !ok {
		return "", fmt.Errorf("finding ID %q names no practice", id)
	}
	bad := func(why string) (ID, error) { return "", fmt.Errorf("finding ID %q is malformed: %s", id, why) }
	switch practice {
	case EnvIgnored:
		kind, subject, ok := strings.Cut(rest, ":")
		if !hasSubject || !ok || subject == "" || (kind != "ignore" && kind != "tracked") {
			return bad("want env-ignored:ignore:<folder> or env-ignored:tracked:<path>")
		}
	case Secrets:
		rule, tail, ok := strings.Cut(rest, ":")
		cut := strings.LastIndex(tail, ":")
		if !hasSubject || !ok || rule == "" || cut <= 0 || !isDigestID(tail[cut+1:]) {
			return bad("want secrets:<rule>:<path>:<digest>")
		}
	case Lockfile:
		manager, _, _ := strings.Cut(rest, ":")
		if _, ok := managerNamed(manager); !hasSubject || !ok || strings.HasSuffix(rest, ":") {
			return bad("want lockfile:<manager> or lockfile:<manager>:<folder>")
		}
	case Tests:
		if _, ok := managerNamed(rest); !hasSubject || !ok {
			return bad("want tests:<manager>")
		}
	default:
		if hasSubject {
			return bad("the practice has no subject")
		}
	}
	return practice, nil
}

func isDigestID(s string) bool {
	if len(s) != DigestIDLength {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
