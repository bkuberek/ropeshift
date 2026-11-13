package authority

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
)

// EnvDelegateToken holds a delegate credential in the shell of the agent a member gave it to. No lane may
// inherit it, as no lane inherits a member's session.
const EnvDelegateToken = "ROPESHIFT_DELEGATE_TOKEN"

// The bounds of a delegate credential's life, in milliseconds, and of its label, in characters.
const (
	MinDelegateTTLMs int64 = 60 * 1000
	MaxDelegateTTLMs int64 = 30 * 24 * 60 * 60 * 1000
	MaxDelegateLabel       = 64
)

// DelegateIDPrefix starts the ID of every delegate credential.
const DelegateIDPrefix = "dlg_"

// DelegateExternal is the one kind of delegate credential: one a member hands to an agent outside
// ropeshift.
const DelegateExternal = "external"

// DelegateState is where a delegate credential stands.
type DelegateState string

// The states of a delegate credential. Only an active one acts.
const (
	DelegateActive  DelegateState = "active"
	DelegateExpired DelegateState = "expired"
	DelegateRevoked DelegateState = "revoked"
)

// The refusals of a delegate.
var (
	// ErrBadDelegate means a request carries a delegate credential that is not known, was revoked, or
	// expired.
	ErrBadDelegate = errors.New("this delegate credential does not act")
	// ErrOutsideProject means a delegate called a route outside the one project its credential names.
	ErrOutsideProject = errors.New("a delegate reaches only its own project")
	// ErrMemberOnly means a delegate called an operation only a member runs, which no card stands in for.
	ErrMemberOnly = errors.New("only a member does this")
	// ErrDelegateActive means a member asked to remove a credential that still acts: revoke it first.
	ErrDelegateActive = errors.New("this delegate credential is active")
)

// DelegateCredential is one delegate credential (ADR-0024). The ledger keeps a digest of its token, as it
// keeps a session's, so the token exists only where the member put it. LastUsedAt and RevokedAt are zero
// until each happens.
type DelegateCredential struct {
	ID         string             `json:"id"`
	Digest     string             `json:"-"`
	Member     string             `json:"member"`
	Project    ledger.WorkspaceID `json:"project_id"`
	Label      string             `json:"label"`
	Kind       string             `json:"kind"`
	CreatedAt  int64              `json:"created_at"`
	ExpiresAt  int64              `json:"expires_at"`
	LastUsedAt int64              `json:"last_used_at,omitempty"`
	RevokedAt  int64              `json:"revoked_at,omitempty"`
}

// State reads a credential at a time. A revoked credential stays revoked after it would have expired.
func (d DelegateCredential) State(now int64) DelegateState {
	switch {
	case d.RevokedAt != 0:
		return DelegateRevoked
	case now >= d.ExpiresAt:
		return DelegateExpired
	}
	return DelegateActive
}

// Actor is who the credential acts as.
func (d DelegateCredential) Actor() ledger.Actor { return DelegateActor(d.Member, d.ID) }

// DelegateActor names a delegate by the member it acts for and its credential, so every event it causes
// names that member (FR-LEDGER-4).
func DelegateActor(member, credential string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorDelegate, ID: member + "/" + credential}
}

// ActingFor reads the member and the credential of a delegate actor.
func ActingFor(actor ledger.Actor) (member, credential string, ok bool) {
	if actor.Kind != ledger.ActorDelegate {
		return "", "", false
	}
	member, credential, ok = strings.Cut(actor.ID, "/")
	return member, credential, ok && member != "" && credential != ""
}

// DelegateRequest is what a member asks of a new credential. The project is checked against the
// registry, which this package does not hold.
type DelegateRequest struct {
	Member      string
	Project     ledger.WorkspaceID
	Label       string
	Kind        string
	ExpiresInMs int64
}

