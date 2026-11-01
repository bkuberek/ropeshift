package fake

import (
	"context"
	"errors"
	"sync"

	"ropeshift.dev/ropeshift/internal/ports"
)

// HookRunner records the commands it gets, and runs none. Programs answer the contract's
// commands by name, so the same checks run here as on real processes.
type HookRunner struct {
	mu       sync.Mutex
	runs     []ports.HookCommand
	fail     error
	Programs map[string]func(ports.HookCommand) ports.HookResult
}

// NewHookRunner returns a runner that passes every command.
func NewHookRunner() *HookRunner {
	return &HookRunner{Programs: map[string]func(ports.HookCommand) ports.HookResult{}}
}

// Fail makes every run fail, until it is given nil.
func (h *HookRunner) Fail(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fail = err
}

// Runs lists every command, in order.
func (h *HookRunner) Runs() []ports.HookCommand {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ports.HookCommand{}, h.runs...)
}

// Run records one command.
func (h *HookRunner) Run(_ context.Context, command ports.HookCommand) (ports.HookResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(command.Argv) == 0 {
		return ports.HookResult{}, errors.New("a hook names no command")
	}
	h.runs = append(h.runs, command)
	if h.fail != nil {
		return ports.HookResult{Exit: ports.ExitStatus{Code: 1}, Output: []byte(h.fail.Error())}, nil
	}
	if program, ok := h.Programs[command.Argv[0]]; ok {
		return program(command), nil
	}
	return ports.HookResult{}, nil
}

var _ ports.HookRunner = (*HookRunner)(nil)
