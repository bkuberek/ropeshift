package authority

import (
	"errors"
	"fmt"
)

// EnvMemberToken holds a member's session in one shell, after `eval "$(ropeshift login)"`.
// No file holds it, and no lane may inherit it (D26).
const EnvMemberToken = "ROPESHIFT_MEMBER_TOKEN"

// The longest a session lasts, in milliseconds. A login lasts for a stretch of work, and
// the session behind one command lasts as long as that command.
const (
	LoginTTLMs   int64 = 60 * 60 * 1000
	CommandTTLMs int64 = 60 * 1000
)

// The shortest and the longest passphrase, in bytes. The longest is a bound a terminal can honor: the passphrase comes
// only from a terminal's line, which the terminal reads to a bounded length and edits with backspace, so a longer
// one set from a browser could be one its owner cannot type again.
const (
	MinPassphrase = 8
	MaxPassphrase = 1024
)

// CheckPassphrase says whether a new passphrase is within the bounds, as ErrInvalid with the bound it broke.
func CheckPassphrase(passphrase string) error {
	if len(passphrase) < MinPassphrase {
		return fmt.Errorf("%w: a passphrase has at least %d bytes", ErrInvalid, MinPassphrase)
	}
	if len(passphrase) > MaxPassphrase {
		return fmt.Errorf("%w: a passphrase has at most %d bytes", ErrInvalid, MaxPassphrase)
	}
	return nil
}

// The ways sign-in refuses.
var (
	// ErrNoSession means a call needs an authenticated member and has none that is live.
	ErrNoSession = errors.New("this needs a member session")
	// ErrBadProof means what a person presented proves nothing.
	ErrBadProof = errors.New("the passphrase does not match")
	// ErrPassphraseChanged means a change of the passphrase lost to another: the current passphrase it
	// checked was right, and is no longer the one in force.
	ErrPassphraseChanged = errors.New("the passphrase changed while you were changing it; sign in again")
	// ErrNotEnrolled means nobody can sign in yet.
	ErrNotEnrolled = errors.New("no passphrase is set: run ropeshift passphrase set on a terminal")
	// ErrInvalid means a proof cannot be accepted as it stands.
	ErrInvalid = errors.New("invalid sign-in")
)

// MemberActive is the status of a member who can sign in.
const MemberActive = "active"

// Member is an authenticated human in a workspace. It has no role: Allowed returns true
// for any member, and an optional role would arrive there.
type Member struct {
	ID              string `json:"id"`
	IdentitySubject string `json:"identity_subject"`
	DisplayName     string `json:"display_name"`
	Status          string `json:"status"`
}

// MemberRecord is a member and the hash a local passphrase is checked against. A member
// another provider authenticates holds no hash.
type MemberRecord struct {
	Member
	ProofDigest string
}

// Session is one sign-in. The ledger keeps a digest of its token, so the token itself
// exists only in the hands of the person who signed in.
type Session struct {
	Digest    string
	Member    string
	CreatedAt int64
	ExpiresAt int64
}

// Live reports whether a session still counts at a time.
func (s Session) Live(now int64) bool { return s.ExpiresAt > now }

// BoundTTL gives the life of a new session: what was asked for, up to a login's.
func BoundTTL(askedMs int64) int64 {
	if askedMs <= 0 || askedMs > LoginTTLMs {
		return LoginTTLMs
	}
	return askedMs
}