// Check refuses a request that cannot make a credential.
func (r DelegateRequest) Check() error {
	switch length := utf8.RuneCountInString(r.Label); {
	case strings.Contains(r.Member, "/"):
		return fmt.Errorf("%w: member %q holds a slash, which a delegate's actor ID cannot carry", ErrInvalid, r.Member)
	case strings.TrimSpace(r.Label) == "" || length > MaxDelegateLabel:
		return fmt.Errorf("%w: a delegate's label is 1 to %d characters", ErrInvalid, MaxDelegateLabel)
	case r.ExpiresInMs < MinDelegateTTLMs || r.ExpiresInMs > MaxDelegateTTLMs:
		return fmt.Errorf("%w: a delegate expires in one minute to thirty days, as expires_in_ms", ErrInvalid)
	case r.Kind != "" && r.Kind != DelegateExternal:
		return fmt.Errorf("%w: a delegate's kind is %s", ErrInvalid, DelegateExternal)
	}
	return nil
}

// PolicyDelegate governs which consent operations a delegate runs with no card (ADR-0024).
const PolicyDelegate PolicyKind = "delegate"

// DelegatePolicy is the body of a delegate policy version: the consent operations a delegate runs at once.
type DelegatePolicy struct {
	Version int64    `toml:"version" json:"version"`
	Exempt  []string `toml:"exempt" json:"exempt"`
}

// DelegatePolicyFile is the table a member publishes.
type DelegatePolicyFile struct {
	DelegatePolicy DelegatePolicy `toml:"delegate_policy"`
}

// DelegatePolicyDraft is a delegate policy as written, and what it says.
type DelegatePolicyDraft struct {
	Body   string
	Policy DelegatePolicy
}

// BuiltInDelegatePolicyBody is what a project runs on until a member publishes one, which exempts nothing:
// every consent operation a delegate calls becomes a card.
const BuiltInDelegatePolicyBody = `[delegate_policy]
version = 1
exempt = []
`

// BuiltInDelegatePolicy is what the built-in body says.
func BuiltInDelegatePolicy() DelegatePolicy { return DelegatePolicy{Version: 1, Exempt: []string{}} }

// Check refuses a version that names anything but an exemptable consent operation, naming each and why.
func (p DelegatePolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: delegate policy version %d", ErrInvalid, p.Version)
	}
	var refused []string
	for _, id := range p.Exempt {
		op, ok := OperationOf(id)
		switch {
		case !ok:
			refused = append(refused, id+" is no operation")
		case op.Class == ClassOperational:
			refused = append(refused, id+" is operational, so a delegate runs it with no exemption")
		case op.Class == ClassMemberOnly:
			refused = append(refused, id+" is a member's alone")
		case !op.Exemptable:
			refused = append(refused, id+" is never exempt: "+op.NeverReason)
		}
	}
	if len(refused) != 0 {
		return fmt.Errorf("%w: the delegate policy cannot exempt %s", ErrInvalid, strings.Join(refused, "; "))
	}
	return nil
}

// Exempts says a delegate's call runs with no card: every consent operation it resolves to is listed, and
// none is one a one-way category keeps for a card.
func (p DelegatePolicy) Exempts(ops []Operation, inOneWayCategory bool) bool {
	for _, op := range ops {
		if op.Class == ClassOperational {
			continue
		}
		if op.Class != ClassConsent || !op.Exemptable || !slices.Contains(p.Exempt, op.ID) || (op.OneWay && inOneWayCategory) {
			return false
		}
	}
	return true
}

// NewDelegatePolicyVersion stores a draft under a generated ID.
func NewDelegatePolicyVersion(id, authoredBy string, draft DelegatePolicyDraft, createdAt int64) (PolicyVersion, error) {
	if draft.Policy.Exempt == nil {
		draft.Policy.Exempt = []string{}
	}
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyDelegate, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadDelegatePolicy reads a version's settings, once its body is what its digest covers.
func ReadDelegatePolicy(version PolicyVersion) (DelegatePolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return DelegatePolicy{}, err
	}
	var policy DelegatePolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return DelegatePolicy{}, err
	}
	return policy, nil
}
