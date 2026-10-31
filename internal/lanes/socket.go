package lanes

import "ropeshift.dev/ropeshift/internal/workorders"

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
