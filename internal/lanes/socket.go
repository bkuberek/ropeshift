package lanes

import (
	"encoding/json"

	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The variables of a lane's process. A hook reads only these, because the CLI strips
// other tags from what reaches a hook.
const (
	EnvLaneID       = TagPrefix + "LANE_ID"
	EnvGeneration   = TagPrefix + "GENERATION_ID"
	EnvWorkOrder    = TagPrefix + "WORK_ORDER"
	EnvLaneToken    = TagPrefix + "LANE_TOKEN"
	EnvWorkerSocket = TagPrefix + "WORKER_SOCKET"
	EnvSpool        = TagPrefix + "SPOOL"
	EnvPolicy       = TagPrefix + "POLICY"
	// EnvPortBase is the first port of a lane's block.
	EnvPortBase = TagPrefix + "PORT_BASE"
)

// EnvMessagingSocket is the one vendor variable a hook reads. The CLI exports it before
// any hook runs, and a pane has no other way to tell a worker where its socket is.
const EnvMessagingSocket = "CLAUDE_CODE_MESSAGING_SOCKET"

// ToolLaneReport is the tool that ends a lane, ToolLaneWait the one that says it is
// waiting on something outside it, ToolAsk the one that asks a question, ToolLaneHandoff
// the one that writes a lane's part of a handoff, and ToolReply the one that answers a message.
const (
	ToolLaneReport  = "lane_report"
	ToolLaneWait    = "lane_wait"
	ToolAsk         = "ask"
	ToolLaneHandoff = "lane_handoff"
	ToolReply       = "reply"
)

// The Context group: a lane reaches the same knowledge a pack holds, so a pack is where a lane
// starts and not where it stops (FR-CTX-2).
const (
	ToolContextGet     = contextpacks.ToolGet
	ToolDecisionSearch = "decision_search"
	ToolRequirementGet = "requirement_get"
	ToolQuestionSearch = "question_search"
)

// Tools are a lane's MCP tools. Generated settings allow exactly these, so a lane
// reports its outcome with no prompt (ADR-0014).
var Tools = []string{
	ToolLaneReport, ToolLaneWait, ToolAsk, ToolLaneHandoff, ToolReply, ToolContextGet, ToolDecisionSearch, ToolRequirementGet, ToolQuestionSearch,
}

// ToolApprovalDecide decides a request a member delegated (FR-AUTH-6).
const ToolApprovalDecide = "approval_decide"

// The Work group: an orchestrator pulls work, and nothing is assigned to it (FR-ORCH-3).
const (
	ToolWorkCandidates = "work_candidates"
	ToolWorkClaim      = "work_claim"
	ToolLaneStart      = "lane_start"
	ToolLaneStop       = "lane_stop"
)

// The Ladder group: one rung at a time, on a question this orchestrator holds.
const (
	ToolQuestionNext     = "question_next"
	ToolQuestionAttach   = "question_attach"
	ToolDecisionRecord   = "decision_record"
	ToolQuestionEscalate = "question_escalate"
)

// ToolWorkDecline is the one act the design gives an orchestrator with no group of its own (D2). It has no
// tool to accept work: acceptance rests on a review's verdicts and on forge checks (ADR-0014).
const ToolWorkDecline = "work_decline"

// OrchestratorTools is what `ropeshift mcp` lists for an orchestrator's token, and all of them. More
// tools raise prompt cost and widen authority, so a further one needs a need a trial named (ADR-0014).
var OrchestratorTools = []string{
	ToolContextGet, ToolDecisionSearch, ToolRequirementGet, ToolQuestionSearch,
	ToolWorkCandidates, ToolWorkClaim, ToolLaneStart, ToolLaneStop,
	ToolQuestionNext, ToolQuestionAttach, ToolDecisionRecord, ToolQuestionEscalate,
	ToolWorkDecline, ToolApprovalDecide,
}

// WorkRequest is one call of an orchestrator's tool. Args go as the session wrote them, so the
// worker reads each by its exact name.
type WorkRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args,omitempty"`
}

// WaitDeclaration is a lane's word that it waits on an external process.
type WaitDeclaration struct {
	Reason  string `json:"reason,omitempty"`
	Minutes int64  `json:"minutes"`
}

// AskRequest is a lane's question. Options are what it would choose between, when it knows.
type AskRequest struct {
	Text       string   `json:"text"`
	Categories []string `json:"categories,omitempty"`
	Blocking   bool     `json:"blocking,omitempty"`
	Options    []string `json:"options,omitempty"`
}

// ReplyRequest is a lane's answer to one message. ReplyTo names the message, which is what makes
// this the only semantic confirmation a sender gets (FR-MSG-3).
type ReplyRequest struct {
	ReplyTo string `json:"reply_to"`
	Body    string `json:"body"`
}

// SocketProtocol is the version of the worker socket's lines.
const SocketProtocol = 1

// The refusals on the worker socket.
const (
	CodeBadToken        = "bad_token"
	CodeInvalid         = "invalid"
	CodeAlreadyReported = "already_reported"
	CodeUnavailable     = "unavailable"
	CodeFenced          = "fenced"
)

