//go:build chaos

package chaos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// The broken variant of a word about an effect, which the oracle below must catch.
const sabotageSplitsEffect = "a word about an effect and its event in two journal writes"

// effectPatience is how long a child gives a person's word to come back. Only a broken variant runs it
// out, and the oracle then says what went missing.
const effectPatience = 5 * time.Second

func unknownEffectOf(lane string) lanes.Effect {
	return lanes.NewEffect(lanes.EffectInput, lane, 1, "", "nudge")
}

// roleWorkerEffect stands where a restart leaves a worker: an input it cannot account for. It passes
// the way a daemon does, and stands in for the person, who answers the moment a word is asked of them.
func roleWorkerEffect(c *child) error {
	if c.spec.Variant == sabotageSplitsEffect {
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsEffect)
	}
	journal, err := sqlite.OpenJournal(c.spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.Migrate(c.ctx); err != nil {
		return err
	}
	client := api.NewUnixClient(c.spec.Socket)
	defer client.Close()
	profiles := fake.NewProfileSource()
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      fake.NewAgentDriver(),
			Provisioner: worker.Provisioner{Profiles: profiles, Worktrees: fake.NewWorktrees()},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string) (string, error) { return filepath.Dir(c.spec.Journal), nil },
		},
	})
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	if _, err := apitest.SignIn(c.ctx, client); err != nil {
		return err
	}
	effect := unknownEffectOf(c.spec.Lane)
	if _, err := journal.BeginEffect(c.ctx, effect); err != nil {
		return err
	}
	if _, err := journal.MarkUnknown(c.ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(effectPatience)
	for time.Now().Before(deadline) {
		if err := subject.Sync(c.ctx); err != nil {
			return err
		}
		if _, err := subject.Reconcile(c.ctx); err != nil {
			return err
		}
		if err := subject.Upload(c.ctx); err != nil {
			return err
		}
		rows, err := client.Effects(c.ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Key != effect.Key {
				continue
			}
			if row.SettledAt != 0 {
				return nil
			}
			if _, err := client.DecideEffect(c.ctx, row.ID, api.DecideEffect{Outcome: ledger.EffectHappened}); err != nil {
				return err
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func effectSpec(w *world) (childSpec, error) {
	w.serveOnce()
	spec, err := w.workerSpec("worker-effect")
	spec.Lane = w.rig.ID("lane")
	return spec, err
}

// effectAccounted is the oracle of a word about an effect, at every step of it. A person heard once,
// the worker acted once, and the ledger knows it did, whatever a kill cut short.
func effectAccounted(w *world, spec childSpec) error {
	key := unknownEffectOf(spec.Lane).Key
	var rows []ledger.UnknownEffect
	var events []ledger.Event
	err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Effects().Effects()
		if err != nil {
			return err
		}
		rows = slices.DeleteFunc(all, func(row ledger.UnknownEffect) bool { return row.Key != key })
		events, err = tx.Events(0, 100_000)
		return err
	})
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return contract.Violatedf("%d rows for one attempt at an effect, so a person heard of it that many times", len(rows))
	}
	row := rows[0]
	if row.Outcome != ledger.EffectHappened {
		return contract.Violatedf("no word came back: %+v", row)
	}
	if row.SettledAt == 0 {
		return contract.Violatedf("the worker holds a word the ledger still offers: %+v", row)
	}
	journal, err := sqlite.OpenJournal(spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	effect, err := journal.Effect(w.rig.Ctx, key)
	if err != nil {
		return err
	}
	if effect.State != lanes.EffectCompleted || effect.Attempts != 1 {
		return contract.Violatedf("the word did not land once: %+v", effect)
	}
	book, err := ledger.ReplayEffects(events)
	if err != nil {
		return err
	}
	if book[row.ID] != row {
		return contract.Violatedf("the row reads %+v, and replays to %+v", row, book[row.ID])
	}
	return nil
}

func workerEffectCase(name string, steps ...string) crashCase {
	return crashCase{name: name, op: "worker.effect", steps: steps, prepare: effectSpec, settled: effectAccounted}
}

// effectCases kills every step of a word about an effect.
func effectCases() []crashCase {
	return []crashCase{workerEffectCase("a word about an effect", "raised", "settled")}
}

func effectVariants() []variant {
	return []variant{{
		name: sabotageSplitsEffect, check: "crash",
		crash: func() (crashCase, string) { return workerEffectCase("a split word", "split"), "split" },
	}}
}
