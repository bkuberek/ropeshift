package worker

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectremote"
)

// tendForgeAccountReads answers each forge account read that desired state offers this worker: it reads the
// person's `gh` sign-in and reports whether it can create repositories, and under which owners. A read has no
// side effect, so it keeps no journal: one still offered is answered again, and the control plane records the first
// report.
func (w *Worker) tendForgeAccountReads(ctx context.Context) {
	rt := w.config.Templates
	if rt == nil {
		return
	}
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading the forge accounts this worker is asked to read: %w", err))
		return
	}
	for _, request := range state.ForgeAccountReads {
		key := "forge-account-read/" + request.ID
		if !w.takeEffect(key) {
			continue
		}
		w.goTracked("forge account read", func() {
			defer w.leaveEffect(key)
			if err := w.runForgeAccountRead(ctx, rt, request); err != nil {
				w.config.Report(fmt.Errorf("forge account read %s: %w", request.ID, err))
			}
		})
	}
}

func (w *Worker) runForgeAccountRead(ctx context.Context, rt *TemplateRuntime, request ledger.DesiredForgeAccountRead) error {
	if w.clock.Now().UnixMilli() >= request.ExpiresAt {
		return nil
	}
	report, err := projectremote.ReadAccount(ctx, w.projectRuntime(rt))
	if err != nil {
		return err
	}
	report.Request = request.ID
	report.Auth = w.Auth()
	if err := w.link.ReportForgeAccount(ctx, report); err != nil {
		return fmt.Errorf("reporting it: %w", err)
	}
	return nil
}
