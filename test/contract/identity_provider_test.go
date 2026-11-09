package contract

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	anyCaller = ledger.Actor{Kind: ledger.ActorSystem, ID: "first-enrollment"}
	aMember   = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
)

// cheapHash keeps the contract fast. A stored hash carries its own costs, so nothing else
// changes with them.
var cheapHash = localauth.Params{Time: 1, MemoryKiB: 8, Threads: 1}

func TestIdentityProviderContract(t *testing.T) {
	t.Run("local passphrase", func(t *testing.T) {
		RunIdentityProvider(t, func(t *testing.T) IdentityRig {
			rig := NewRig(t, SQLiteFile)
			return IdentityRig{Provider: localauth.New(rig.Store(), cheapHash), Scope: rig.Scope, Member: aMember, Anyone: anyCaller}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunIdentityProvider(t, func(*testing.T) IdentityRig {
			return IdentityRig{Provider: fake.NewIdentityProvider(), Scope: "workspace", Member: aMember, Anyone: anyCaller}
		})
	})
}

// TestTheLedgerHoldsOnlyAHash reads what a local passphrase leaves behind: its hash, with
// no trace of the passphrase in the record or the events.
func TestTheLedgerHoldsOnlyAHash(t *testing.T) {
	rig := NewRig(t, SQLiteFile)
	provider := localauth.New(rig.Store(), cheapHash)
	if err := provider.Enroll(rig.Ctx, rig.Scope, ports.Proof{Passphrase: goodPassphrase}, anyCaller); err != nil {
		t.Fatal(err)
	}
	err := rig.Store().Read(rig.Ctx, rig.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Members().Member(localauth.Member)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(record.ProofDigest, "$argon2id$") || strings.Contains(record.ProofDigest, goodPassphrase) {
			t.Errorf("digest %q", record.ProofDigest)
		}
		events, err := tx.Events(0, 100)
		if err != nil {
			return err
		}
		for _, event := range events {
			if strings.Contains(string(event.Data), goodPassphrase) || strings.Contains(string(event.Data), record.ProofDigest) {
				t.Errorf("event %s holds a secret", event.Type)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
