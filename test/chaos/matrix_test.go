//go:build chaos

package chaos

import (
	"errors"
	"fmt"
	"testing"

	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// transactionSteps are the steps every write transaction passes. Random kills
// alone miss some windows, so the matrix kills at each one (R9).
var transactionSteps = []string{"begun", "opened", "before-commit", "committed"}

// crashCase is one operation in the matrix.
type crashCase struct {
	name string
	// op is the prefix of the operation's named steps.
	op string
	// steps lists every step a kill must reach.
	steps []string
	// prepare builds the state before the operation, in the parent.
	prepare func(*world) (childSpec, error)
	// settled checks the end state after the retry.
	settled func(*world, childSpec) error
}

func withSteps(extra ...string) []string {
	return append(append([]string{}, transactionSteps...), extra...)
}

func createCase() crashCase {
	return crashCase{
		name:  "create a work order",
		op:    "work_order.create",
		steps: withSteps(),
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "create-work-order", WorkOrder: w.rig.ID("wo")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, workorders.EventCreated, spec.WorkOrder, 1)
		},
	}
}

func countEvents(w *world, eventType, subject string, want int) error {
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	got := 0
	for _, e := range events {
		if e.Type == eventType && e.Subject.ID == subject {
			got++
		}
	}
	if got != want {
		return contract.Violatedf("%d %s events for %s, want %d", got, eventType, subject, want)
	}
	return nil
}

// runCrashCase kills a child at one step, checks that nothing is half-applied,
// retries the operation in a new process, and checks the end state.
func runCrashCase(w *world, c crashCase, step string) error {
	spec, err := c.prepare(w)
	if err != nil {
		return err
	}
	spec = w.spec(spec)
	spec.KillOp, spec.KillStep = c.op, step
	code, signaled, stderr, err := runToEnd(spec)
	if err != nil {
		return err
	}
	if !signaled {
		return fmt.Errorf("%s/%s was never reached: exit %d: %s", c.op, step, code, stderr)
	}
	if err := w.verify(); err != nil {
		return err
	}
	spec.KillOp, spec.KillStep = "", ""
	code, signaled, stderr, err = runToEnd(spec)
	if err != nil {
		return err
	}
	if signaled || code != exitDone {
		return fmt.Errorf("the retry after %s/%s: exit %d: %s", c.op, step, code, stderr)
	}
	if err := w.verify(); err != nil {
		return err
	}
	if c.settled != nil {
		return c.settled(w, spec)
	}
	return nil
}

func crashMatrix() []crashCase {
	return []crashCase{createCase()}
}

// TestCrashMatrix kills a real process at every named step of every operation.
func TestCrashMatrix(t *testing.T) {
	kills := 0
	for _, c := range crashMatrix() {
		for _, step := range c.steps {
			t.Run(c.name+"/"+step, func(t *testing.T) {
				w := newWorld(t, "")
				if err := runCrashCase(w, c, step); err != nil {
					var violation *contract.Violation
					if errors.As(err, &violation) {
						t.Fatalf("violation: %v", err)
					}
					t.Fatal(err)
				}
			})
			kills++
		}
	}
	t.Logf("crash matrix: %d named steps, each killed once", kills)
}
