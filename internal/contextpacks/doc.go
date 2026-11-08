// Package contextpacks holds what a lane starts from: which records a pack selects, in what order,
// how they fit a token budget, and the canonical form its digest covers. It holds no model and no
// client, because a pack is built at claim time from what the ledger holds (FR-CTX-1, FR-CTX-4).
package contextpacks
