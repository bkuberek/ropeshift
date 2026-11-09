package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// effectNote is what a split variant's first write holds in place of the event that matters.
const effectNote = "effect_note"

// raiseUnknown tells the ledger of each effect no observation can settle, so a person hears of it. The
// journal keeps that it did in the write that stores the event, so a crash repeats nothing and loses
// nothing. Everything else a crash left unknown waits for an observation, and troubles nobody.
func (w *Worker) raiseUnknown(ctx context.Context, unknown []lanes.ObservedEffect, records map[string]lanes.Record) {
	for _, observed := range unknown {
		effect := observed.Effect
		if effect.Kind.Observable() {
			continue
		}
		payload, err := json.Marshal(ledger.EffectReport{Key: effect.Key, Kind: string(effect.Kind), Target: effect.Target, Attempt: effect.Attempts})
		if err != nil {
			w.config.Report(fmt.Errorf("effect %s: %w", effect.Key, err))
			continue
		}
		record, known := records[effect.Lane]
		if !known {
			record = lanes.Record{ID: effect.Lane}
		}
		record.Generation = effect.Generation
		event := w.event(ledger.UploadEffectUnknown, record, payload)
		first, err := w.journal.ReportEffect(ctx, effect.Key, effect.Attempts, heldEvent(event))
		if err == nil && first {
			err = w.afterSplit(ctx, event)
		}
		if err != nil {
			w.config.Report(fmt.Errorf("effect %s: %w", effect.Key, err))
			continue
		}
		crashpoint.Hit("worker.effect", "raised")
	}
}

// heldEvent is what a journal write keeps with an effect's mark: the event itself, in any normal build.
func heldEvent(event ledger.UploadEvent) ledger.UploadEvent {
	if splitsEffect() {
		event.Type = effectNote
	}
	return event
}

// afterSplit stores in a second write what a build split apart. Only a broken variant reaches this.
func (w *Worker) afterSplit(ctx context.Context, event ledger.UploadEvent) error {
	if !splitsEffect() {
		return nil
	}
	crashpoint.Hit("worker.effect", "split")
	_, err := w.journal.Append(ctx, event)
	return err
}

// settleDecided acts on what a person said. The journal applies a word once and stores the event that
// stops the offer in the same write, so a crash at any point leaves the word applied and owed to the
// ledger, or not applied at all.
func (w *Worker) settleDecided(ctx context.Context) {
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading what a person decided: %w", err))
		return
	}
	for _, decided := range state.Effects {
		payload, err := json.Marshal(ledger.EffectSettlement{ID: decided.ID})
		if err != nil {
			w.config.Report(fmt.Errorf("effect %s: %w", decided.Key, err))
			continue
		}
		event := w.event(ledger.UploadEffectSettled, lanes.Record{}, payload)
		first, err := w.journal.SettleEffect(ctx, verdictOf(decided), heldEvent(event))
		if err == nil && first {
			err = w.afterSplit(ctx, event)
		}
		if err != nil {
			w.config.Report(fmt.Errorf("effect %s: %w", decided.Key, err))
			continue
		}
		if first {
			crashpoint.Hit("worker.effect", "settled")
		}
	}
}

// verdictOf says what a person's word does to an effect.
func verdictOf(decided ledger.DesiredEffect) lanes.EffectVerdict {
	verdict := lanes.EffectVerdict{ID: decided.ID, Key: decided.Key, Attempt: decided.Attempt}
	switch decided.Outcome {
	case ledger.EffectHappened:
		verdict.State, verdict.Detail = lanes.EffectCompleted, lanes.DetailSaidHappened
	case ledger.EffectRetry:
		verdict.Retry = true
	default:
		verdict.State, verdict.Detail = lanes.EffectFailed, lanes.DetailSaidNotHappened
	}
	return verdict
}
