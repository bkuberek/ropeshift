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

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
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
	// between runs after the kill and before the retry, for what lands while nothing runs.
	between func(*world, childSpec) error
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
	if c.between != nil {
		if err := c.between(w, spec); err != nil {
			return err
		}
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
	return append([]crashCase{
		createCase(),
		claimCase(),
		renewCase(),
		sweepCase(),
		lapseCase(),
		returnCase(),
		releaseCase(),
		reassignCase(),
		laneStartCase(),
		dispatchCase(),
		laneEndCase(),
		workerLaneCase(),
		fencedWriteCase(),
		laneHandoffCase(),
		intakeCase("intake", []string{"polled"}),
		intakeCase("intake.item", withSteps("versioned", "ordered")),
		intakeCase("intake.settle", withSteps()),
		intakeFilesCase(),
		repinCase("repinned"),
		reviewCase(),
		sendBackCase(),
		recordQuestionCase("indexed"),
		recordDecisionCase(),
		supersedeCase(),
		ruleCase(),
		overturnCase(),
		acceptCase(),
		reworkCase(),
		settleCase(),
		publishPolicyCase(),
		addCategoryCase(),
		askCase("question.ask", withSteps("recorded", "attached")),
		askCase("question.record", []string{"indexed"}),
		takeCase(),
		climbCase(),
		escalateCase(),
		deliverCase(),
		answerCase(),
		resolveCase(),
		mergeCase(),
		dismissCase(),
		recordCase(),
		signInCase(),
		workerStartCase(),
		uploadCase("worker.upload", []string{"read", "answered", "acknowledged"}),
		uploadCase("journal.append", []string{"appended"}),
		worktreeCase(),
	}, slices.Concat(reconcileCases(), stopCases(), handoffCases(), wakeCases(), parkCases())...)
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
func dispatchCase() crashCase {
	return crashCase{
		name:  "dispatch a lane",
		op:    "lane.dispatch",
		steps: withSteps("recorded"),
		prepare: func(w *world) (childSpec, error) {
			grant, err := w.claimed(longTTL)
			return childSpec{Role: "dispatch", WorkOrder: grant.WorkOrder, Lane: w.rig.ID("lane")}, err
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, workorders.EventLaneDispatched, spec.WorkOrder, 1)
		},
	}
}

func laneEndCase() crashCase {
	return crashCase{
		name:  "end a lane",
		op:    "lane.end",
		steps: withSteps("ended", "evidence"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			return holderSpec("end-lane", holder), err
		},
		settled: func(w *world, spec childSpec) error {
			if err := hasState(w, spec.WorkOrder, workorders.Acceptance); err != nil {
				return err
			}
			if err := evidenceHeld(w, spec.WorkOrder, 2, 1); err != nil {
				return err
			}
			return countEvents(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder, 1)
		},
	}
}

// evidenceHeld counts what stayed linked to a work order. An outcome standing without what it
// rests on is the failure this looks for, and so is one written twice.
func evidenceHeld(w *world, id string, artifacts, checks int) error {
	return w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		held, err := tx.Evidence().Artifacts(id)
		if err != nil {
			return err
		}
		rows, err := tx.Evidence().Checks(id)
		if err != nil {
			return err
		}
		if len(held) != artifacts || len(rows) != checks {
			return contract.Violatedf("%s holds %d artifacts and %d checks, want %d and %d", id, len(held), len(rows), artifacts, checks)
		}
		return nil
	})
}

// awaitingWork leaves a work order in `acceptance`, on a lane's own check.
func awaitingWork(w *world, commit string) (string, error) {
	return w.rig.Awaiting(w.store, []string{"c1"}, contract.LaneFinished(commit, contract.LaneCheck("c1", true)))
}

func acceptCase() crashCase {
	return crashCase{
		name:  "accept a work order",
		op:    "acceptance.accept",
		steps: withSteps("moved"),
		prepare: func(w *world) (childSpec, error) {
			id, err := awaitingWork(w, w.rig.ID("commit"))
			return childSpec{Role: "accept", WorkOrder: id}, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := hasState(w, spec.WorkOrder, workorders.Done); err != nil {
				return err
			}
			return countEvents(w, workorders.EventType(workorders.TriggerAcceptancePassed), spec.WorkOrder, 1)
		},
	}
}

