package entry

import (
	"context"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// soloHosts says what the daemon's own worker could run a project's assistant on: whether its driver offers
// a headless session, and the repositories it keeps a checkout of. A worker on another machine says nothing
// here, so solo mode runs the assistant on its own worker only.
type soloHosts struct {
	kept    ports.Checkouts
	runtime *worker.LaneRuntime

	mu     sync.Mutex
	worker *worker.Worker
}

// serve names the worker this daemon runs, once it exists.
func (h *soloHosts) serve(w *worker.Worker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.worker = w
}

func (h *soloHosts) read(ctx context.Context, project ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
	h.mu.Lock()
	w := h.worker
	h.mu.Unlock()
	hosts := map[string]control.AssistantHost{}
	if w == nil || w.Auth().WorkerID == "" {
		return hosts, nil
	}
	host := control.AssistantHost{Checkouts: map[string]bool{}}
	if h.runtime != nil && h.runtime.Driver != nil {
		if capabilities, err := h.runtime.Driver.Capabilities(ctx); err == nil {
			host.Headless = slices.Contains(capabilities.RunModes, ports.RunModeHeadless)
		}
	}
	if h.kept != nil {
		checkouts, err := h.kept.Of(ctx, project)
		if err != nil {
			return nil, err
		}
		for repository := range checkouts {
			host.Checkouts[repository] = true
		}
	}
	hosts[w.Auth().WorkerID] = host
	return hosts, nil
}