// Request is one line on the worker socket. The token proves which process is
// calling, and the worker resolves the lane, generation, and fence from it.
type Request struct {
	Version int                `json:"v"`
	Token   string             `json:"token"`
	Hook    *HookEvent         `json:"hook,omitempty"`
	Report  *workorders.Report `json:"report,omitempty"`
	Wait    *WaitDeclaration   `json:"wait,omitempty"`
	Ask     *AskRequest        `json:"ask,omitempty"`
	// Handoff is a lane's narrative as it wrote it, so the worker judges what arrived.
	Handoff json.RawMessage `json:"handoff,omitempty"`
	Reply   *ReplyRequest   `json:"reply,omitempty"`
	Context *ContextRequest `json:"context,omitempty"`
	// Approval is a hook's look at a call that needs a decision.
	Approval *ApprovalRequest `json:"approval,omitempty"`
	// Decide is an orchestrator's decision, which a lane's token cannot make.
	Decide *DecideRequest `json:"decide,omitempty"`
	// Work is an orchestrator's tool, which a lane's token cannot reach.
	Work *WorkRequest `json:"work,omitempty"`
	// Who asks whom this token stands for, which only the worker can say.
	Who bool `json:"who,omitempty"`
}

// LaneOnly says a line asks for what only a lane does. An orchestrator has no work to report on, no
// lane to ask for, and no message to answer.
func (r Request) LaneOnly() bool {
	return r.Report != nil || r.Wait != nil || r.Ask != nil || r.Reply != nil
}

// ContextRequest is one read of the Context group. A search reads Query, and a lookup reads Kind
// and Ref.
type ContextRequest struct {
	Tool  string `json:"tool"`
	Kind  string `json:"kind,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// Reply is the one line that answers it.
type Reply struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// Generation is the one this lane is in now, on an answer to a hook, because a clear moves it
	// under a process whose tags cannot change.
	Generation int64 `json:"generation,omitempty"`
	// Ask is what `ask` found, set when OK.
	Ask *questions.AskResult `json:"ask,omitempty"`
	// Context is what a read of the Context group found, set when OK.
	Context []ledger.ContextRecord `json:"context,omitempty"`
	// Approval is what a look at a call found, set when OK.
	Approval *ApprovalReply `json:"approval,omitempty"`
	// Owner answers Who, and a lane's token has none.
	Owner string `json:"owner,omitempty"`
	// Work is what an orchestrator's tool found, set when OK.
	Work json.RawMessage `json:"work,omitempty"`
}

// What a look at a call can answer.
const (
	// ApprovalWaiting says nobody has decided, and a hook goes on waiting.
	ApprovalWaiting = "waiting"
	// ApprovalGoesOn says a decision stands for this call. A peek spends nothing, so the call goes on
	// to the hook that spends it.
	ApprovalGoesOn = "goes_on"
	// ApprovalAllow says this call may run.
	ApprovalAllow = "allow"
	// ApprovalDeny says it may not, and Reason reaches the model.
	ApprovalDeny = "deny"
)

// ApprovalRequest is one look a hook takes at its call. Tool and Input are the call as the CLI gave
// them, so a worker reads the digest and the summary itself, and a lane has no say in either.
type ApprovalRequest struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
	// Call names this one call, so a lost answer reaches it and nothing else.
	Call string `json:"call"`
	// Peek looks at a call before a turn ends: it spends nothing, and asks for what a hook needs to
	// defer the turn.
	Peek bool `json:"peek,omitempty"`
	// Raised says an earlier look reached a worker, so a request stands, and an end to it is an
	// answer. A first look at a call that ended raises it again.
	Raised bool `json:"raised,omitempty"`
	// Held names the request an earlier answer gave this hook, so a worker can tell that the call it
	// holds now lands somewhere else than that request says, and close the request for good.
	Held string `json:"held,omitempty"`
}

// ApprovalReply is what a look found.
type ApprovalReply struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	ID       string `json:"id,omitempty"`
	// WaitMs is how much longer this call waits.
	WaitMs int64 `json:"wait_ms,omitempty"`
}

// DecideRequest is what an orchestrator decides, with the digest it read.
type DecideRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

// CodeForbidden refuses what this caller's kind of token cannot do.
const CodeForbidden = "forbidden"

// ReasonApprovalWait reaches the model when a request outlasts its wait.
const ReasonApprovalWait = "ropeshift policy: this call needs an approval, and none came within this lane's wait. The request stands. End your turn: the lane parks, and it resumes once the request is decided."

// ReasonNoApproval reaches the model when no request could be recorded.
const ReasonNoApproval = "ropeshift policy: this call needs an approval, and the worker could not record the request. Nothing ran."

// ReasonRequestClosed reaches the model when a call's request ended with no decision for it.
const ReasonRequestClosed = "ropeshift policy: the request for this call has closed, so nothing allows it. Make the call again to ask again."
