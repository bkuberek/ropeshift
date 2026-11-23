package contract

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// OrchestratorEndChecks hold that a store keeps why an orchestrator's run ended, which Needs you shows while a member
// still wants it to run, and that the next run clears it.
var OrchestratorEndChecks = []Check{
	{"OX1", "an orchestrator's record keeps the reason and the cause of its run's end, and a save without them clears them", checkAnOrchestratorKeepsItsEnd},
}

func checkAnOrchestratorKeepsItsEnd(r *Rig) error {
	s := r.Store()
	worker := r.ID("worker")
	row := ledger.Orchestrator{
		ID: ledger.OrchestratorOf(worker), WorkerID: worker, Desired: ledger.OrchestratorRunning, Run: 1, Actual: ledger.StateEnded,
		EndReason: "spawn_failed", EndDetail: "the pane did not open",
	}
	save := func(row ledger.Orchestrator, note string) error {
		return s.Installation(r.Scope).Write(r.Ctx, "test.orchestrator", func(tx ports.SharedTx) error {
			if err := tx.Workers().SaveOrchestrator(row); err != nil {
				return err
			}
			_, err := tx.Append(r.noteDraft(r.ID(note)))
			return err
		})
	}
	read := func() (ledger.Orchestrator, error) {
		var got ledger.Orchestrator
		err := s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
			var err error
			got, err = tx.Workers().Orchestrator(worker)
			return err
		})
		return got, err
	}
	if err := save(row, "ended"); err != nil {
		return err
	}
	got, err := read()
	if err != nil {
		return err
	}
	if got.EndReason != row.EndReason || got.EndDetail != row.EndDetail {
		return Violatedf("the record reads %+v, want the end %q: %q", got, row.EndReason, row.EndDetail)
	}
	row.Run, row.Actual, row.EndReason, row.EndDetail = 2, "", "", ""
	if err := save(row, "next"); err != nil {
		return err
	}
	if got, err = read(); err != nil {
		return err
	}
	if got.Run != 2 || got.EndReason != "" || got.EndDetail != "" {
		return Violatedf("the next run reads %+v, want no end", got)
	}
	return nil
}
