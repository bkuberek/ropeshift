// Package localauth is the local passphrase behind IdentityProvider. The ledger holds an
// argon2id hash, and the terminal is the only place a passphrase comes from.
package localauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Member is the one member a local passphrase signs in.
const Member = "local"

// eventProofSet records that a passphrase was set, and holds no part of it.
const eventProofSet = "member.proof_set"

const (
	saltBytes = 16
	hashBytes = 32
)

// Params are the costs of one hash.
type Params struct {
	Time      uint32
	MemoryKiB uint32
	Threads   uint8
}

// DefaultParams is one of the two sets RFC 9106 recommends.
var DefaultParams = Params{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

// Provider checks a passphrase against the ledger. It hashes one at a time, so calls on a
// socket cannot take the machine's memory, and each guess costs its full price.
type Provider struct {
	store   ports.Store
	params  Params
	hashing sync.Mutex
}

// New returns a provider that hashes new passphrases at these costs.
func New(store ports.Store, params Params) *Provider {
	return &Provider{store: store, params: params}
}

// Enrolled reports whether a passphrase is set.
func (p *Provider) Enrolled(ctx context.Context, scope ledger.WorkspaceID) (bool, error) {
	var count int64
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		count, err = tx.Members().CountMembers()
		return err
	})
	return count > 0, err
}

// Enroll sets the passphrase. The hash is made outside the transaction, which decides
// whether this caller may replace what is there.
func (p *Provider) Enroll(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof, actor ledger.Actor) error {
	if len(proof.Passphrase) < authority.MinPassphrase {
		return fmt.Errorf("%w: a passphrase has at least %d bytes", authority.ErrInvalid, authority.MinPassphrase)
	}
	digest, err := p.hash(proof.Passphrase)
	if err != nil {
		return err
	}
	return p.store.Write(ctx, scope, "member.enroll", func(tx ports.WriteTx) error {
		_, err := tx.Members().Member(Member)
		if err == nil && !authority.Allowed(actor, authority.ActionSetPassphrase) {
			return authority.ErrNoSession
		}
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		err = tx.Members().SaveMember(authority.MemberRecord{
			Member:      authority.Member{ID: Member, IdentitySubject: Member, DisplayName: "Local member", Status: authority.MemberActive},
			ProofDigest: digest,
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: eventProofSet, Actor: actor, Subject: ledger.Subject{Kind: "member", ID: Member}})
		return err
	})
}

// Authenticate names the member a passphrase belongs to. It holds no transaction while it
// hashes.
func (p *Provider) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	var record authority.MemberRecord
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Members().Member(Member)
		return err
	})
	if errors.Is(err, ports.ErrNotFound) {
		return "", authority.ErrNotEnrolled
	}
	if err != nil {
		return "", err
	}
	match, err := p.matches(proof.Passphrase, record.ProofDigest)
	if err != nil {
		return "", err
	}
	if !match {
		return "", authority.ErrBadProof
	}
	return record.ID, nil
}

// hash encodes a passphrase in the PHC form, so a stored hash carries its own costs and a
// later build can raise them.
func (p *Provider) hash(passphrase string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := p.derive(passphrase, salt, p.params, hashBytes)
	encoding := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.params.MemoryKiB, p.params.Time, p.params.Threads,
		encoding.EncodeToString(salt), encoding.EncodeToString(sum)), nil
}

func (p *Provider) matches(passphrase, digest string) (bool, error) {
	params, salt, want, err := decode(digest)
	if err != nil {
		return false, err
	}
	got := p.derive(passphrase, salt, params, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func (p *Provider) derive(passphrase string, salt []byte, params Params, size uint32) []byte {
	p.hashing.Lock()
	defer p.hashing.Unlock()
	return argon2.IDKey([]byte(passphrase), salt, params.Time, params.MemoryKiB, params.Threads, size)
}

func decode(digest string) (Params, []byte, []byte, error) {
	parts := strings.Split(digest, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, errors.New("the stored passphrase hash has no form this build reads")
	}
	var version int
	var params Params
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("the stored passphrase hash has version %q", parts[2])
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.MemoryKiB, &params.Time, &params.Threads); err != nil {
		return Params{}, nil, nil, fmt.Errorf("the stored passphrase hash has costs %q", parts[3])
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, err
	}
	sum, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, err
	}
	return params, salt, sum, nil
}

var _ ports.IdentityProvider = (*Provider)(nil)
