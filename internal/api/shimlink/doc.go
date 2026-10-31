// Package shimlink is what a worker and a lane shim share: the versioned protocol on the
// shim's socket, the client, the journal, and the state a shim leaves on disk. A shim
// outlives its worker, so this surface is a contract between two builds, and it lives
// beside the other wire types. The process itself is internal/shim. The journal covers
// process death and not power loss, so it does not sync (D21).
package shimlink
