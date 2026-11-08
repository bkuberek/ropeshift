package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const (
	// claimIDBytes sizes the ID a worker gives a claim its orchestrator made.
	claimIDBytes = 16
	// laneIDBytes sizes the name it gives a lane. A lane's name reaches a session's socket path,
	// which has a limit, and a clash only refuses the start.
	laneIDBytes = 6
)

// toolArgs is a tool's input read by exact name, because a decoder that folds a key's case would
// take a field the tool never declared for the one it did.
type toolArgs map[string]json.RawMessage

func readArgs(raw []byte) (toolArgs, error) {
	if len(raw) == 0 {
		return toolArgs{}, nil
	}
	var out toolArgs
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	return out, nil
}

func (a toolArgs) text(key string) string {
	var value string
	if raw, ok := a[key]; ok {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func (a toolArgs) texts(key string) []string {
	var value []string
	if raw, ok := a[key]; ok {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

// takeWork runs one of an orchestrator's tools. Only a token that stands for one reaches the ledger,
// so a lane's token is refused here, and every other rule is the ledger's.
func (w *Worker) takeWork(ctx context.Context, lane lanes.Record, request lanes.WorkRequest) lanes.Reply {
	if lane.Owner != lanes.OwnerOrchestrator {
		return refused(lanes.CodeForbidden, "this token cannot do this")
	}
	args, err := readArgs(request.Args)
	if err != nil {
		return refused(lanes.CodeInvalid, err.Error())
	}
	bounded, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()
	result, err := w.workTool(bounded, lane, request.Tool, args)
	switch {
	case err == nil:
		w.noteLedger(lane.ID, lanes.Reachable)
		return lanes.Reply{OK: true, Work: result}
	case errors.Is(err, workorders.ErrForbidden), errors.Is(err, questions.ErrOneWay):
		return refused(lanes.CodeForbidden, err.Error())
	}
	return refused(lanes.CodeInvalid, err.Error())
}

func (w *Worker) workTool(ctx context.Context, lane lanes.Record, tool string, args toolArgs) (json.RawMessage, error) {
	call := ledger.OrchestratorCall{
		Auth: w.Auth(), Epoch: w.epoch(), Lane: lane.ID, WorkOrder: args.text("work_order"), Question: args.text("question"), Reason: args.text("reason"),
	}
	act := func(verb string) (json.RawMessage, error) {
		reply, err := w.link.Orchestrate(ctx, verb, call)
		return reply.Result, err
	}
	switch tool {
	case lanes.ToolWorkCandidates:
		return act(ledger.VerbCandidates)
	case lanes.ToolWorkClaim:
		if reason := w.claimHeldBy(); reason != "" {
			return nil, fmt.Errorf("%w: %s", workorders.ErrInvalid, reason)
		}
		id, err := newToken(claimIDBytes)
		if err != nil {
			return nil, err
		}
		call.Target = id
		return act(ledger.VerbClaim)
	case lanes.ToolLaneStart:
		id, err := newToken(laneIDBytes)
		if err != nil {
			return nil, err
		}
		call.Target = "lane-" + id
		return act(ledger.VerbStart)
	case lanes.ToolLaneStop:
		return act(ledger.VerbStop)
	case lanes.ToolWorkDecline:
		return act(ledger.VerbDecline)
	case lanes.ToolQuestionNext:
		return act(ledger.VerbNext)
	case lanes.ToolQuestionAttach:
		return w.attach(ctx, call, args, act)
	case lanes.ToolDecisionRecord:
		return w.record(ctx, call, args, act)
	case lanes.ToolQuestionEscalate:
		return w.escalate(ctx, call, args, act)
	case lanes.ToolAcceptanceRecord:
		return act(ledger.VerbAccept)
	}
	return nil, fmt.Errorf("%w: no such tool: %q", workorders.ErrInvalid, tool)
}

// claimHeldBy says what a sign-in holds new work back for. An orchestrator claims on what the ledger
// can see, and only this machine knows its sign-in is near a limit.
func (w *Worker) claimHeldBy() string {
	facts := w.usageFacts("", false)
	if facts.SignedOut || facts.Limited || facts.NearLimit {
		return "this worker's sign-in is at or near its limit, so it starts no new work now"
	}
	return ""
}

type acting func(verb string) (json.RawMessage, error)

// climb stands a question on the rung an act needs, and records what each rung it passed considered,
// which is what the gate reads. A hold it already has is kept, and a rung an earlier call passed is
// not passed again, so a call that failed part way can be made again.
func (w *Worker) climb(ctx context.Context, call ledger.OrchestratorCall, args toolArgs, act acting, upTo int64) error {
	raw, err := act(ledger.VerbTake)
	if err != nil {
		return err
	}
	var question questions.Question
	if err := json.Unmarshal(raw, &question); err != nil {
		return err
	}
	steps := []questions.Step{
		{Rung: questions.RungLookup, Candidates: args.texts("lookup"), Outcome: "no_match"},
		{Rung: questions.RungOpen, Candidates: args.texts("open"), Outcome: "no_match"},
		{Rung: questions.RungDecide, Outcome: "not_decided"},
	}
	for _, step := range steps {
		if question.Rung != step.Rung || step.Rung >= upTo {
			continue
		}
		body, err := json.Marshal(step)
		if err != nil {
			return err
		}
		call.Body = body
		reply, err := w.link.Orchestrate(ctx, ledger.VerbClimb, call)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(reply.Result, &question); err != nil {
			return err
		}
	}
	return nil
}

// attach is rung 2: this question is the same as an open one. Rung 1 ran first, and what it
// considered stays on record.
func (w *Worker) attach(ctx context.Context, call ledger.OrchestratorCall, args toolArgs, act acting) (json.RawMessage, error) {
	if err := w.climb(ctx, call, args, act, questions.RungOpen); err != nil {
		return nil, err
	}
	call.Target = args.text("into")
	reply, err := w.link.Orchestrate(ctx, ledger.VerbMerge, call)
	return reply.Result, err
}

// record is rungs 1, 3, and 4. A decision the ledger holds answers on rung 1, and what the policy lets
// an orchestrator decide or assume goes on rung 3 or 4, with the rungs before it on record.
func (w *Worker) record(ctx context.Context, call ledger.OrchestratorCall, args toolArgs, act acting) (json.RawMessage, error) {
	if answer := args.text("decision"); answer != "" {
		if _, err := act(ledger.VerbTake); err != nil {
			return nil, err
		}
		call.Target = answer
		reply, err := w.link.Orchestrate(ctx, ledger.VerbResolve, call)
		return reply.Result, err
	}
	status := questions.DecisionStatus(args.text("status"))
	rung := questions.RungDecide
	if status == questions.DecisionAssumed {
		rung = questions.RungAssume
	}
	if err := w.climb(ctx, call, args, act, rung); err != nil {
		return nil, err
	}
	id, err := newToken(questionIDBytes)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(questions.Ruling{
		ID: id, Status: status, Statement: args.text("statement"), Rationale: args.text("rationale"), Citations: args.texts("citations"),
		Scope: args.text("scope"), Reversibility: args.text("reversibility"), Categories: args.texts("categories"),
		Supersedes: args.text("supersedes"), NoConflict: args.texts("no_conflict"),
	})
	if err != nil {
		return nil, err
	}
	call.Body = body
	reply, err := w.link.Orchestrate(ctx, ledger.VerbRule, call)
	return reply.Result, err
}

// escalate is rung 5. The control plane's gate decides, and a refusal names what is missing.
func (w *Worker) escalate(ctx context.Context, call ledger.OrchestratorCall, args toolArgs, act acting) (json.RawMessage, error) {
	if err := w.climb(ctx, call, args, act, questions.RungDecide); err != nil {
		return nil, err
	}
	body, err := json.Marshal(questions.Escalation{
		Reason: args.text("reason"), Categories: args.texts("categories"), Options: args.texts("options"), Recommendation: args.text("recommendation"),
	})
	if err != nil {
		return nil, err
	}
	call.Body = body
	reply, err := w.link.Orchestrate(ctx, ledger.VerbEscalate, call)
	return reply.Result, err
}
