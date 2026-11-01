package hook

import (
	"io"
	"slices"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
)

// flagPass copies the input to stdout, for the person's own status line after this one
// in a pipe. Generated settings choose it, because the CLI's shell runs that command and
// this binary starts none.
const flagPass = "--pass"

// StatusMain is `ropeshift statusline`, the telemetry half of an interactive lane's status
// line. It follows the hook contract, and it never spools: only the latest value means
// anything, so a report no worker takes is not worth keeping. It reports before it passes
// the input on, because a person's command may never read it, and a write to that closed
// pipe would end this process first.
func StatusMain(args []string, stdin io.Reader, stdout io.Writer, getenv func(string) string) int {
	defer func() { _ = recover() }()
	input, _ := io.ReadAll(io.LimitReader(stdin, maxInput))
	cfg := read(lanes.EventStatusLine, getenv)
	if cfg.socket != "" {
		timer := time.AfterFunc(hardDeadline, func() { expire(cfg, stdout) })
		defer timer.Stop()
		event := lanes.NewStatusEvent(input)
		event.Lane, event.Generation, event.AtMs = cfg.lane, cfg.generation, time.Now().UnixMilli()
		request := lanes.Request{Version: lanes.SocketProtocol, Token: cfg.token, Hook: &event}
		_, _ = api.CallLane(cfg.socket, request, dialDeadline, ackDeadline)
	}
	if slices.Contains(args, flagPass) {
		_, _ = stdout.Write(input)
	}
	return 0
}
