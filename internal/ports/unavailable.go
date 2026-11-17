package ports

import "errors"

// ErrRepositoryReadsUnavailable says a control plane reads no repository on its own machine: a group
// server keeps no clone, so each read that solo mode makes from one is refused, and the error that wraps
// this one names the read.
var ErrRepositoryReadsUnavailable = errors.New("repository_reads_unavailable")

// ErrPaneRelayUnavailable says a control plane relays no pane of a worker's machine, so a member's view of
// a lane's terminal is refused, and the error that wraps this one names the view.
var ErrPaneRelayUnavailable = errors.New("pane_relay_unavailable")
