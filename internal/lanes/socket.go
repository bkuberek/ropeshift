package lanes

import "ropeshift.dev/ropeshift/internal/workorders"

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
)

// EnvMessagingSocket is the one vendor variable a hook reads. The CLI exports it before
// any hook runs, and a pane has no other way to tell a worker where its socket is.
const EnvMessagingSocket = "CLAUDE_CODE_MESSAGING_SOCKET"

// ToolLaneReport is the tool that ends a lane.
const ToolLaneReport = "lane_report"

// Tools are a lane's MCP tools. Generated settings allow exactly these, so a lane
// reports its outcome with no prompt (ADR-0014).
var Tools = []string{ToolLaneReport}

// SocketProtocol is the version of the worker socket's lines.
const SocketProtocol = 1

// The refusals on the worker socket.
const (
	CodeBadToken        = "bad_token"
	CodeInvalid         = "invalid"
	CodeAlreadyReported = "already_reported"
	CodeUnavailable     = "unavailable"
)

// Request is one line on the worker socket. The token proves which process is
// calling, and the worker resolves the lane, generation, and fence from it.
type Request struct {
	Version int                `json:"v"`
	Token   string             `json:"token"`
	Hook    *HookEvent         `json:"hook,omitempty"`
	Report  *workorders.Report `json:"report,omitempty"`
}

// Reply is the one line that answers it.
type Reply struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
