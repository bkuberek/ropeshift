//go:build chaos

package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/contract"
)

// sabotageSplitsApproval keeps a request and what tells the ledger in two journal writes.
const sabotageSplitsApproval = "a request and its upload in two journal writes"

const (
	// witnessCallRan records a call that ran, which nothing a worker wrote can vouch for.
	witnessCallRan = "call-ran"
	approvalInput  = `{"command":"git tag v1"}`
	approvalCall   = "call-1"
	approvalPoll   = 5 * time.Millisecond
	approvalLimit  = 10 * time.Second
	// approvalProfile waits long enough for a member, and leaves a lost request a short way out.
	approvalProfile = "[lifecycle]\nhandoff_timeout = \"300ms\"\nmax_restarts = 0\napproval_wait = \"3s\"\n"
)

func applyApprovalVariant(variant string) {
	if variant == sabotageSplitsApproval {
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsApproval)
	}
}

// approvalDriver is a session whose one call waits on a decision. Whichever process takes a look at
// it, that look is the same call, so a look a kill interrupted is taken again.
type approvalDriver struct {
	*handoffDriver
	client  *api.Client
	decides sync.Once
}

func (d *approvalDriver) look(spec ports.SessionSpec, token string, raised bool) lanes.Reply {
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: token,
		Approval: &lanes.ApprovalRequest{Tool: "Bash", Input: json.RawMessage(approvalInput), Call: approvalCall, Raised: raised},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "session:", err)
		return lanes.Reply{}
	}
	return d.subject.AnswerLane(d.ctx, line)
}

// decide stands for a member. A member's call is safe to repeat, and the digest is the request's.
func (d *approvalDriver) decide() {
	d.decides.Do(func() {
		go func() {
			if _, err := apitest.SignIn(d.ctx, d.client); err != nil {
				fmt.Fprintln(os.Stderr, "member:", err)
				return
			}
			for d.ctx.Err() == nil {
				rows, err := d.client.Approvals(d.ctx, authority.ApprovalPending)
				if err == nil && len(rows) == 1 {
					_, err = d.client.DecideApproval(d.ctx, rows[0].ID, api.DecideApproval{Digest: rows[0].SubjectDigest, Allow: true})
					if err == nil {
						return
					}
				}
				time.Sleep(approvalPoll)
			}
		}()
	})
}

// waits runs the call: it looks until the look says what to do, and a call runs only on an allow.
func (d *approvalDriver) waits(token string) fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		if spec.Env != nil {
			token = tokenOf(spec)
		}
		raised := false
		for deadline := time.Now().Add(approvalLimit); d.ctx.Err() == nil && time.Now().Before(deadline); time.Sleep(approvalPoll) {
			reply := d.look(spec, token, raised)
			if !reply.OK || reply.Approval == nil {
				continue
			}
			raised = true
			d.decide()
			switch reply.Approval.Decision {
			case lanes.ApprovalAllow:
				if err := d.witness(witnessCallRan, approvalCall); err != nil {
					fmt.Fprintln(os.Stderr, "session:", err)
				}
				return
			case lanes.ApprovalDeny:
				return
			}
		}
	}}
}

func (d *approvalDriver) script(token string) fake.DriverScript {
	finish := fake.DriverStep{Do: func(ports.SessionSpec) {
		records, err := d.journal.Lanes(context.Background())
		if err != nil || len(records) != 1 {
			fmt.Fprintln(os.Stderr, "lanes:", len(records), err)
			return
		}
		d.report(ports.SessionSpec{}, records[0].ID)
	}}
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{
		{{Bound: true}, d.waits(token), finish, turn},
	}}
}

func (d *approvalDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if err := os.WriteFile(d.marker("token"), []byte(tokenOf(spec)), 0o600); err != nil {
		return ports.SessionHandle{}, err
	}
	d.Play(d.script(""))
	return d.lastingDriver.Start(ctx, spec)
}

// Adopt plays the rest of a session the last worker left, which still holds the token it started
// with, because that process did not end.
func (d *approvalDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	token, err := os.ReadFile(d.marker("token"))
	if err != nil {
		return err
	}
	d.Play(d.script(string(token)))
	lane, _, _ := strings.Cut(h.ID, "/")
	d.Leave(lane, generationOf(h.ID), lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

// roleWorkerApproval runs one lane through one decision on a real worker.
func roleWorkerApproval(c *child) error {
	return runHandoffWorker(c, func(d *handoffDriver) ports.AgentDriver {
		return &approvalDriver{handoffDriver: d, client: api.NewUnixClient(c.spec.Socket)}
	})
}

// decidedOnce is the oracle of a wait. Whichever step a kill landed on, one request stands for the
// call, one decision was spent on it, the call ran once, and the lane ended once.
func decidedOnce(w *world, spec childSpec) error {
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder, 1); err != nil {
		return err
	}
	rows, err := control.NewApprovals(w.store).List(context.Background(), w.rig.Scope, "")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].State != authority.ApprovalUsed {
		return contract.Violatedf("requests %+v", rows)
	}
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	ran := 0
	for _, record := range records {
		if record.Op == witnessCallRan {
			ran++
		}
	}
	if ran > 1 {
		return contract.Violatedf("the call ran %d times on one decision", ran)
	}
	return nil
}

