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
	// opts names the daemon's sources, each the checkout of its record in the first project.
	opts soloOptions

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
	host, err := h.host(ctx, project)
	if err != nil {
		return nil, err
	}
	hosts[w.Auth().WorkerID] = host
	return hosts, nil
}

// host is what the daemon's own worker could run a project's assistant on. In the first project, each daemon source
// is the checkout of its record, as a lane's start reads it (checkoutOf), so the worker holds that repository
// before a member keeps a checkout of it with `repository add`, and its orchestrator serves the project. A source
// that cannot be read counts as no checkout.
func (h *soloHosts) host(ctx context.Context, project ledger.WorkspaceID) (control.AssistantHost, error) {
	host := control.AssistantHost{Checkouts: map[string]bool{}}
	if h.runtime != nil && h.runtime.Driver != nil {
		if capabilities, err := h.runtime.Driver.Capabilities(ctx); err == nil {
			host.Headless = slices.Contains(capabilities.RunModes, ports.RunModeHeadless)
		}
	}
	if h.kept != nil {
		checkouts, err := h.kept.Of(ctx, project)
		if err != nil {
			return control.AssistantHost{}, err
		}
		for repository := range checkouts {
			host.Checkouts[repository] = true
		}
	}
	if project == soloScope {
		for _, path := range h.opts.sources {
			// A source git cannot read is the checkout of no record, and the sources after it still are.
			instance, ok, err := sourceInstance(ctx, h.opts, h.kept, project, path)
			if err != nil {
				continue
			}
			if ok {
				host.Checkouts[instance] = true
			}
		}
	}
	return host, nil
}
