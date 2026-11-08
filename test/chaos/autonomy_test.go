//go:build chaos

package chaos

import (
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/test/contract"
)

// aWorker registers a worker in the parent, because an autonomy record names one.
func (w *world) aWorker() (string, error) {
	plane := control.NewWorkers(w.store, w.rig.Scope, control.WorkerOptions{HeartbeatMs: 1000, LeaseTTLMs: longTTL})
	token, err := plane.CreateJoinToken(w.rig.Ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		return "", err
	}
	id := w.rig.ID("worker")
	if _, err := plane.Register(w.rig.Ctx, ledger.Registration{JoinToken: token, WorkerID: id, Name: "w"}); err != nil {
		return "", err
	}
	return id, nil
}

func roleAcknowledgeRisk(c *child) error {
	return control.NewAutonomy(c.store).AcknowledgeRisk(c.ctx, c.scope, c.spec.Worker, authority.RiskDigest(), c.member())
}

func roleSetLevel(c *child) error {
	return control.NewAutonomy(c.store).SetLevel(c.ctx, c.scope, c.spec.Worker, authority.LevelManual, c.member())
}

func roleOpenWindow(c *child) error {
	_, err := control.NewAutonomy(c.store).OpenWindow(c.ctx, c.scope, c.spec.Worker, c.spec.Key, 0, c.member())
	return err
}

func roleCloseWindow(c *child) error {
	return control.NewAutonomy(c.store).CloseWindow(c.ctx, c.scope, c.spec.Worker, c.member())
}

func autonomyView(w *world, worker string) (authority.AutonomyView, error) {
	return control.NewAutonomy(w.store).View(w.rig.Ctx, w.rig.Scope, worker)
}

func autonomyCases() []crashCase {
	return []crashCase{
		{
			name: "acknowledge the risk", op: "autonomy.acknowledge", steps: withSteps("saved"),
			prepare: func(w *world) (childSpec, error) {
				id, err := w.aWorker()
				return childSpec{Role: "acknowledge-risk", Worker: id}, err
			},
			settled: func(w *world, spec childSpec) error {
				if err := countEvents(w, authority.EventRiskAcknowledged, spec.Worker, 1); err != nil {
					return err
				}
				view, err := autonomyView(w, spec.Worker)
				if err != nil {
					return err
				}
				if !view.Acknowledged {
					return contract.Violatedf("the retry left no acknowledgement: %+v", view)
				}
				return nil
			},
		},
		{
			name: "choose a level", op: "autonomy.set", steps: withSteps("saved"),
			prepare: func(w *world) (childSpec, error) {
				id, err := w.aWorker()
				return childSpec{Role: "set-level", Worker: id}, err
			},
			settled: func(w *world, spec childSpec) error {
				if err := countEvents(w, authority.EventAutonomySet, spec.Worker, 1); err != nil {
					return err
				}
				view, err := autonomyView(w, spec.Worker)
				if err != nil {
					return err
				}
				if view.Level != authority.LevelManual {
					return contract.Violatedf("the retry left %+v", view)
				}
				return nil
			},
		},
		{
			name: "open a run window", op: "autonomy.window_open", steps: withSteps("inserted"),
			prepare: func(w *world) (childSpec, error) {
				id, err := w.aWorker()
				return childSpec{Role: "open-window", Worker: id, Key: w.rig.ID("window")}, err
			},
			settled: func(w *world, spec childSpec) error {
				if err := countEvents(w, authority.EventWindowOpened, spec.Worker, 1); err != nil {
					return err
				}
				view, err := autonomyView(w, spec.Worker)
				if err != nil {
					return err
				}
				if view.Window == nil || view.Window.ID != spec.Key {
					return contract.Violatedf("the retry left %+v", view)
				}
				return nil
			},
		},
		{
			name: "close a run window", op: "autonomy.window_close", steps: withSteps("closed"),
			prepare: func(w *world) (childSpec, error) {
				id, err := w.aWorker()
				if err != nil {
					return childSpec{}, err
				}
				_, err = control.NewAutonomy(w.store).OpenWindow(w.rig.Ctx, w.rig.Scope, id, w.rig.ID("window"), 0, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
				return childSpec{Role: "close-window", Worker: id}, err
			},
			settled: func(w *world, spec childSpec) error {
				if err := countEvents(w, authority.EventWindowClosed, spec.Worker, 1); err != nil {
					return err
				}
				view, err := autonomyView(w, spec.Worker)
				if err != nil {
					return err
				}
				if view.Window != nil {
					return contract.Violatedf("the retry left %+v", view)
				}
				return nil
			},
		},
	}
}

func autonomyVariants() []variant {
	return []variant{
		{name: "a claim that reads no level", control: control.Sabotage{IgnoresAutonomy: true}, check: "AU4"},
		{name: "a change the log never hears of", control: control.Sabotage{DropsAutonomyEvent: true}, check: "AU13"},
		{
			name: "a window read past its end", check: "AU6",
			sabotage: sqlstore.Sabotage{Rewrite: replace("run_window.open", "closes_at > CAST(:now AS BIGINT)", "closes_at > 0")},
		},
		{
			name: "a window a member closed read as open", check: "AU6",
			sabotage: sqlstore.Sabotage{Rewrite: replace("run_window.open", "AND closed_at = 0", "")},
		},
		{
			name: "a store that forgets who acknowledged", check: "AU13",
			sabotage: sqlstore.Sabotage{Rewrite: replace("autonomy.save", "acknowledged_by = :acknowledged_by,", "acknowledged_by = '',")},
		},
	}
}
