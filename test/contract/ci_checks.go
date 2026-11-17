package contract

import (
	"errors"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// CIChecks are a repository's `[ci]` settings (ADR-0037): the keys the control plane reads CI by, kept on the
// repository's record with its other settings, audited, and changed only by a member.
var CIChecks = []Check{
	{"CI1", "a repository's CI keys are kept with its record, read back, and audited as one change", checkCIKeysAreKept},
	{"CI2", "a CI key outside its range changes nothing, and only a member changes one", checkCIKeysAreBounded},
}

func checkCIKeysAreKept(r *Rig) error {
	s := r.Store()
	record, err := r.detect(s, "repo-1", "", "main")
	if err != nil {
		return err
	}
	if record.CI.Custom || record.CI.Effective().SteadyInterval != "5m" {
		return Violatedf("a record nobody edited reads %+v", record.CI)
	}
	steady, err := record.CI.Set("ci_steady_interval", "2m")
	if err != nil {
		return err
	}
	steady, err = steady.Set("ci_auto_rerun", "0")
	if err != nil {
		return err
	}
	changed, err := r.change(s, "repo-1", control.RepositoryChange{CI: &steady}, r.Actor)
	if err != nil {
		return err
	}
	if changed.CI != steady {
		return Violatedf("the change left %+v", changed.CI)
	}
	var read ledger.Repository
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		read, err = tx.Repositories().Repository("repo-1")
		return err
	})
	if err != nil {
		return err
	}
	effective := read.CI.Effective()
	if effective.SteadyInterval != "2m" || effective.AutoRerun != 0 || effective.NoHistoryP50 != "10m" {
		return Violatedf("the record reads %+v", effective)
	}
	events, err := r.repositoryEvents(s, "repo-1")
	if err != nil {
		return err
	}
	if last := events[len(events)-1]; last.Type != ledger.EventRepositoryChanged || last.Actor != r.Actor {
		return Violatedf("the last event is %s from %+v", last.Type, last.Actor)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{CI: &steady}, r.Actor); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a change that changes nothing moved the ledger from %d to %d: %v", before, after, err)
	}
	return nil
}

func checkCIKeysAreBounded(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	bad := ledger.CISettings{Custom: true, SteadyInterval: "10s"}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{CI: &bad}, r.Actor); !errors.Is(err, ledger.ErrInvalidRepository) {
		return Violatedf("a steady interval of 10 seconds read %v", err)
	}
	good := ledger.CISettings{}.Effective()
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorOrchestrator, ID: "o"}, {Kind: ledger.ActorLane, ID: "l"}, {Kind: ledger.ActorWorker, ID: "w"}} {
		if _, err := r.change(s, "repo-1", control.RepositoryChange{CI: &good}, actor); err == nil {
			return Violatedf("%s changed a repository's CI keys", actor.Kind)
		}
	}
	var read ledger.Repository
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		read, err = tx.Repositories().Repository("repo-1")
		return err
	})
	if err != nil {
		return err
	}
	if read.CI.Custom {
		return Violatedf("a refused change left %+v", read.CI)
	}
	return nil
}
