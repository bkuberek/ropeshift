package authority

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestPartyOf(t *testing.T) {
	tests := []struct {
		kind  ledger.ActorKind
		party Party
		agent bool
	}{
		{ledger.ActorMember, PartyPerson, false},
		{ledger.ActorOrchestrator, PartyOrchestrator, true},
		{ledger.ActorLane, PartyLane, true},
		{ledger.ActorWorker, PartySystem, false},
		{ledger.ActorSystem, PartySystem, false},
		{"", PartySystem, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			got := PartyOf(ledger.Actor{Kind: tt.kind, ID: "x"})
			if got != tt.party || got.Agent() != tt.agent {
				t.Errorf("%s reads as %s, agent %v", tt.kind, got, got.Agent())
			}
		})
	}
}