func reworkCase() crashCase {
	return crashCase{
		name:  "send work back",
		op:    "acceptance.rework",
		steps: withSteps("moved"),
		prepare: func(w *world) (childSpec, error) {
			id, err := awaitingWork(w, w.rig.ID("commit"))
			return childSpec{Role: "rework", WorkOrder: id}, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := hasState(w, spec.WorkOrder, workorders.Ready); err != nil {
				return err
			}
			return countEvents(w, workorders.EventType(workorders.TriggerRework), spec.WorkOrder, 1)
		},
	}
}

// settleCase kills a tick between keeping a forge's result and acting on it. The two commit
// together, so a retry reads the forge again and ends with one of each.
func settleCase() crashCase {
	return crashCase{
		name:  "settle on a forge result",
		op:    "acceptance.settle",
		steps: withSteps("recorded", "moved"),
		prepare: func(w *world) (childSpec, error) {
			commit := w.rig.ID("commit")
			id, err := w.rig.Awaiting(w.store, []string{"c1"}, contract.LaneFinished(commit))
			return childSpec{Role: "settle", WorkOrder: id, Key: commit}, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := hasState(w, spec.WorkOrder, workorders.Done); err != nil {
				return err
			}
			if err := evidenceHeld(w, spec.WorkOrder, 2, 1); err != nil {
				return err
			}
			return countEvents(w, workorders.EventType(workorders.TriggerAcceptancePassed), spec.WorkOrder, 1)
		},
	}
}

// workerLaneCase kills a real worker at each step of a lane's run, with a fake driver.
// Spawning is not idempotent, so the retry must account for the lane it finds and
// never start it again, and the ledger must hold one start and one end.
func workerLaneCase() crashCase {
	return crashCase{
		name:  "run a lane",
		op:    "worker.lane",
		steps: []string{"begun", "spawned", "ended"},
		prepare: func(w *world) (childSpec, error) {
			w.serveOnce()
			id, err := w.rig.Ready(w.store)
			if err != nil {
				return childSpec{}, err
			}
			spec, err := w.workerSpec("worker-lane")
			spec.WorkOrder, spec.ClaimID, spec.Lane = id, w.rig.ID("claim"), w.rig.ID("lane")
			return spec, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, workorders.EventType(workorders.TriggerLaneStarted), spec.WorkOrder, 1); err != nil {
				return err
			}
			completed, err := countOf(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder)
			if err != nil {
				return err
			}
			retried, err := countOf(w, workorders.EventType(workorders.TriggerLaneFailedRetry), spec.WorkOrder)
			if err != nil {
				return err
			}
			if completed+retried != 1 {
				return contract.Violatedf("%d outcomes on record for one lane", completed+retried)
			}
			return nil
		},
	}
}

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

func laneHandoffCase() crashCase {
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

func intakeCase(op string, steps []string) crashCase {
	return crashCase{
		name:  op,
		op:    op,
		steps: steps,
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "intake", Key: w.rig.ID("alpha")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return intakeSettled(w, spec.Key)
		},
	}
}

// intakeSettled proves a killed poll finished on its retry: one version, and the
// work orders that version names.
func intakeSettled(w *world, id string) error {
	contracts := 0
	orders := 0
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	for _, e := range events {
		switch e.Type {
		case planning.EventContractVersion:
			contracts++
		case workorders.EventCreated:
			orders++
		}
	}
	if contracts != 1 || orders != 1 {
		return contract.Violatedf("%s: %d versions and %d work orders, want one of each", id, contracts, orders)
	}
	return nil
}

// recordCase is ledger work: a contract and its work order in one transaction.
// signInCase kills a sign-in at each step. A retry opens a session of its own, because a
// token dies with the process that held it, and each session keeps its record.
func signInCase() crashCase {
	return crashCase{
		name:  "sign in",
		op:    "member.sign_in",
		steps: withSteps("issued"),
		prepare: func(*world) (childSpec, error) {
			return childSpec{Role: "sign-in"}, nil
		},
		settled: func(w *world, _ childSpec) error { return w.rig.CheckSessions(w.store) },
	}
}

func recordCase() crashCase {
	return crashCase{
		name:  "record ledger work",
		op:    "work_order.record",
		steps: withSteps(),
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "record-work", WorkOrder: w.rig.ID("wo")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, workorders.EventCreated, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.ContractReview)
		},
	}
}

