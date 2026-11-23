package worker_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// answeredClaims is a control plane link whose answer to a claim a test sets.
type answeredClaims struct {
	ports.WorkerLink
	mu     sync.Mutex
	answer error
}

func (l *answeredClaims) answers(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answer = err
}

func (l *answeredClaims) Claim(context.Context, ledger.WorkerAuth, workorders.ClaimRequest) (workorders.Grant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return workorders.Grant{}, l.answer
}

// wakeRefusals lists what the worker reported of a refused claim of a wake.
func (r *laneRig) wakeRefusals() []string {
	var reported []string
	for _, problem := range r.problems() {
		if strings.Contains(problem.Error(), "the claim of its wake was refused") {
			reported = append(reported, problem.Error())
		}
	}
	return reported
}

// A woken lane the ledger refuses a claim for waits, as any lane that cannot be claimed does, and the worker says why once:
// the same refusal on each pass says nothing more, a refusal that reads differently is said again, and a claim that
// goes through forgets what was said.
func TestARefusedWakeClaimIsReportedOnce(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	link := &answeredClaims{WorkerLink: base.link}
	base.workerLink = link
	w := newWorldRigOn(t, base)
	if err := w.journal.SaveDesired(t.Context(), ledger.DesiredState{
		Rev: 1, Woken: []ledger.DesiredWoken{{WorkOrder: "wo-lane-1", Lane: "lane-1", Fence: 2}},
	}); err != nil {
		t.Fatal(err)
	}
	pass := func() {
		t.Helper()
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	paused := fmt.Errorf("%w: the project is paused", workorders.ErrIneligible)
	link.answers(paused)
	pass()
	pass()
	if got := w.wakeRefusals(); len(got) != 1 || !strings.Contains(got[0], "lane lane-1: the claim of its wake was refused") || !strings.Contains(got[0], "the project is paused") {
		t.Fatalf("two passes under one refusal reported %q, want it once", got)
	}

	link.answers(fmt.Errorf("%w: the work order is held", workorders.ErrIneligible))
	pass()
	pass()
	if got := w.wakeRefusals(); len(got) != 2 || !strings.Contains(got[1], "the work order is held") {
		t.Fatalf("a different refusal reported %q, want it said again once", got)
	}

	link.answers(workorders.ErrLost)
	pass()
	if got := w.wakeRefusals(); len(got) != 2 {
		t.Errorf("a lost race reported %q, want no more", got)
	}

	link.answers(nil)
	pass()
	if err := w.journal.SaveDesired(t.Context(), ledger.DesiredState{
		Rev: 2, Woken: []ledger.DesiredWoken{{WorkOrder: "wo-lane-1", Lane: "lane-1", Fence: 3}},
	}); err != nil {
		t.Fatal(err)
	}
	link.answers(paused)
	pass()
	if got := w.wakeRefusals(); len(got) != 3 || !strings.Contains(got[2], "the project is paused") {
		t.Errorf("a refusal after a claim went through reported %q, want it said again", got)
	}
}
