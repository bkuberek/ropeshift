package lanes

import (
	"encoding/json"

	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
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
	// Ask is what `ask` found, set when OK.
	Ask *questions.AskResult `json:"ask,omitempty"`
	// Context is what a read of the Context group found, set when OK.
	Context []ledger.ContextRecord `json:"context,omitempty"`
}