// inReview polls one contract and returns its work order, in `contract_review`.
func (w *world) inReview(key string) (workorders.WorkOrder, error) {
	source := fake.NewWorkSource()
	source.Write("contract.md", []byte(contract.ContractText(key, "Alpha", "One.")))
	if _, err := w.rig.NewOps(w.store).Poll(w.rig.Ctx, w.rig.Scope, source); err != nil {
		return workorders.WorkOrder{}, err
	}
	rows, err := w.rows()
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	if len(rows) != 1 {
		return workorders.WorkOrder{}, fmt.Errorf("%d rows", len(rows))
	}
	return rows[0], nil
}

// repinCase kills a poll of a contract's second version, while its work order is ready.
func repinCase(step string) crashCase {
	return crashCase{
		name:  "a new version",
		op:    "intake.item",
		steps: []string{step},
		prepare: func(w *world) (childSpec, error) {
			key := w.rig.ID("alpha")
			order, err := w.inReview(key)
			if err != nil {
				return childSpec{}, err
			}
			if _, err := w.rig.NewOps(w.store).Pass(w.rig.Ctx, w.rig.Scope, order.ID, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "intake-version", Key: key, WorkOrder: order.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			row, err := w.rig.WorkOrder(w.store, spec.WorkOrder)
			if err != nil {
				return err
			}
			if row.State != workorders.ContractReview || row.ContractVersion != 2 || row.ReviewedBy != "" {
				return contract.Violatedf("a new version left %+v", row)
			}
			return countEvents(w, workorders.EventContractPinned, spec.WorkOrder, 1)
		},
	}
}

func intakeFilesCase() crashCase {
	return crashCase{
		name:  "goal, plan, and requirement files",
		op:    "intake.file",
		steps: withSteps("recorded"),
		prepare: func(*world) (childSpec, error) {
			return childSpec{Role: "intake-files"}, nil
		},
		settled: func(w *world, _ childSpec) error {
			events, err := w.rig.Events(w.store, w.rig.Scope)
			if err != nil {
				return err
			}
			count := map[string]int{}
			for _, e := range events {
				count[e.Type]++
			}
			for _, eventType := range []string{planning.EventGoalRecorded, planning.EventPlanRecorded, planning.EventRequirementVersion} {
				if count[eventType] != 1 {
					return contract.Violatedf("%d %s events, want 1", count[eventType], eventType)
				}
			}
			return nil
		},
	}
}

func sendBackCase() crashCase {
	return crashCase{
		name:  "send back",
		op:    "review.send_back",
		steps: withSteps("moved"),
		prepare: func(w *world) (childSpec, error) {
			order, err := w.inReview(w.rig.ID("alpha"))
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "send-back", WorkOrder: order.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, workorders.EventType(workorders.TriggerContractChange), spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Planning)
		},
	}
}

// findable proves a record reached search as well as its table.
func findable(w *world, kind, id string) error {
	var hits []ports.SearchHit
	err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		hits, err = tx.Search(ports.SearchQuery{Text: "retry budget", Kinds: []string{kind}})
		return err
	})
	if err != nil {
		return err
	}
	if len(hits) != 1 || hits[0].RecordID != id {
		return contract.Violatedf("search found %+v, want %s", hits, id)
	}
	return nil
}

func recordQuestionCase(step string) crashCase {
	return crashCase{
		name:  "record a question",
		op:    "question.record",
		steps: withSteps(step),
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "record-question", Key: w.rig.ID("q")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, questions.EventQuestionChanged, spec.Key, 1); err != nil {
				return err
			}
			return findable(w, questions.KindQuestion, spec.Key)
		},
	}
}

func recordDecisionCase() crashCase {
	return crashCase{
		name:  "record a decision",
		op:    "decision.record",
		steps: withSteps("indexed"),
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "record-decision", Key: w.rig.ID("d")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, questions.EventDecisionRecorded, spec.Key, 1); err != nil {
				return err
			}
			return findable(w, questions.KindDecision, spec.Key)
		},
	}
}

// supersedeCase kills a decision that replaces another. It must leave the old one in force
// with no new one, or both done.
func supersedeCase() crashCase {
	return crashCase{
		name:  "replace a decision",
		op:    "decision.record",
		steps: []string{"replaced"},
		prepare: func(w *world) (childSpec, error) {
			old, err := w.rig.Assumed(w.store, w.rig.ID("Which log level do retries use"), "")
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "supersede", Key: w.rig.ID("d"), ClaimID: old.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, questions.EventDecisionEnded, spec.ClaimID, 1); err != nil {
				return err
			}
			return decisionIs(w, spec.ClaimID, questions.DecisionReplaced)
		},
	}
}

