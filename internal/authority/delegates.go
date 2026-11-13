package authority

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
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

// MachineLocalCommands are the commands that act on the machine they run on, not through the API alone: a
// service, the first run, the doctor, a passphrase typed on a terminal, a pane a person attaches to, a copy
// written to a file the caller names, and clones registered from paths on this machine. No class gates
// what they do to the machine, so a delegate runs none of them (ADR-0024).
var MachineLocalCommands = []string{
	"service install", "service uninstall", "init", "worker doctor", "login", "passphrase set", "lane attach", "plan start", "adopt",
	"backup", "repository add",
}

// RedirectingFlags are the flags no delegate's command takes, by name: each points the command at another
// listener than the one this machine's installation names, which would receive the delegate's credential.
var RedirectingFlags = []string{"socket"}

// NamesRedirectingFlag says arguments hold one of RedirectingFlags, in any form a flag takes: `-name`,
// `--name`, `-name=value`, or `--name=value`, wherever it stands.
func NamesRedirectingFlag(args []string) bool {
	for _, arg := range args {
		for _, name := range RedirectingFlags {
			for _, form := range []string{"-" + name, "--" + name} {
				if arg == form || strings.HasPrefix(arg, form+"=") {
					return true
				}
			}
		}
	}
	return false
}

// DelegateIDPrefix starts the ID of every delegate credential.
const DelegateIDPrefix = "dlg_"

// The kinds of delegate credential. An external one is a member's to hand to an agent outside ropeshift.
// An assistant one is the control plane's, made for one generation of the session of one of a member's
// conversations, which acts only while that generation runs (ADR-0024).
const (
	DelegateExternal  = "external"
	DelegateAssistant = "assistant"
)

// DelegateState is where a delegate credential stands.
type DelegateState string

// The states of a delegate credential. Only an active one acts.
const (
	DelegateActive  DelegateState = "active"
	DelegateExpired DelegateState = "expired"
	DelegateRevoked DelegateState = "revoked"
)

// Why a revoked credential ended. An assistant credential ends routinely when its generation ends or its
// conversation's session stops, by a member's stop, an idle stop, a delete, or the session's own end, and a
// card it prepared still waits for a member. A member ends one explicitly by revoking it or by signing out
// of every session, and nothing it prepared stands after that.
const (
	DelegateEndGeneration = "generation_ended"
	DelegateEndStopped    = "session_stopped"
	DelegateEndIdle       = "idle_stopped"
	DelegateEndRevoked    = "revoked"
	DelegateEndSignedOut  = "signed_out"
)

// RoutineEnd says a reason is a routine end, which says nothing against what the credential prepared.
func RoutineEnd(reason string) bool {
	return reason == DelegateEndGeneration || reason == DelegateEndStopped || reason == DelegateEndIdle
}

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
// until each happens. An assistant credential names its conversation and generation and has no expiry,
// which a zero ExpiresAt says: it ends when its generation does.
type DelegateCredential struct {
	ID           string             `json:"id"`
	Digest       string             `json:"-"`
	Member       string             `json:"member"`
	Project      ledger.WorkspaceID `json:"project_id"`
	Label        string             `json:"label"`
	Kind         string             `json:"kind"`
	CreatedAt    int64              `json:"created_at"`
	ExpiresAt    int64              `json:"expires_at"`
	LastUsedAt   int64              `json:"last_used_at,omitempty"`
	RevokedAt    int64              `json:"revoked_at,omitempty"`
	Conversation string             `json:"conversation_id,omitempty"`
	Generation   int64              `json:"generation,omitempty"`
	// EndedReason says why a revoked credential ended, and is empty while it acts.
	EndedReason string `json:"ended_reason,omitempty"`
}

// State reads a credential at a time. A revoked credential stays revoked after it would have expired.
func (d DelegateCredential) State(now int64) DelegateState {
	switch {
	case d.RevokedAt != 0:
		return DelegateRevoked
	case d.ExpiresAt != 0 && now >= d.ExpiresAt:
		return DelegateExpired
	}
	return DelegateActive
}

// DelegateStates are the states of a delegate credential.
func DelegateStates() []DelegateState {
	return []DelegateState{DelegateActive, DelegateExpired, DelegateRevoked}
}

// Assistant says the credential is one the control plane made for an assistant's session.
func (d DelegateCredential) Assistant() bool { return d.Kind == DelegateAssistant }

// EndedRoutinely says the credential was revoked by a routine end, not by a member.
func (d DelegateCredential) EndedRoutinely() bool {
	return d.RevokedAt != 0 && RoutineEnd(d.EndedReason)
}

// AssistantLabel is the label of every assistant credential.
const AssistantLabel = "assistant"

// hidesCharacters says a label holds a character that shows as nothing or moves a terminal or a line of
// text: a control, a format character such as a zero-width space, a byte order mark, or a bidirectional
// control, a line or paragraph separator, a private-use or surrogate code point.
func hidesCharacters(label string) bool {
	return strings.IndexFunc(label, func(r rune) bool {
		return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs, unicode.Zl, unicode.Zp)
	}) >= 0
}

// Reserved says a label is the assistant's own, in any case and with spaces around it, so no outside
// credential can be made to pass for the assistant by its name.
func Reserved(label string) bool { return strings.EqualFold(strings.TrimSpace(label), AssistantLabel) }

// Actor is who the credential acts as.
func (d DelegateCredential) Actor() ledger.Actor { return DelegateActor(d.Member, d.ID) }

// DelegateActor names a delegate by the member it acts for and its credential, so every event it causes
// names that member (FR-LEDGER-4).
func DelegateActor(member, credential string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorDelegate, ID: member + "/" + credential}
}

// MemberOf reads the member an actor reads as: a member, or the member a delegate acts for.
func MemberOf(actor ledger.Actor) (string, bool) {
	if actor.Kind == ledger.ActorMember {
		return actor.ID, actor.ID != ""
	}
	member, _, ok := ActingFor(actor)
	return member, ok
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
	case hidesCharacters(r.Label):
		return fmt.Errorf("%w: a delegate's label holds no control, format, or separator character, such as a zero-width space, a byte order mark, or a bidirectional control", ErrInvalid)
	case Reserved(r.Label):
		return fmt.Errorf("%w: the label %q is reserved for the assistant's own credentials", ErrInvalid, AssistantLabel)
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
