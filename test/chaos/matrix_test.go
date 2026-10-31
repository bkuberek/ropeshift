//go:build chaos

package chaos

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// transactionSteps are the steps every write transaction passes. Random kills
// alone miss some windows, so the matrix kills at each one (R9).
var transactionSteps = []string{"begun", "opened", "before-commit", "committed"}

// fencedStep is the step every fenced write passes after its guard.
const fencedStep = "guarded"

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
	return []crashCase{
		createCase(),
		claimCase(),
		renewCase(),
		sweepCase(),
		lapseCase(),
		returnCase(),
		releaseCase(),
		reassignCase(),
		laneStartCase(),
		fencedWriteCase(),
		handoffCase(),
	}
}

const longTTL = int64(60_000)

func (w *world) claimed(ttlMs int64) (workorders.Grant, error) {
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return workorders.Grant{}, err
	}
	request := workorders.ClaimRequest{WorkOrder: id, ClaimID: w.rig.ID("claim"), Worker: "parent", TTLMs: ttlMs}
	return w.rig.NewOps(w.store).Claim(w.rig.Ctx, w.rig.Scope, request, w.rig.Actor)
}

// running returns a holder in `running` with a live lease.
func (w *world) running(ttlMs int64) (workorders.Holder, error) {
	grant, err := w.claimed(ttlMs)
	if err != nil {
		return workorders.Holder{}, err
	}
	lane := w.rig.ID("lane")
	generation, err := w.rig.NewOps(w.store).StartLane(w.rig.Ctx, w.rig.Scope, grant.Lease, lane, w.rig.Actor)
	if err != nil {
		return workorders.Holder{}, err
	}
	return workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}, nil
}

// lapsed returns a holder whose lease expired in real time and was swept.
func (w *world) lapsed() (workorders.Holder, error) {
	holder, err := w.running(1)
	if err != nil {
		return workorders.Holder{}, err
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := w.rig.NewOps(w.store).Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
		return workorders.Holder{}, err
	}
	return holder, nil
}

func holderSpec(role string, h workorders.Holder) childSpec {
	return childSpec{
		Role: role, WorkOrder: h.WorkOrder, ClaimID: h.ClaimID, Fence: h.Fence,
		Lane: h.Lane, Generation: h.Generation, TTLMs: longTTL,
	}
}

func hasState(w *world, id string, want workorders.State) error {
	got, err := w.rig.WorkOrder(w.store, id)
	if err != nil {
		return err
	}
	if got.State != want {
		return contract.Violatedf("%s is %s, want %s", id, got.State, want)
	}
	return nil
}

func claimCase() crashCase {
	return crashCase{
		name:  "claim",
		op:    "claim",
		steps: withSteps("eligible", "granted", "recorded"),
		prepare: func(w *world) (childSpec, error) {
			id, err := w.rig.Ready(w.store)
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "claim", WorkOrder: id, ClaimID: w.rig.ID("child-claim"), Worker: "worker-0", TTLMs: longTTL}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, grantEventType(), spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Claimed)
		},
	}
}

func renewCase() crashCase {
	return crashCase{
		name:  "renewal",
		op:    sqlstore.OpRenew,
		steps: []string{"begun", "before-commit", "committed"},
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			return holderSpec("renew", holder), err
		},
		settled: func(w *world, spec childSpec) error {
			return hasState(w, spec.WorkOrder, workorders.Running)
		},
	}
}

func sweepCase() crashCase {
	return crashCase{
		name:  "sweep",
		op:    "sweep",
		steps: []string{"candidates-read"},
		prepare: func(w *world) (childSpec, error) {
			grant, err := w.claimed(1)
			time.Sleep(10 * time.Millisecond)
			return childSpec{Role: "sweep", WorkOrder: grant.WorkOrder}, err
		},
		settled: func(w *world, spec childSpec) error {
			return hasState(w, spec.WorkOrder, workorders.Lapsed)
		},
	}
}

func lapseCase() crashCase {
	expired := workorders.EventType(workorders.TriggerLeaseExpired)
	return crashCase{
		name:  "lapse",
		op:    "lapse",
		steps: withSteps("lapsed"),
		prepare: func(w *world) (childSpec, error) {
			grant, err := w.claimed(1)
			time.Sleep(10 * time.Millisecond)
			return childSpec{Role: "sweep", WorkOrder: grant.WorkOrder}, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, expired, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Lapsed)
		},
	}
}

