package authority

import "ropeshift.com/ropeshift/internal/ledger"

// Party says who an actor is for a record that keeps who acted: a person, an orchestrator, a lane, or
// the system. It grants nothing. Allowed is the one place a permission is decided, and this sits
// beside it so no other package compares an actor's kind (FR-AUTH-9).
type Party string

// The parties.
const (
	PartyPerson       Party = "person"
	PartyOrchestrator Party = "orchestrator"
	PartyLane         Party = "lane"
	PartySystem       Party = "system"
	// PartyDelegate is an agent outside ropeshift acting with a member's delegate credential. It is not
	// the person, so nothing it does counts as a person's word.
	PartyDelegate Party = "delegate"
)

// PartyOf reads one actor. A member is a person, and a worker or a tick is the system.
func PartyOf(actor ledger.Actor) Party {
	switch actor.Kind {
	case ledger.ActorMember:
		return PartyPerson
	case ledger.ActorOrchestrator:
		return PartyOrchestrator
	case ledger.ActorLane:
		return PartyLane
	case ledger.ActorDelegate:
		return PartyDelegate
	}
	return PartySystem
}

// Agent says an orchestrator, a lane, or a delegate acted: an agent, whose choices the overturn rate holds
// to account.
func (p Party) Agent() bool { return p == PartyOrchestrator || p == PartyLane || p == PartyDelegate }
