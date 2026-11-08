// Package shim is `ropeshift lane-shim`, a small per-lane process that owns a headless
// session's stdin and stdout, so the session outlives the worker that started it
// (FR-LANE-17). It journals every line with a contiguous sequence number, and a client
// reads that journal, so catch-up and live delivery are one path. It never stops
// reading, so an absent worker costs disk and not progress.
package shim
