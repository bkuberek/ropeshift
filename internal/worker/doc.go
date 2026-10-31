// Package worker is the worker plane's application layer: it holds the machine's
// connection to the control plane and converges on desired state. It is
// deterministic, so it exercises no judgment and grants itself nothing, and it
// imports no adapter and nothing from the control plane (FR-DEP-7).
package worker