func approvalCase(name, op string, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps, settled: decidedOnce,
		prepare: func(w *world) (childSpec, error) {
			spec, err := handoffSpec(w, "")
			spec.Role, spec.Profile = "worker-approval", approvalProfile
			return spec, err
		},
	}
}

// decideCase kills a process at each step of a decision, and the retry is the same decision.
func decideCase() crashCase {
	return crashCase{
		name: "decide a request", op: "approval.decide", steps: withSteps("saved", "recorded"),
		prepare: func(w *world) (childSpec, error) {
			call, err := authority.ReadToolCall("Bash", []byte(approvalInput))
			if err != nil {
				return childSpec{}, err
			}
			request := authority.Approval{
				ID: w.rig.ID("approval"), Lane: "lane-1", WorkOrder: "work", Kind: authority.KindToolCall, Subject: call.Tool,
				SubjectDigest: call.Digest, Input: call.Input, Summary: call.Summary, RequestedBy: "lane:lane-1",
			}
			if err := control.NewApprovals(w.store).Raise(w.rig.Ctx, w.rig.Scope, request, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "approval-decide", Key: request.ID, ClaimID: call.Digest}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, authority.EventApprovalChanged, spec.Key, 2); err != nil {
				return err
			}
			row, err := control.NewApprovals(w.store).Get(w.rig.Ctx, w.rig.Scope, spec.Key)
			if err != nil {
				return err
			}
			if row.State != authority.ApprovalApproved {
				return contract.Violatedf("the request reads %s", row.State)
			}
			return nil
		},
	}
}

func roleApprovalDecide(c *child) error {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	_, err := control.NewApprovals(c.store).Decide(c.ctx, c.scope, authority.Decision{ID: c.spec.Key, Digest: c.spec.ClaimID, Allow: true}, member)
	return err
}

// raiseCase kills a process as a request reaches the ledger, and a retry raises one request.
func raiseCase() crashCase {
	return crashCase{
		name: "raise a request", op: "approval.raise", steps: withSteps("inserted"),
		prepare: func(w *world) (childSpec, error) {
			return childSpec{Role: "approval-raise", Key: w.rig.ID("approval")}, nil
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, authority.EventApprovalChanged, spec.Key, 1)
		},
	}
}

func aRequest(id string) (authority.Approval, error) {
	call, err := authority.ReadToolCall("Bash", []byte(approvalInput))
	if err != nil {
		return authority.Approval{}, err
	}
	return authority.Approval{
		ID: id, Lane: "lane-1", WorkOrder: "work", Kind: authority.KindToolCall, Subject: call.Tool, SubjectDigest: call.Digest,
		Input: call.Input, Summary: call.Summary, RequestedBy: "lane:lane-1",
	}, nil
}

func roleApprovalRaise(c *child) error {
	request, err := aRequest(c.spec.Key)
	if err != nil {
		return err
	}
	return control.NewApprovals(c.store).Raise(c.ctx, c.scope, request, c.actor)
}

// useCase kills a process as a decision is spent on a call. The same call asks again and is let
// through, so a lost answer costs nothing, and the decision is spent once.
func useCase() crashCase {
	return crashCase{
		name: "spend a decision", op: "approval.use", steps: withSteps("spent"),
		prepare: func(w *world) (childSpec, error) {
			request, err := aRequest(w.rig.ID("approval"))
			if err != nil {
				return childSpec{}, err
			}
			approvals := control.NewApprovals(w.store)
			if err := approvals.Raise(w.rig.Ctx, w.rig.Scope, request, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			decision := authority.Decision{ID: request.ID, Digest: request.SubjectDigest, Allow: true}
			member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
			if _, err := approvals.Decide(w.rig.Ctx, w.rig.Scope, decision, member); err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "approval-use", Key: request.ID, ClaimID: request.SubjectDigest}, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, authority.EventApprovalChanged, spec.Key, 3); err != nil {
				return err
			}
			row, err := control.NewApprovals(w.store).Get(w.rig.Ctx, w.rig.Scope, spec.Key)
			if err != nil {
				return err
			}
			if row.State != authority.ApprovalUsed || row.UsedBy != approvalCall {
				return contract.Violatedf("the request reads %+v", row)
			}
			return nil
		},
	}
}

func roleApprovalUse(c *child) error {
	answer, err := control.NewApprovals(c.store).Check(c.ctx, c.scope, authority.ApprovalUse{
		Lane: "lane-1", Generation: 1, Subject: "Bash", Digest: c.spec.ClaimID, Call: approvalCall,
	})
	if err != nil {
		return err
	}
	if !answer.Allowed {
		return fmt.Errorf("the call whose answer was lost hears %+v", answer)
	}
	return nil
}

func approvalCases() []crashCase {
	return []crashCase{
		decideCase(),
		raiseCase(),
		useCase(),
		approvalCase("a request a lane raises", "worker.approval", "kept", "closed"),
	}
}

func approvalVariants() []variant {
	return []variant{
		{name: sabotageSplitsApproval, check: "crash", crash: func() (crashCase, string) {
			return approvalCase("a request in two writes", "worker.approval", "split"), "split"
		}},
		{name: "a decision that wakes no lane", control: control.Sabotage{LeavesApprovalWakes: true}, check: "AP5"},
	}
}