func returnCase() crashCase {
	returned := workorders.EventType(workorders.TriggerReturnedStarted)
	return crashCase{
		name:  "return",
		op:    "return",
		steps: withSteps("restored"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.lapsed()
			return holderSpec("return", holder), err
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, returned, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Running)
		},
	}
}

func releaseCase() crashCase {
	released := workorders.EventType(workorders.TriggerClaimReleased)
	return crashCase{
		name:  "release",
		op:    "release",
		steps: withSteps("released"),
		prepare: func(w *world) (childSpec, error) {
			grant, err := w.claimed(longTTL)
			return holderSpec("release", workorders.Holder{Lease: grant.Lease}), err
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, released, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Ready)
		},
	}
}

func reassignCase() crashCase {
	takeover := workorders.EventType(workorders.TriggerTakeover)
	return crashCase{
		name:  "reassignment",
		op:    "reassign",
		steps: withSteps("revoked"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.lapsed()
			return holderSpec("reassign", holder), err
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, takeover, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Ready)
		},
	}
}

func laneStartCase() crashCase {
	started := workorders.EventType(workorders.TriggerLaneStarted)
	return crashCase{
		name:  "lane start",
		op:    "lane.start",
		steps: withSteps("started"),
		prepare: func(w *world) (childSpec, error) {
			grant, err := w.claimed(longTTL)
			spec := holderSpec("lane-start", workorders.Holder{Lease: grant.Lease})
			spec.Lane = w.rig.ID("lane")
			return spec, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, started, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Running)
		},
	}
}

// fencedWriteCase needs no exact count: a write whose answer was lost can repeat,
// and U06 removes the duplicate by its sequence number.
func fencedWriteCase() crashCase {
	return crashCase{
		name:  "fenced write",
		op:    "fenced_write",
		steps: withSteps(fencedStep),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			spec := holderSpec("fenced-write", holder)
			spec.Key = w.rig.ID("child-write")
			return spec, err
		},
		settled: func(w *world, spec childSpec) error {
			return hasState(w, spec.WorkOrder, workorders.Running)
		},
	}
}

func handoffCase() crashCase {
	return crashCase{
		name:  "handoff",
		op:    "lane.handoff",
		steps: withSteps(fencedStep),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			return holderSpec("handoff", holder), err
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, "work_order.lane_handoff", spec.WorkOrder, 1)
		},
	}
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

// namedSteps reads every crashpoint.Hit in the source. A step with a variable op
// belongs to every write transaction, and one with a literal op to that operation.
func namedSteps(t *testing.T) (generic []string, literal []string) {
	t.Helper()
	for _, root := range []string{"../../internal/control", "../../internal/adapters/sqlstore"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Hit" {
					return true
				}
				step, err := strconv.Unquote(call.Args[1].(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				switch op := call.Args[0].(type) {
				case *ast.BasicLit:
					name, err := strconv.Unquote(op.Value)
					if err != nil {
						t.Fatal(err)
					}
					literal = append(literal, name+"/"+step)
				case *ast.Ident:
					if op.Name == "OpRenew" {
						literal = append(literal, sqlstore.OpRenew+"/"+step)
					} else {
						generic = append(generic, step)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return generic, literal
}

// TestEveryNamedStepIsInTheMatrix fails when a new step has no row, because a
// random run alone can leave a step unhit (R9).
func TestEveryNamedStepIsInTheMatrix(t *testing.T) {
	generic, literal := namedSteps(t)
	if len(generic) == 0 || len(literal) == 0 {
		t.Fatalf("the source scan found generic %v and literal %v", generic, literal)
	}
	for _, step := range transactionSteps {
		if !slices.Contains(generic, step) {
			t.Errorf("transaction step %s is not in the source", step)
		}
	}
	for _, step := range generic {
		if !slices.Contains(transactionSteps, step) && step != fencedStep {
			t.Errorf("transaction step %s has no matrix column", step)
		}
	}
	rows := map[string]bool{}
	for _, c := range crashMatrix() {
		for _, step := range c.steps {
			rows[c.op+"/"+step] = true
		}
	}
	for _, step := range literal {
		if !rows[step] {
			t.Errorf("named step %s has no matrix row", step)
		}
	}
}