func decisionIs(w *world, id string, want questions.DecisionStatus) error {
	return w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Questions().Decision(id)
		if err != nil {
			return err
		}
		if row.Status != want {
			return contract.Violatedf("decision %s is %s, want %s", id, row.Status, want)
		}
		return nil
	})
}

// ruleCase kills rung 3. It must leave an open question with no decision, or an answered one.
func ruleCase() crashCase {
	return crashCase{
		name:  "decide on rung 3",
		op:    "question.rule",
		steps: withSteps("decided"),
		prepare: func(w *world) (childSpec, error) {
			id, _, err := w.rig.OnRung(w.store, w.rig.ID("What is the retry budget"), questions.RungDecide, nil)
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "rule", Key: id, ClaimID: w.rig.ID("d")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := w.questionIs(spec.Key, questions.StatusAnswered, ""); err != nil {
				return err
			}
			return countEvents(w, questions.EventDecisionRecorded, spec.ClaimID, 1)
		},
	}
}

// overturnCase kills an overturn at each of its five steps. Nothing may stand half done, and
// the retry must finish with one of everything.
func overturnCase() crashCase {
	return crashCase{
		name:  "overturn a decision",
		op:    "decision.overturn",
		steps: withSteps("overturned", "succeeded", "reworked", "noticed", "measured"),
		prepare: func(w *world) (childSpec, error) {
			done, err := w.rig.Completed(w.store, true)
			if err != nil {
				return childSpec{}, err
			}
			old, err := w.rig.Assumed(w.store, w.rig.ID("Which log level do retries use"), done)
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "overturn", Key: old.ID, ClaimID: w.rig.ID("d"), WorkOrder: done}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := decisionIs(w, spec.Key, questions.DecisionOverturned); err != nil {
				return err
			}
			for eventType, subject := range map[string]string{
				questions.EventDecisionEnded:    spec.Key,
				questions.EventDecisionRecorded: spec.ClaimID,
				questions.EventOutcomeFilled:    spec.Key,
			} {
				if err := countEvents(w, eventType, subject, 1); err != nil {
					return err
				}
			}
			return reworkOnce(w, spec.WorkOrder)
		},
	}
}

func reworkOnce(w *world, of string) error {
	return w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		found := 0
		for _, row := range rows {
			if row.ReworkOf == of {
				found++
			}
		}
		if found != 1 {
			return contract.Violatedf("%d rework orders of %s", found, of)
		}
		return nil
	})
}

// publishPolicyCase names the next version, so each kill publishes a new one.
func publishPolicyCase() crashCase {
	return crashCase{
		name:  "publish a policy",
		op:    "policy.publish",
		steps: withSteps("inserted"),
		prepare: func(w *world) (childSpec, error) {
			versions, err := w.rig.NewOps(w.store).PolicyVersions(w.rig.Ctx, w.rig.Scope)
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "publish-policy", Generation: versions[0].Version + 1}, nil
		},
		settled: func(w *world, spec childSpec) error {
			versions, err := w.rig.NewOps(w.store).PolicyVersions(w.rig.Ctx, w.rig.Scope)
			if err != nil {
				return err
			}
			if versions[0].Version != spec.Generation {
				return contract.Violatedf("version %d is in force, want %d", versions[0].Version, spec.Generation)
			}
			return nil
		},
	}
}

// askCase kills an ask on a ready work order. It must leave one question, the lane on it,
// and the work back in review, or nothing at all.
func askCase(op string, steps []string) crashCase {
	return crashCase{
		name:  "ask on " + op,
		op:    op,
		steps: steps,
		prepare: func(w *world) (childSpec, error) {
			order, err := w.inReview(w.rig.ID("alpha"))
			if err != nil {
				return childSpec{}, err
			}
			if _, err := w.rig.NewOps(w.store).Pass(w.rig.Ctx, w.rig.Scope, order.ID, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "ask", Key: w.rig.ID("q"), Lane: "lane-a", WorkOrder: order.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, questions.EventQuestionAttached, spec.Key, 1); err != nil {
				return err
			}
			if err := findable(w, questions.KindQuestion, spec.Key); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.ContractReview)
		},
	}
}

