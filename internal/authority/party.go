package authority

import "ropeshift.dev/ropeshift/internal/ledger"

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
	}
	return PartySystem
}

// Agent says an orchestrator or a lane acted: an agent, whose choices the overturn rate holds to
// account.
func (p Party) Agent() bool { return p == PartyOrchestrator || p == PartyLane }
