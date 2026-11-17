package contract

import (
	"errors"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// JoinTokenChecks hold what a store says of a join token's use and revocation, which every database answers alike:
// a token no worker used is revoked and refused afterwards, and a token a worker used is not revoked, whatever the
// database stores for a grant nobody used.
var JoinTokenChecks = []Check{
	{"JT1", "a join token no worker used is revoked and refused after, and revoking it again changes nothing", checkRevokeAnUnusedToken},
	{"JT2", "revoking a join token a worker used is refused as used and changes neither the grant nor the worker's credential", checkRevokeAUsedToken},
}

// grantOf reads a join grant as the installation holds it.
func (r *Rig) grantOf(s ports.Store, id string) (ledger.JoinGrant, error) {
	var grant ledger.JoinGrant
	err := s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		grant, err = tx.Workers().JoinGrant(id)
		return err
	})
	return grant, err
}

func (r *Rig) joinPlane(s ports.Store) *control.Workers {
	return control.NewWorkers(s, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
}

var tokenMaker = ledger.Actor{Kind: ledger.ActorMember, ID: "member"}

func checkRevokeAnUnusedToken(r *Rig) error {
	s := r.Store()
	plane := r.joinPlane(s)
	made, err := plane.MakeJoinToken(r.Ctx, tokenMaker, 0)
	if err != nil {
		return err
	}
	if grant, err := r.grantOf(s, made.ID); err != nil || grant.UsedBy != "" || grant.RevokedAt != 0 || grant.ExpiresAt != made.ExpiresAt || grant.ExpiresAt == 0 {
		return Violatedf("a new grant holds %+v: %v", grant, err)
	}
	if err := plane.RevokeJoinToken(r.Ctx, tokenMaker, made.ID); err != nil {
		return Violatedf("revoking an unused token: %v", err)
	}
	revoked, err := r.grantOf(s, made.ID)
	if err != nil || revoked.RevokedAt == 0 || revoked.UsedBy != "" {
		return Violatedf("a revoked grant holds %+v: %v", revoked, err)
	}
	if err := plane.RevokeJoinToken(r.Ctx, tokenMaker, made.ID); err != nil {
		return Violatedf("revoking a token again: %v", err)
	}
	if again, err := r.grantOf(s, made.ID); err != nil || again != revoked {
		return Violatedf("a second revocation changed the grant from %+v to %+v: %v", revoked, again, err)
	}
	if _, err := plane.Register(r.Ctx, ledger.Registration{JoinToken: made.Token, WorkerID: r.ID("worker"), Name: "w"}); !errors.Is(err, ledger.ErrJoinTokenRevoked) {
		return Violatedf("a revoked token registers a worker: %v", err)
	}
	if err := plane.RevokeJoinToken(r.Ctx, tokenMaker, "no-such-token"); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("revoking a token that is not there: %v", err)
	}
	return nil
}

func checkRevokeAUsedToken(r *Rig) error {
	s := r.Store()
	plane := r.joinPlane(s)
	made, err := plane.MakeJoinToken(r.Ctx, tokenMaker, 0)
	if err != nil {
		return err
	}
	worker := r.ID("worker")
	credential, err := plane.Register(r.Ctx, ledger.Registration{JoinToken: made.Token, WorkerID: worker, Name: "w"})
	if err != nil {
		return err
	}
	used, err := r.grantOf(s, made.ID)
	if err != nil || used.UsedBy != worker || used.RevokedAt != 0 {
		return Violatedf("a used grant holds %+v: %v", used, err)
	}
	if err := plane.RevokeJoinToken(r.Ctx, tokenMaker, made.ID); !errors.Is(err, ledger.ErrJoinTokenUsed) {
		return Violatedf("revoking a token a worker used answers %v, and wants ErrJoinTokenUsed", err)
	}
	if after, err := r.grantOf(s, made.ID); err != nil || after != used {
		return Violatedf("a refused revocation changed the grant from %+v to %+v: %v", used, after, err)
	}
	// The worker's credential stands, so the revocation took nothing from it.
	if _, err := plane.OpenSession(r.Ctx, ledger.SessionRequest{
		Auth: ledger.WorkerAuth{WorkerID: worker, Credential: credential.Credential}, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest,
	}); err != nil {
		return Violatedf("the credential of a worker whose token was refused a revocation: %v", err)
	}
	// A retry before the session reissued the credential, and after it the used token is a spent one.
	if _, err := plane.Register(r.Ctx, ledger.Registration{JoinToken: made.Token, WorkerID: worker, Name: "w"}); !errors.Is(err, ledger.ErrBadJoinToken) {
		return Violatedf("a retry after the first session answers %v", err)
	}
	return nil
}