// heldQuestion records a question and gives it to the orchestrator that a child acts as.
func (w *world) heldQuestion(text string, rungs ...int64) (string, error) {
	draft := questions.Question{ID: w.rig.ID("q"), Text: text, Categories: []string{questions.CategoryProduct}}
	ops := w.rig.NewOps(w.store)
	if _, err := ops.RecordQuestion(w.rig.Ctx, w.rig.Scope, draft, w.rig.Actor); err != nil {
		return "", err
	}
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	if _, err := ops.Take(w.rig.Ctx, w.rig.Scope, draft.ID, longTTL, holder); err != nil {
		return "", err
	}
	for _, rung := range rungs {
		if _, err := ops.Climb(w.rig.Ctx, w.rig.Scope, draft.ID, questions.Step{Rung: rung}, holder); err != nil {
			return "", err
		}
	}
	return draft.ID, nil
}

func takeCase() crashCase {
	return crashCase{
		name:  "take a question",
		op:    "question.take",
		steps: withSteps("taken"),
		prepare: func(w *world) (childSpec, error) {
			draft := questions.Question{ID: w.rig.ID("q"), Text: "What is the retry budget?"}
			if _, err := w.rig.NewOps(w.store).RecordQuestion(w.rig.Ctx, w.rig.Scope, draft, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "take", Key: draft.ID, TTLMs: longTTL}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return w.questionIs(spec.Key, questions.StatusTriage, "orchestrator-1")
		},
	}
}

func (w *world) questionIs(id string, status questions.Status, holder string) error {
	var row questions.Question
	err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Questions().Question(id)
		return err
	})
	if err != nil {
		return err
	}
	if row.Status != status || row.ClaimedBy != holder {
		return contract.Violatedf("question %+v, want %s held by %q", row, status, holder)
	}
	return nil
}

func climbCase() crashCase {
	return crashCase{
		name:  "climb a rung",
		op:    "question.climb",
		steps: withSteps("stepped"),
		prepare: func(w *world) (childSpec, error) {
			id, err := w.heldQuestion("What is the retry budget?")
			return childSpec{Role: "climb", Key: id}, err
		},
		settled: func(w *world, spec childSpec) error {
			var row questions.Question
			err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
				var err error
				row, err = tx.Questions().Question(spec.Key)
				return err
			})
			if err != nil {
				return err
			}
			if row.Rung != questions.RungOpen || len(row.LadderTrace) != 1 {
				return contract.Violatedf("question %+v", row)
			}
			return nil
		},
	}
}

func escalateCase() crashCase {
	return crashCase{
		name:  "escalate",
		op:    "question.escalate",
		steps: withSteps("passed", "gathered"),
		prepare: func(w *world) (childSpec, error) {
			id, err := w.heldQuestion("May audit data leave the region?", questions.RungLookup, questions.RungOpen)
			return childSpec{Role: "escalate", Key: id}, err
		},
		settled: func(w *world, spec childSpec) error {
			return w.questionIs(spec.Key, questions.StatusNeedsHuman, "")
		},
	}
}

// dueBatch puts one escalated question in a batch whose window already closed. A crash run
// keeps real time, so the batch is opened under a window of one millisecond.
func (w *world) dueBatch() (string, string, error) {
	id, err := w.heldQuestion("May audit data leave the region?", questions.RungLookup, questions.RungOpen)
	if err != nil {
		return "", "", err
	}
	quick := control.NewQuestions(w.store, questions.BatchPolicy{WindowMs: 1, MaxWaitMs: 1})
	record, err := quick.Escalate(w.rig.Ctx, w.rig.Scope, id, questions.Escalation{
		Reason: questions.ReasonOneWay, Options: []string{"Keep", "Allow"}, Recommendation: "Keep",
	}, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"})
	if err != nil {
		return "", "", err
	}
	time.Sleep(5 * time.Millisecond)
	return id, record.BatchID, nil
}

func deliverCase() crashCase {
	return crashCase{
		name:  "deliver a batch",
		op:    "question.deliver",
		steps: withSteps("closed"),
		prepare: func(w *world) (childSpec, error) {
			_, batch, err := w.dueBatch()
			return childSpec{Role: "deliver", Key: batch}, err
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, questions.EventBatchDelivered, spec.Key, 1)
		},
	}
}

