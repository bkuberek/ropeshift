package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
)

// The variables of a lane's process, for the clients that read them.
const (
	EnvLaneID       = lanes.EnvLaneID
	EnvGeneration   = lanes.EnvGeneration
	EnvWorkOrder    = lanes.EnvWorkOrder
	EnvLaneToken    = lanes.EnvLaneToken
	EnvWorkerSocket = lanes.EnvWorkerSocket
	EnvSpool        = lanes.EnvSpool
	EnvPolicy       = lanes.EnvPolicy
)

// The worker's local files.
const (
	WorkerSocketName = "worker.sock"
	SpoolFileName    = "hooks.spool"
	stableBinaryDir  = "bin"
	stableBinaryName = "ropeshift"
)

// WorkerSocket is the socket hooks and `ropeshift mcp` reach.
func (p Paths) WorkerSocket() string { return filepath.Join(p.RunDir, WorkerSocketName) }

// Spool is where a hook appends what the worker could not take.
func (p Paths) Spool() string { return filepath.Join(p.StateDir, SpoolFileName) }

// StableBinary is the link generated settings use (D18), so an upgrade that removes
// a versioned path cannot break a running lane.
func (p Paths) StableBinary() string {
	return filepath.Join(p.StateDir, stableBinaryDir, stableBinaryName)
}

// ErrNoWorker means nothing answered on the worker socket in time.
var ErrNoWorker = errors.New("the worker did not answer")

// CallLane sends one line and reads one line. The deadlines are the caller's, because
// a hook has milliseconds and a tool call has seconds.
func CallLane(socket string, request lanes.Request, dial, answer time.Duration) (lanes.Reply, error) {
	line, err := json.Marshal(request)
	if err != nil {
		return lanes.Reply{}, err
	}
	conn, err := net.DialTimeout("unix", socket, dial)
	if err != nil {
		return lanes.Reply{}, errors.Join(ErrNoWorker, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(answer)); err != nil {
		return lanes.Reply{}, errors.Join(ErrNoWorker, err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return lanes.Reply{}, errors.Join(ErrNoWorker, err)
	}
	raw, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return lanes.Reply{}, errors.Join(ErrNoWorker, err)
	}
	var reply lanes.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return lanes.Reply{}, errors.Join(ErrNoWorker, err)
	}
	return reply, nil
}