func answerCase() crashCase {
	return crashCase{
		name:  "answer a question",
		op:    "question.answer",
		steps: withSteps("decided"),
		prepare: func(w *world) (childSpec, error) {
			draft := questions.Question{ID: w.rig.ID("q"), Text: "What is the retry budget?"}
			if _, err := w.rig.NewOps(w.store).RecordQuestion(w.rig.Ctx, w.rig.Scope, draft, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "answer", Key: draft.ID, ClaimID: w.rig.ID("d")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := w.questionIs(spec.Key, questions.StatusAnswered, ""); err != nil {
				return err
			}
			if err := countEvents(w, questions.EventDecisionRecorded, spec.ClaimID, 1); err != nil {
				return err
			}
			return findable(w, questions.KindDecision, spec.ClaimID)
		},
	}
}

func resolveCase() crashCase {
	return crashCase{
		name:  "resolve",
		op:    "question.resolve",
		steps: withSteps("answered"),
		prepare: func(w *world) (childSpec, error) {
			id, err := w.heldQuestion("What is the retry budget?")
			if err != nil {
				return childSpec{}, err
			}
			decision := questions.Decision{ID: w.rig.ID("d"), Status: questions.DecisionDecided, Statement: "Three."}
			if _, err := w.rig.NewOps(w.store).RecordDecision(w.rig.Ctx, w.rig.Scope, decision, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "resolve", Key: id, ClaimID: decision.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return w.questionIs(spec.Key, questions.StatusAnswered, "")
		},
	}
}

func mergeCase() crashCase {
	return crashCase{
		name:  "merge",
		op:    "question.merge",
		steps: withSteps("moved"),
		prepare: func(w *world) (childSpec, error) {
			ops := w.rig.NewOps(w.store)
			lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-b"}
			asked, err := ops.Ask(w.rig.Ctx, w.rig.Scope, questions.Ask{ID: w.rig.ID("q"), Text: "How many retries?", Lane: "lane-b", Generation: 1}, lane)
			if err != nil {
				return childSpec{}, err
			}
			holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
			if _, err := ops.Take(w.rig.Ctx, w.rig.Scope, asked.QuestionID, longTTL, holder); err != nil {
				return childSpec{}, err
			}
			target := questions.Question{ID: w.rig.ID("q"), Text: "What is the retry budget?"}
			if _, err := ops.RecordQuestion(w.rig.Ctx, w.rig.Scope, target, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "merge", Key: asked.QuestionID, ClaimID: target.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := w.questionIs(spec.Key, questions.StatusDismissed, ""); err != nil {
				return err
			}
			return countEvents(w, questions.EventQuestionAttached, spec.ClaimID, 1)
		},
	}
}

func dismissCase() crashCase {
	return crashCase{
		name:  "dismiss",
		op:    "question.dismiss",
		steps: withSteps(),
		prepare: func(w *world) (childSpec, error) {
			draft := questions.Question{ID: w.rig.ID("q"), Text: "What is the retry budget?"}
			if _, err := w.rig.NewOps(w.store).RecordQuestion(w.rig.Ctx, w.rig.Scope, draft, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "dismiss", Key: draft.ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return w.questionIs(spec.Key, questions.StatusDismissed, "")
		},
	}
}

func addCategoryCase() crashCase {
	return crashCase{
		name:  "add a category",
		op:    "category.add",
		steps: withSteps(),
		prepare: func(*world) (childSpec, error) {
			return childSpec{Role: "add-category", Key: "pricing"}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, questions.EventCategoryAdded, spec.Key, 1)
		},
	}
}

func reviewCase() crashCase {
	return crashCase{
		name:  "review",
		op:    "review.pass",
		steps: withSteps("recorded"),
		prepare: func(w *world) (childSpec, error) {
			source := fake.NewWorkSource()
			source.Write("contract.md", []byte(contract.ContractText(w.rig.ID("alpha"), "Alpha", "One.")))
			ops := w.rig.NewOps(w.store)
			if _, err := ops.Poll(w.rig.Ctx, w.rig.Scope, source); err != nil {
				return childSpec{}, err
			}
			rows, err := w.rows()
			if err != nil || len(rows) != 1 {
				return childSpec{}, fmt.Errorf("%d rows: %v", len(rows), err)
			}
			return childSpec{Role: "review-pass", WorkOrder: rows[0].ID}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, workorders.EventReviewPassed, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Ready)
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
	for _, root := range []string{
		"../../internal/control", "../../internal/adapters/sqlstore", "../../internal/adapters/sqlite",
		"../../internal/adapters/gitcli", "../../internal/adapters/claudecode", "../../internal/worker",
	} {
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
	for _, step := range paneSteps {
		rows[paneOp+"/"+step.name] = true
	}
	for _, step := range restartSteps {
		rows[restartOp+"/"+step] = true
	}
	// These steps exist only in the broken variants that split the write they name.
	rows["worker.handoff/split"] = true
	rows["worker.park/split"] = true
	for _, step := range literal {
		if !rows[step] {
			t.Errorf("named step %s has no matrix row", step)
		}
	}
}
